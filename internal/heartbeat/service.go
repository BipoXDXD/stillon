package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/rotulo"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

const (
	// MaxPerUser é o teto de heartbeats por usuário, o mesmo de alvos (D16).
	MaxPerUser int64 = 100

	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100
)

var (
	ErrNotFound     = errors.New("heartbeat não encontrado")
	ErrLimitReached = errors.New("limite de heartbeats atingido")
)

// Status é o que o painel mostra, derivado do estado e do relógio: nada disto
// é gravado.
type Status string

const (
	StatusPaused  Status = "paused"
	StatusFailing Status = "failing"
	StatusLate    Status = "late"
	StatusWaiting Status = "waiting"
	StatusUp      Status = "up"
)

// Heartbeat é o que sai do domínio. Não tem campo para o token nem para o
// hash: nenhuma leitura os devolve.
type Heartbeat struct {
	ID             uuid.UUID
	Name           string
	Enabled        bool
	Schedule       Schedule
	Grace          time.Duration
	Status         Status
	DeadlineAt     time.Time
	LastPingAt     *time.Time
	LastSuccessAt  *time.Time
	LastStartAt    *time.Time
	OpenIncidentID *uuid.UUID
	CreatedAt      time.Time
}

// Input é o cadastro. O agendamento já chega validado (NewPeriod, NewCron).
type Input struct {
	Name     string
	Schedule Schedule
	Grace    time.Duration
	Enabled  bool
}

// Patch: nil é "não mexa".
type Patch struct {
	Name     *string
	Schedule *Schedule
	Grace    *time.Duration
	Enabled  *bool
}

// Incident é um incidente de heartbeat na visão do dono.
type Incident struct {
	ID            uuid.UUID
	HeartbeatID   uuid.UUID
	HeartbeatName string
	StartedAt     time.Time
	ResolvedAt    *time.Time
	Cause         string
	AckAt         *time.Time
}

// Service é o cadastro de heartbeats do dono.
type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem e a inserção, onde os dois POST precisam se encontrar.
	depoisDeContar func()
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

// ValidateName devolve o nome limpo.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &ValidationError{Field: "name", Reason: ReasonRequired}
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", &ValidationError{Field: "name", Reason: ReasonOutOfRange}
	case rotulo.TemInvisivel(name):
		return "", &ValidationError{Field: "name", Reason: ReasonInvalid}
	}
	return name, nil
}

// Create grava o heartbeat e devolve o token, que só existe neste retorno.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Heartbeat, string, error) {
	nome, err := ValidateName(in.Name)
	if err != nil {
		return Heartbeat{}, "", err
	}
	if err := ValidateGrace(in.Grace); err != nil {
		return Heartbeat{}, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Heartbeat{}, "", fmt.Errorf("gerar id de heartbeat: %w", err)
	}
	token, hash := NewPingToken()
	now := s.clk.Now()
	params := db.InsertHeartbeatParams{
		ID: id, UserID: userID, Name: nome, PingTokenHash: hash, GraceSec: seconds(in.Grace),
		Enabled: in.Enabled, CreatedAt: now, DeadlineAt: FirstDeadline(in.Schedule, in.Grace, now),
	}
	params.PeriodSec, params.CronExpr, params.Timezone = scheduleColumns(in.Schedule)

	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := q.LockUserHeartbeats(ctx, userID); err != nil {
			return fmt.Errorf("travar heartbeats do usuário: %w", err)
		}
		n, err := q.CountHeartbeats(ctx, userID)
		if err != nil {
			return fmt.Errorf("contar heartbeats: %w", err)
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n >= MaxPerUser {
			return ErrLimitReached
		}
		return q.InsertHeartbeat(ctx, params)
	})
	if err != nil {
		return Heartbeat{}, "", err
	}
	h, err := s.Get(ctx, userID, id)
	return h, token, err
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Heartbeat, error) {
	var out Heartbeat
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		r, err := q.GetHeartbeatView(ctx, db.GetHeartbeatViewParams{UserID: userID, ID: id})
		if err != nil {
			return notFound(err)
		}
		out, err = toHeartbeat(r.Heartbeat, r.OpenIncidentID, s.clk.Now())
		return err
	})
	return out, err
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, before *uuid.UUID, limit int32) ([]Heartbeat, error) {
	var out []Heartbeat
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		rows, err := q.ListHeartbeats(ctx, db.ListHeartbeatsParams{UserID: userID, Before: before, MaxRows: pageSize(limit)})
		if err != nil {
			return fmt.Errorf("listar heartbeats: %w", err)
		}
		out = make([]Heartbeat, 0, len(rows))
		now := s.clk.Now()
		for _, r := range rows {
			h, err := toHeartbeat(r.Heartbeat, r.OpenIncidentID, now)
			if err != nil {
				return err
			}
			out = append(out, h)
		}
		return nil
	})
	return out, err
}

// Update aplica o patch com a linha travada. Agendamento ou tolerância novos
// recalculam o prazo a partir de agora; retomar também, e zera a falha (D13).
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, p Patch) (Heartbeat, error) {
	if p.Name != nil {
		nome, err := ValidateName(*p.Name)
		if err != nil {
			return Heartbeat{}, err
		}
		p.Name = &nome
	}
	if p.Grace != nil {
		if err := ValidateGrace(*p.Grace); err != nil {
			return Heartbeat{}, err
		}
	}
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		h, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{UserID: userID, ID: id})
		if err != nil {
			return notFound(err)
		}
		params, err := s.updateParams(h, p)
		if err != nil {
			return err
		}
		return q.UpdateHeartbeat(ctx, params)
	})
	if err != nil {
		return Heartbeat{}, err
	}
	return s.Get(ctx, userID, id)
}

func (s *Service) updateParams(h db.Heartbeat, p Patch) (db.UpdateHeartbeatParams, error) {
	sch, err := ScheduleFromRow(h.PeriodSec, h.CronExpr, h.Timezone)
	if err != nil {
		return db.UpdateHeartbeatParams{}, fmt.Errorf("agendamento do heartbeat %s: %w", h.ID, err)
	}
	grace := time.Duration(h.GraceSec) * time.Second
	out := db.UpdateHeartbeatParams{
		UserID: h.UserID, ID: h.ID, Name: h.Name, Enabled: h.Enabled,
		DeadlineAt: h.DeadlineAt, FailingSince: h.FailingSince,
	}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.Schedule != nil {
		sch = *p.Schedule
	}
	if p.Grace != nil {
		grace = *p.Grace
	}
	if p.Enabled != nil {
		out.Enabled = *p.Enabled
	}
	retomou := out.Enabled && !h.Enabled
	if retomou {
		out.FailingSince = nil
	}
	if retomou || p.Schedule != nil || p.Grace != nil {
		out.DeadlineAt = FirstDeadline(sch, grace, s.clk.Now())
	}
	out.GraceSec = seconds(grace)
	out.PeriodSec, out.CronExpr, out.Timezone = scheduleColumns(sch)
	return out, nil
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		n, err := q.DeleteHeartbeat(ctx, db.DeleteHeartbeatParams{UserID: userID, ID: id})
		if err != nil {
			return fmt.Errorf("apagar heartbeat: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RotateToken revoga o token atual trocando o hash, e devolve o novo, que só
// existe neste retorno (D8).
func (s *Service) RotateToken(ctx context.Context, userID, id uuid.UUID) (Heartbeat, string, error) {
	token, hash := NewPingToken()
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if _, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{UserID: userID, ID: id}); err != nil {
			return notFound(err)
		}
		return q.SetHeartbeatPingHash(ctx, db.SetHeartbeatPingHashParams{UserID: userID, ID: id, PingTokenHash: hash})
	})
	if err != nil {
		return Heartbeat{}, "", err
	}
	h, err := s.Get(ctx, userID, id)
	return h, token, err
}

// Events é o log do heartbeat, do mais novo ao mais velho.
func (s *Service) Events(ctx context.Context, userID, id uuid.UUID, before *uuid.UUID, limit int32) ([]db.HeartbeatEvent, error) {
	var out []db.HeartbeatEvent
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := exists(ctx, q, userID, id); err != nil {
			return err
		}
		var err error
		out, err = q.ListHeartbeatEvents(ctx, db.ListHeartbeatEventsParams{
			UserID: userID, HeartbeatID: id, Before: before, MaxRows: pageSize(limit),
		})
		if err != nil {
			return fmt.Errorf("listar eventos: %w", err)
		}
		return nil
	})
	return out, err
}

// Incidents é o histórico do heartbeat. open nil lista tudo.
func (s *Service) Incidents(ctx context.Context, userID, id uuid.UUID, open *bool, before *uuid.UUID, limit int32) ([]Incident, error) {
	var out []Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := exists(ctx, q, userID, id); err != nil {
			return err
		}
		rows, err := q.ListHeartbeatIncidents(ctx, db.ListHeartbeatIncidentsParams{
			UserID: userID, HeartbeatID: id, Open: open, Before: before, MaxRows: pageSize(limit),
		})
		if err != nil {
			return fmt.Errorf("listar incidentes: %w", err)
		}
		out = make([]Incident, 0, len(rows))
		for _, r := range rows {
			out = append(out, Incident(r))
		}
		return nil
	})
	return out, err
}

// Ack reconhece o incidente. Idempotente: o segundo ack devolve o instante do
// primeiro, e reconhecer interrompe só os lembretes.
func (s *Service) Ack(ctx context.Context, userID, id, incidentID uuid.UUID) (Incident, error) {
	var out Incident
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		r, err := q.AckHeartbeatIncident(ctx, db.AckHeartbeatIncidentParams{
			UserID: userID, HeartbeatID: id, ID: incidentID, Now: s.clk.Now(),
		})
		if err != nil {
			return notFound(err)
		}
		out = Incident(r)
		return nil
	})
	return out, err
}

func exists(ctx context.Context, q *db.Queries, userID, id uuid.UUID) error {
	if _, err := q.GetHeartbeat(ctx, db.GetHeartbeatParams{UserID: userID, ID: id}); err != nil {
		return notFound(err)
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func pageSize(limit int32) int32 {
	if limit < 1 || limit > MaxPageSize {
		return DefaultPageSize
	}
	return limit
}

// seconds cabe em int32: as faixas validadas vão até 31 dias.
func seconds(d time.Duration) int32 { return int32(d / time.Second) } // #nosec G115 -- faixa validada (ValidateGrace, NewPeriod).

func scheduleColumns(s Schedule) (period *int32, expr, timezone *string) {
	if s.IsCron() {
		e, tz := s.CronExpr(), s.Timezone()
		return nil, &e, &tz
	}
	p := seconds(s.Period())
	return &p, nil, nil
}

func toHeartbeat(h db.Heartbeat, open *uuid.UUID, now time.Time) (Heartbeat, error) {
	sch, err := ScheduleFromRow(h.PeriodSec, h.CronExpr, h.Timezone)
	if err != nil {
		return Heartbeat{}, fmt.Errorf("agendamento do heartbeat %s: %w", h.ID, err)
	}
	return Heartbeat{
		ID: h.ID, Name: h.Name, Enabled: h.Enabled, Schedule: sch,
		Grace:      time.Duration(h.GraceSec) * time.Second,
		Status:     statusOf(h.Enabled, h.FailingSince != nil, h.DeadlineAt, h.LastPingAt != nil, now),
		DeadlineAt: h.DeadlineAt, LastPingAt: h.LastPingAt, LastSuccessAt: h.LastSuccessAt,
		LastStartAt: h.LastStartAt, OpenIncidentID: open, CreatedAt: h.CreatedAt,
	}, nil
}

func statusOf(enabled, failing bool, deadline time.Time, pinged bool, now time.Time) Status {
	switch {
	case !enabled:
		return StatusPaused
	case failing:
		return StatusFailing
	case !deadline.After(now):
		return StatusLate
	case !pinged:
		return StatusWaiting
	}
	return StatusUp
}
