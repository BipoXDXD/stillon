package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// ErrUnknownToken cobre token malformado, inexistente e revogado: quem chama
// não tem como distingui-los, e a resposta pública também não.
var ErrUnknownToken = errors.New("token de ping desconhecido")

// PingService recebe os sinais das tarefas. Só registra: quem abre e fecha
// incidente é o avaliador, no papel prober (D4 da spec).
type PingService struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewPingService(pool *pgxpool.Pool, clk clock.Clock) *PingService {
	return &PingService{pool: pool, clk: clk}
}

// Ping aplica sig ao heartbeat do token. runID é o ?rid= da execução, ou nil.
func (s *PingService) Ping(ctx context.Context, token string, sig Signal, runID *uuid.UUID) error {
	return s.aplicar(ctx, token, sinalRecebido{sig: sig, runID: runID, source: SourcePing})
}

// PingExit aplica o sinal que o código de saída representa, e o log guarda o
// número.
func (s *PingService) PingExit(ctx context.Context, token string, code ExitCode, runID *uuid.UUID) error {
	valor := code.Value()
	return s.aplicar(ctx, token, sinalRecebido{sig: code.Signal(), code: &valor, runID: runID, source: SourcePing})
}

// aplicar trava a linha, e isso serializa pings simultâneos: o segundo lê o que
// o primeiro gravou, e a repetição tem um efeito só, entre instâncias também
// (D9).
func (s *PingService) aplicar(ctx context.Context, token string, sinal sinalRecebido) error {
	hash, ok := HashPingToken(token)
	if !ok {
		return ErrUnknownToken
	}
	return storage.InPingTx(ctx, s.pool, hash, func(q *db.Queries) error {
		h, err := q.LockHeartbeatForPing(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownToken
		}
		if err != nil {
			return fmt.Errorf("travar heartbeat: %w", err)
		}
		return registrarSinal(ctx, q, h, sinal, s.clk.Now())
	})
}

// sinalRecebido é o que chegou, por ping ou pelo painel: o sinal, o código de
// saída (nil sem código), o rid (nil sem ?rid=) e a origem.
type sinalRecebido struct {
	sig    Signal
	code   *int16
	runID  *uuid.UUID
	source Source
}

// registrarSinal aplica s à linha h, já travada pela transação de q. O log só
// deixa o evento: a linha fica como estava.
func registrarSinal(ctx context.Context, q *db.Queries, h db.Heartbeat, s sinalRecebido, now time.Time) error {
	sch, err := ScheduleFromRow(h.PeriodSec, h.CronExpr, h.Timezone)
	if err != nil {
		return fmt.Errorf("agendamento do heartbeat %s: %w", h.ID, err)
	}
	run, err := execucaoDoSinal(ctx, q, h.ID, s)
	if err != nil {
		return err
	}
	grace := time.Duration(h.GraceSec) * time.Second
	estado, ev := stateOf(h).Apply(s.sig, now, run, sch, grace)
	if ev == nil {
		return nil
	}
	if s.sig != SignalLog {
		if err := q.SaveHeartbeatPing(ctx, pingParams(h, estado, s.sig, now)); err != nil {
			return fmt.Errorf("gravar ping: %w", err)
		}
	}
	return gravarEvento(ctx, q, h.ID, *ev, s)
}

// execucaoDoSinal lê do log o início de mesmo rid, que só o término de uma
// execução identificada usa. O sinal do painel é manual: não encerra execução.
func execucaoDoSinal(ctx context.Context, q *db.Queries, heartbeatID uuid.UUID, s sinalRecebido) (Run, error) {
	run := Run{ID: s.runID, Manual: s.source == SourcePanel}
	encerra := s.sig == SignalSuccess || s.sig == SignalFail
	if s.runID == nil || !encerra {
		return run, nil
	}
	at, err := q.GetRunStartAt(ctx, db.GetRunStartAtParams{HeartbeatID: heartbeatID, RunID: s.runID})
	if errors.Is(err, pgx.ErrNoRows) {
		return run, nil
	}
	if err != nil {
		return Run{}, fmt.Errorf("ler início da execução: %w", err)
	}
	run.StartAt = &at
	return run, nil
}

// gravarEvento grava a linha do log e apara o teto (D15) na mesma transação.
func gravarEvento(ctx context.Context, q *db.Queries, heartbeatID uuid.UUID, ev Event, s sinalRecebido) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de evento: %w", err)
	}
	if err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
		ID: id, HeartbeatID: heartbeatID, At: ev.At, Kind: string(ev.Kind), RuntimeMS: ev.RuntimeMS,
		ExitCode: s.code, RunID: ev.RunID, Source: string(s.source),
	}); err != nil {
		return fmt.Errorf("gravar evento: %w", err)
	}
	err = q.TrimHeartbeatEvents(ctx, db.TrimHeartbeatEventsParams{HeartbeatID: heartbeatID, MaxEvents: MaxEventsPerHeartbeat})
	if err != nil {
		return fmt.Errorf("aparar eventos: %w", err)
	}
	return nil
}

func stateOf(h db.Heartbeat) State {
	s := State{
		DeadlineAt: h.DeadlineAt, LastPingAt: storage.TimeOrZero(h.LastPingAt),
		LastSuccessAt: storage.TimeOrZero(h.LastSuccessAt), LastStartAt: storage.TimeOrZero(h.LastStartAt),
		FailingSince: storage.TimeOrZero(h.FailingSince), LastRuntimeMS: h.LastRuntimeMS,
		LastRunID: h.LastRunID,
	}
	if h.LastSignal != nil {
		s.LastSignal = Signal(*h.LastSignal)
	}
	return s
}

// pingParams monta a gravação do estado. A retomada por ping (ResumedByPing)
// liga a tarefa, marca resumed_at e, como a retomada manual, descarta o início
// aberto de antes da pausa; sem ela, os três ficam como estavam.
func pingParams(h db.Heartbeat, s State, sig Signal, now time.Time) db.SaveHeartbeatPingParams {
	sinal := string(s.LastSignal)
	enabled, resumedAt := h.Enabled, h.ResumedAt
	if ResumedByPing(sig, h.Enabled, h.PingResumes) {
		enabled, resumedAt = true, &now
		s.LastStartAt = time.Time{}
	}
	return db.SaveHeartbeatPingParams{
		ID: h.ID, DeadlineAt: s.DeadlineAt, LastPingAt: nilIfZero(s.LastPingAt),
		LastSuccessAt: nilIfZero(s.LastSuccessAt), LastStartAt: nilIfZero(s.LastStartAt),
		LastSignal: &sinal, FailingSince: nilIfZero(s.FailingSince), LastRuntimeMS: s.LastRuntimeMS,
		LastRunID: s.LastRunID, Enabled: enabled, ResumedAt: resumedAt,
	}
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
