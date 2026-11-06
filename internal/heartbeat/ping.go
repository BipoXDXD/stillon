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

// Ping aplica sig ao heartbeat do token.
func (s *PingService) Ping(ctx context.Context, token string, sig Signal) error {
	return s.aplicar(ctx, token, sig, nil)
}

// PingExit aplica o sinal que o código de saída representa, e o log guarda o
// número.
func (s *PingService) PingExit(ctx context.Context, token string, code ExitCode) error {
	valor := code.Value()
	return s.aplicar(ctx, token, code.Signal(), &valor)
}

// aplicar trava a linha, e isso serializa pings simultâneos: o segundo lê o que
// o primeiro gravou, e a repetição tem um efeito só, entre instâncias também
// (D9).
func (s *PingService) aplicar(ctx context.Context, token string, sig Signal, code *int16) error {
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
		return s.registrar(ctx, q, h, sig, code)
	})
}

func (s *PingService) registrar(ctx context.Context, q *db.Queries, h db.Heartbeat, sig Signal, code *int16) error {
	sch, err := ScheduleFromRow(h.PeriodSec, h.CronExpr, h.Timezone)
	if err != nil {
		return fmt.Errorf("agendamento do heartbeat %s: %w", h.ID, err)
	}
	grace := time.Duration(h.GraceSec) * time.Second
	estado, ev := stateOf(h).Apply(sig, s.clk.Now(), sch, grace)
	if ev == nil {
		return nil
	}
	if err := q.SaveHeartbeatPing(ctx, pingParams(h.ID, estado)); err != nil {
		return fmt.Errorf("gravar ping: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de evento: %w", err)
	}
	if err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
		ID: id, HeartbeatID: h.ID, At: ev.At, Kind: string(ev.Kind), RuntimeMS: ev.RuntimeMS, ExitCode: code,
	}); err != nil {
		return fmt.Errorf("gravar evento: %w", err)
	}
	err = q.TrimHeartbeatEvents(ctx, db.TrimHeartbeatEventsParams{HeartbeatID: h.ID, MaxEvents: MaxEventsPerHeartbeat})
	if err != nil {
		return fmt.Errorf("aparar eventos: %w", err)
	}
	return nil
}

func stateOf(h db.Heartbeat) State {
	s := State{
		DeadlineAt: h.DeadlineAt, LastPingAt: valueOrZero(h.LastPingAt),
		LastSuccessAt: valueOrZero(h.LastSuccessAt), LastStartAt: valueOrZero(h.LastStartAt),
		FailingSince: valueOrZero(h.FailingSince),
	}
	if h.LastSignal != nil {
		s.LastSignal = Signal(*h.LastSignal)
	}
	return s
}

func pingParams(id uuid.UUID, s State) db.SaveHeartbeatPingParams {
	sinal := string(s.LastSignal)
	return db.SaveHeartbeatPingParams{
		ID: id, DeadlineAt: s.DeadlineAt, LastPingAt: nilIfZero(s.LastPingAt),
		LastSuccessAt: nilIfZero(s.LastSuccessAt), LastStartAt: nilIfZero(s.LastStartAt),
		LastSignal: &sinal, FailingSince: nilIfZero(s.FailingSince),
	}
}

func valueOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
