package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
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

	// PreviewRuns é quantos disparos a prévia do cron mostra.
	PreviewRuns = 5

	DefaultPageSize int32 = 50
	MaxPageSize     int32 = 100
)

var (
	ErrNotFound     = errors.New("heartbeat não encontrado")
	ErrLimitReached = errors.New("limite de heartbeats atingido")
	// ErrScheduleUnsupported recusa editar a linha cujo agendamento veio de
	// uma versão posterior: esta não saberia regravá-lo nem recalcular o prazo.
	ErrScheduleUnsupported = errors.New("agendamento não suportado nesta versão")
)

// Status é o que o painel mostra, derivado do estado e do relógio: nada disto
// é gravado.
type Status string

const (
	StatusPaused  Status = "paused"
	StatusFailing Status = "failing"
	StatusLate    Status = "late"
	StatusWaiting Status = "waiting"
	// StatusGrace é a janela de tolerância antes do prazo: o sinal ainda não
	// veio, mas não está atrasado. Não pede atenção.
	StatusGrace Status = "grace"
	StatusUp    Status = "up"
)

// Heartbeat é o que sai do domínio. Não tem campo para o token nem para o
// hash: nenhuma leitura os devolve.
type Heartbeat struct {
	ID             uuid.UUID
	Name           string
	Description    string
	Tags           []string
	Enabled        bool
	Schedule       Schedule
	Grace          time.Duration
	Status         Status
	DeadlineAt     time.Time
	LastPingAt     *time.Time
	LastSuccessAt  *time.Time
	LastStartAt    *time.Time
	LastRuntimeMS  *int64
	OpenIncidentID *uuid.UUID
	CreatedAt      time.Time
}

// Input é o cadastro. O agendamento já chega validado (NewPeriod, NewCron).
type Input struct {
	Name        string
	Description string
	Tags        []string
	Schedule    Schedule
	Grace       time.Duration
	Enabled     bool
}

// Patch: nil é "não mexa". Tags não seguem a regra: nil e vazio limpam, e quem
// diz se elas vieram é SetTags.
type Patch struct {
	SetTags     bool
	Tags        []string
	Name        *string
	Description *string
	Schedule    *Schedule
	Grace       *time.Duration
	Enabled     *bool
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
	// depoisDeGravar existe para o teste da resposta: roda depois de gravar,
	// antes de montar o que volta, ainda dentro da transação.
	depoisDeGravar func()
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

// ValidateDescription devolve a descrição limpa; "" é "sem descrição". É texto
// de várias linhas, e por isso as regras de ValidateName valem linha a linha:
// quebra de linha e tab são os únicos controles que um texto colado precisa. O
// erro de caractere aponta a linha em description[i], contada desde 0 no texto
// recebido, como items[i] na página de status.
func ValidateDescription(description string) (string, error) {
	limpa := strings.TrimSpace(description)
	if utf8.RuneCountInString(limpa) > MaxDescriptionLength {
		return "", &ValidationError{Field: "description", Reason: ReasonOutOfRange}
	}
	indice := strings.Count(description[:strings.Index(description, limpa)], "\n")
	for linha := range strings.Lines(strings.ReplaceAll(limpa, "\t", " ")) {
		if rotulo.TemInvisivel(strings.TrimSuffix(linha, "\n")) {
			return "", &ValidationError{Field: fmt.Sprintf("description[%d]", indice), Reason: ReasonInvalid}
		}
		indice++
	}
	return limpa, nil
}

// CronPreview são os próximos disparos de uma expressão ainda não gravada. Passa
// pela mesma validação do cadastro, e não toca no banco.
func (s *Service) CronPreview(expr, timezone string) ([]time.Time, error) {
	sch, err := NewCron(expr, timezone)
	if err != nil {
		return nil, err
	}
	return sch.NextRuns(s.clk.Now(), PreviewRuns), nil
}

// Create grava o heartbeat e devolve o token, que só existe neste retorno.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Heartbeat, string, error) {
	nome, err := ValidateName(in.Name)
	if err != nil {
		return Heartbeat{}, "", err
	}
	descricao, err := ValidateDescription(in.Description)
	if err != nil {
		return Heartbeat{}, "", err
	}
	if err := ValidateGrace(in.Grace); err != nil {
		return Heartbeat{}, "", err
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return Heartbeat{}, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Heartbeat{}, "", fmt.Errorf("gerar id de heartbeat: %w", err)
	}
	token, hash := NewPingToken()
	now := s.clk.Now()
	params := db.InsertHeartbeatParams{
		ID: id, UserID: userID, Name: nome, Description: descricao, PingTokenHash: hash, GraceSec: seconds(in.Grace),
		Enabled: in.Enabled, CreatedAt: now, DeadlineAt: FirstDeadline(in.Schedule, in.Grace, now),
	}
	params.PeriodSec, params.CronExpr, params.Timezone = scheduleColumns(in.Schedule)

	var out Heartbeat
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		out, err = s.inserir(ctx, q, userID, params, tags)
		return err
	})
	if err != nil {
		return Heartbeat{}, "", err
	}
	return out, token, nil
}

// inserir grava o heartbeat dentro do limite por usuário. A trava vem antes da
// contagem: sem ela, dois pedidos simultâneos contariam 99 e gravariam 101.
func (s *Service) inserir(ctx context.Context, q *db.Queries, userID uuid.UUID, params db.InsertHeartbeatParams, tags []string) (Heartbeat, error) {
	if err := q.LockUserHeartbeats(ctx, userID); err != nil {
		return Heartbeat{}, fmt.Errorf("travar heartbeats do usuário: %w", err)
	}
	n, err := q.CountHeartbeats(ctx, userID)
	if err != nil {
		return Heartbeat{}, fmt.Errorf("contar heartbeats: %w", err)
	}
	if s.depoisDeContar != nil {
		s.depoisDeContar()
	}
	if n >= MaxPerUser {
		return Heartbeat{}, ErrLimitReached
	}
	if err := q.InsertHeartbeat(ctx, params); err != nil {
		return Heartbeat{}, err
	}
	if err := q.InsertHeartbeatTags(ctx, db.InsertHeartbeatTagsParams{Tags: tags, UserID: userID, HeartbeatID: params.ID}); err != nil {
		return Heartbeat{}, fmt.Errorf("gravar tags do heartbeat: %w", err)
	}
	return s.responseOf(ctx, q, userID, params.ID)
}

// CloneSuffix marca o nome da cópia.
const CloneSuffix = " (cópia)"

// Clone grava uma tarefa nova com a configuração do original (nome com o
// sufixo, descrição, tags, agendamento, tolerância e enabled) e devolve o token
// novo, que só existe neste retorno. Não copia token, eventos, last_*, falha,
// incidentes nem a retomada: a cópia nasce como uma criação, e conta no limite.
func (s *Service) Clone(ctx context.Context, userID, id uuid.UUID) (Heartbeat, string, error) {
	novoID, err := uuid.NewV7()
	if err != nil {
		return Heartbeat{}, "", fmt.Errorf("gerar id de heartbeat: %w", err)
	}
	token, hash := NewPingToken()
	var out Heartbeat
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		original, err := q.GetHeartbeat(ctx, db.GetHeartbeatParams{UserID: userID, ID: id})
		if err != nil {
			return notFound(err)
		}
		params, err := s.cloneParams(original, novoID, hash)
		if err != nil {
			return err
		}
		tags, err := q.ListHeartbeatTags(ctx, db.ListHeartbeatTagsParams{UserID: userID, HeartbeatID: id})
		if err != nil {
			return fmt.Errorf("ler tags do heartbeat: %w", err)
		}
		out, err = s.inserir(ctx, q, userID, params, tags)
		return err
	})
	if err != nil {
		return Heartbeat{}, "", err
	}
	return out, token, nil
}

func (s *Service) cloneParams(original db.Heartbeat, id uuid.UUID, hash []byte) (db.InsertHeartbeatParams, error) {
	sch, err := ScheduleFromRow(original.PeriodSec, original.CronExpr, original.Timezone)
	if err != nil {
		return db.InsertHeartbeatParams{}, fmt.Errorf("agendamento do heartbeat %s: %w", original.ID, err)
	}
	// A cópia regrava o agendamento por scheduleColumns, que não conhece o de
	// uma versão posterior, e FirstDeadline não saberia calcular o prazo.
	if !sch.IsKnown() {
		return db.InsertHeartbeatParams{}, ErrScheduleUnsupported
	}
	grace := time.Duration(original.GraceSec) * time.Second
	now := s.clk.Now()
	params := db.InsertHeartbeatParams{
		ID: id, UserID: original.UserID, Name: cloneName(original.Name), Description: original.Description,
		PingTokenHash: hash, GraceSec: original.GraceSec, Enabled: original.Enabled, CreatedAt: now,
		DeadlineAt: FirstDeadline(sch, grace, now),
	}
	params.PeriodSec, params.CronExpr, params.Timezone = scheduleColumns(sch)
	return params, nil
}

// cloneName põe o sufixo e corta o nome, por caractere, para a cópia caber em
// MaxNameLength. Só o corte deixa sobra no fim, e ela sai: espaço (dobraria o
// do sufixo), formatação como o ZWJ (Cf, que ValidateName recusaria no próximo
// PATCH com o mesmo nome) e marca combinante órfã do caractere-base cortado. O
// nome que cabe inteiro fica como está: já veio limpo do cadastro, e o
// combinante no fim dele é legítimo ("Café" decomposto, "❤️").
func cloneName(name string) string {
	caben := MaxNameLength - utf8.RuneCountInString(CloneSuffix)
	if runas := []rune(name); len(runas) > caben {
		name = strings.TrimRightFunc(string(runas[:caben]), soltoNoFim)
	}
	return name + CloneSuffix
}

func soltoNoFim(r rune) bool {
	return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.In(r, unicode.Mn, unicode.Me)
}

func normalizeTags(cruas []string) ([]string, error) {
	tags, motivo := rotulo.NormalizeTags(cruas)
	if motivo != "" {
		return nil, &ValidationError{Field: "tags", Reason: motivo}
	}
	return tags, nil
}

// responseOf monta o que volta dentro da transação que gravou. Na criação e na
// rotação, o token só existe no retorno: uma leitura que falhasse depois do
// commit perderia um token que já valeria (ou que já teria revogado o anterior).
func (s *Service) responseOf(ctx context.Context, q *db.Queries, userID, id uuid.UUID) (Heartbeat, error) {
	if s.depoisDeGravar != nil {
		s.depoisDeGravar()
	}
	return s.view(ctx, q, userID, id)
}

func (s *Service) view(ctx context.Context, q *db.Queries, userID, id uuid.UUID) (Heartbeat, error) {
	r, err := q.GetHeartbeatView(ctx, db.GetHeartbeatViewParams{UserID: userID, ID: id})
	if err != nil {
		return Heartbeat{}, notFound(err)
	}
	out, err := toHeartbeat(r.Heartbeat, r.OpenIncidentID, s.clk.Now())
	if err != nil {
		return Heartbeat{}, err
	}
	out.Tags, err = q.ListHeartbeatTags(ctx, db.ListHeartbeatTagsParams{UserID: userID, HeartbeatID: id})
	if err != nil {
		return Heartbeat{}, fmt.Errorf("ler tags do heartbeat: %w", err)
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Heartbeat, error) {
	var out Heartbeat
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		out, err = s.view(ctx, q, userID, id)
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
		tags, err := q.ListUserHeartbeatTags(ctx, userID)
		if err != nil {
			return fmt.Errorf("ler tags do dono: %w", err)
		}
		porHeartbeat := make(map[uuid.UUID][]string, len(rows))
		for _, t := range tags {
			porHeartbeat[t.HeartbeatID] = append(porHeartbeat[t.HeartbeatID], t.Tag)
		}
		out = make([]Heartbeat, 0, len(rows))
		now := s.clk.Now()
		for _, r := range rows {
			h, err := toHeartbeat(r.Heartbeat, r.OpenIncidentID, now)
			if err != nil {
				return err
			}
			if t, ok := porHeartbeat[h.ID]; ok {
				h.Tags = t
			}
			out = append(out, h)
		}
		return nil
	})
	return out, err
}

// Update aplica o patch com a linha travada. Só valor que muda recalcula o
// prazo, a partir de agora: reenviar o que já está gravado não adia nada.
// Retomar também recalcula, zera a falha e registra a retomada (D13). Quem
// fecha incidente é o avaliador, e prazo novo sozinho não fecha (§5 da spec).
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, p Patch) (Heartbeat, error) {
	if p.Name != nil {
		nome, err := ValidateName(*p.Name)
		if err != nil {
			return Heartbeat{}, err
		}
		p.Name = &nome
	}
	if p.Description != nil {
		descricao, err := ValidateDescription(*p.Description)
		if err != nil {
			return Heartbeat{}, err
		}
		p.Description = &descricao
	}
	if p.Grace != nil {
		if err := ValidateGrace(*p.Grace); err != nil {
			return Heartbeat{}, err
		}
	}
	if p.SetTags {
		tags, err := normalizeTags(p.Tags)
		if err != nil {
			return Heartbeat{}, err
		}
		p.Tags = tags
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
		if err := q.UpdateHeartbeat(ctx, params); err != nil {
			return err
		}
		if !p.SetTags {
			return nil
		}
		return replaceTags(ctx, q, userID, id, p.Tags)
	})
	if err != nil {
		return Heartbeat{}, err
	}
	return s.Get(ctx, userID, id)
}

func replaceTags(ctx context.Context, q *db.Queries, userID, id uuid.UUID, tags []string) error {
	if err := q.DeleteHeartbeatTags(ctx, db.DeleteHeartbeatTagsParams{UserID: userID, HeartbeatID: id}); err != nil {
		return fmt.Errorf("apagar tags do heartbeat: %w", err)
	}
	if err := q.InsertHeartbeatTags(ctx, db.InsertHeartbeatTagsParams{Tags: tags, UserID: userID, HeartbeatID: id}); err != nil {
		return fmt.Errorf("gravar tags do heartbeat: %w", err)
	}
	return nil
}

func (s *Service) updateParams(h db.Heartbeat, p Patch) (db.UpdateHeartbeatParams, error) {
	sch, err := ScheduleFromRow(h.PeriodSec, h.CronExpr, h.Timezone)
	if err != nil {
		return db.UpdateHeartbeatParams{}, fmt.Errorf("agendamento do heartbeat %s: %w", h.ID, err)
	}
	// Antes de qualquer cálculo: scheduleColumns gravaria período zero no
	// lugar do agendamento que esta versão não conhece, e FirstDeadline não
	// sabe calculá-lo.
	if !sch.IsKnown() {
		return db.UpdateHeartbeatParams{}, ErrScheduleUnsupported
	}
	grace := time.Duration(h.GraceSec) * time.Second
	out := db.UpdateHeartbeatParams{
		UserID: h.UserID, ID: h.ID, Name: h.Name, Description: h.Description, Enabled: h.Enabled,
		DeadlineAt: h.DeadlineAt, FailingSince: h.FailingSince, ResumedAt: h.ResumedAt,
		LastStartAt: h.LastStartAt,
	}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.Description != nil {
		out.Description = *p.Description
	}
	mudou := false
	if p.Schedule != nil && !p.Schedule.Equal(sch) {
		sch, mudou = *p.Schedule, true
	}
	if p.Grace != nil && *p.Grace != grace {
		grace, mudou = *p.Grace, true
	}
	if p.Enabled != nil {
		out.Enabled = *p.Enabled
	}
	retomou := out.Enabled && !h.Enabled
	now := s.clk.Now()
	if retomou {
		// O início aberto é de antes da pausa: mantê-lo mediria a pausa inteira
		// como execução no próximo sinal.
		out.LastStartAt = nil
		out.FailingSince = nil
		out.ResumedAt = &now
	}
	if retomou || mudou {
		out.DeadlineAt = FirstDeadline(sch, grace, now)
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

const (
	// AuditEventsCleared é a ação que limpar o log grava em audit_log.
	AuditEventsCleared = "heartbeat.events_cleared"
	auditEntity        = "heartbeat"
)

// ClearEvents apaga o log de eventos do heartbeat e deixa o rastro em
// audit_log, na mesma transação: não existe log apagado sem auditoria. Só os
// eventos saem; last_*, prazo e incidentes ficam (spec §7.2). O log já vazio
// também é auditado, porque o pedido aconteceu. Agendamento desconhecido não
// impede: nada aqui regrava o agendamento nem recalcula o prazo.
func (s *Service) ClearEvents(ctx context.Context, userID, id uuid.UUID) error {
	auditID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	entity := auditEntity
	return storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := exists(ctx, q, userID, id); err != nil {
			return err
		}
		if _, err := q.DeleteHeartbeatEvents(ctx, db.DeleteHeartbeatEventsParams{UserID: userID, HeartbeatID: id}); err != nil {
			return fmt.Errorf("apagar eventos: %w", err)
		}
		if err := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
			ID: auditID, UserID: &userID, Action: AuditEventsCleared, Entity: &entity, EntityID: &id, At: s.clk.Now(),
		}); err != nil {
			return fmt.Errorf("auditar %s: %w", AuditEventsCleared, err)
		}
		return nil
	})
}

// RotateToken revoga o token atual trocando o hash, e devolve o novo, que só
// existe neste retorno (D8).
func (s *Service) RotateToken(ctx context.Context, userID, id uuid.UUID) (Heartbeat, string, error) {
	token, hash := NewPingToken()
	var out Heartbeat
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if _, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{UserID: userID, ID: id}); err != nil {
			return notFound(err)
		}
		if err := q.SetHeartbeatPingHash(ctx, db.SetHeartbeatPingHashParams{UserID: userID, ID: id, PingTokenHash: hash}); err != nil {
			return fmt.Errorf("trocar o token: %w", err)
		}
		var err error
		out, err = s.responseOf(ctx, q, userID, id)
		return err
	})
	if err != nil {
		return Heartbeat{}, "", err
	}
	return out, token, nil
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

// seconds cabe em int32: as faixas validadas vão até 365 dias (31536000 s,
// CHECKs da migration heartbeat_limits), longe de 2^31-1.
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
		ID: h.ID, Name: h.Name, Description: h.Description, Tags: []string{}, Enabled: h.Enabled, Schedule: sch,
		Grace: time.Duration(h.GraceSec) * time.Second,
		Status: statusOf(statusFacts{
			Enabled: h.Enabled, Failing: h.FailingSince != nil, OpenIncident: open != nil,
			KnownSchedule: sch.IsKnown(), FinishedARun: finishedARun(h), RunOpen: h.LastStartAt != nil,
			Deadline: h.DeadlineAt, Grace: time.Duration(h.GraceSec) * time.Second,
		}, now),
		DeadlineAt: h.DeadlineAt, LastPingAt: h.LastPingAt, LastSuccessAt: h.LastSuccessAt,
		LastStartAt: h.LastStartAt, LastRuntimeMS: h.LastRuntimeMS, OpenIncidentID: open, CreatedAt: h.CreatedAt,
	}, nil
}

// finishedARun diz se a tarefa já terminou alguma execução, bem ou mal. Um início
// sozinho não prova nada: a tarefa nova pode morrer logo depois dele, e ficaria
// "em dia" até o prazo.
func finishedARun(h db.Heartbeat) bool {
	return h.LastSuccessAt != nil || (h.LastSignal != nil && Signal(*h.LastSignal) != SignalStart)
}

// statusFacts são os fatos da linha de que o estado derivado depende.
type statusFacts struct {
	Enabled, Failing, OpenIncident, KnownSchedule, FinishedARun, RunOpen bool
	Deadline                                                             time.Time
	Grace                                                                time.Duration
}

// O incidente aberto conta como atraso mesmo com prazo futuro: editar o
// agendamento move o prazo, e a tarefa continua devendo um sinal. Com
// agendamento desconhecido (linha de uma versão posterior) o prazo gravado não
// é confiável, e só o incidente aberto torna o estado atrasado.
func statusOf(f statusFacts, now time.Time) Status {
	switch {
	case !f.Enabled:
		return StatusPaused
	case f.Failing:
		return StatusFailing
	case f.OpenIncident, f.KnownSchedule && !f.Deadline.After(now):
		return StatusLate
	case !f.FinishedARun:
		return StatusWaiting
	case f.inGrace(now):
		return StatusGrace
	}
	return StatusUp
}

// inGrace diz se now cai na tolerância antes do prazo, [prazo - tolerância,
// prazo). Com agendamento desconhecido o prazo não é confiável, e com execução
// aberta a tarefa está trabalhando: nos dois casos não há o que tolerar.
func (f statusFacts) inGrace(now time.Time) bool {
	return f.KnownSchedule && !f.RunOpen && !now.Before(f.Deadline.Add(-f.Grace))
}
