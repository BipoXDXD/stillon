package prober

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

const (
	// evaluateEvery é de quanto em quanto o avaliador procura heartbeats. O
	// menor agendamento é de um minuto, com 30 s de tolerância.
	evaluateEvery = 5 * time.Second
	// evaluatePageSize é o teto de heartbeats de cada lado por transação; o
	// tique segue até as páginas virem menores.
	evaluatePageSize = 100
	// evaluateTickTimeout é o prazo de um tique inteiro, espera por conexão
	// do pool incluída. Um banco que trava não segura o job para sempre, e o
	// tique seguinte tenta de novo: o estado está todo no banco.
	evaluateTickTimeout = 20 * time.Second
)

// HeartbeatEvaluator abre e fecha os incidentes de heartbeat. Não guarda nada em
// memória: cada tique lê o banco, reivindica com SKIP LOCKED e grava incidente e
// outbox na mesma transação. Por isso duas instâncias de --role=prober convivem, e
// um restart não perde nada.
type HeartbeatEvaluator struct {
	pool        *pgxpool.Pool
	clk         clock.Clock
	pageSize    int32
	tickTimeout time.Duration
}

func NewHeartbeatEvaluator(pool *pgxpool.Pool, clk clock.Clock) *HeartbeatEvaluator {
	return &HeartbeatEvaluator{pool: pool, clk: clk, pageSize: evaluatePageSize, tickTimeout: evaluateTickTimeout}
}

// Run roda Tick na subida e depois a cada evaluateEvery, até ctx acabar: um
// prazo que venceu com o processo fora do ar vira incidente logo. Um tique que
// falha fica no log, e o seguinte tenta de novo.
func (e *HeartbeatEvaluator) Run(ctx context.Context) error {
	t := time.NewTicker(evaluateEvery)
	defer t.Stop()
	for {
		if _, err := e.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Error("avaliar heartbeats", "erro", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Tick aplica as transições devidas e devolve quantas aplicou.
func (e *HeartbeatEvaluator) Tick(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, e.tickTimeout)
	defer cancel()
	now := e.clk.Now()
	total := 0
	for {
		var abrir []db.ClaimHeartbeatsToOpenRow
		var fechar []db.ClaimHeartbeatsToCloseRow
		err := storage.InProberTx(ctx, e.pool, func(q *db.Queries) error {
			var err error
			abrir, err = q.ClaimHeartbeatsToOpen(ctx, db.ClaimHeartbeatsToOpenParams{
				Now: now, CooldownCut: now.Add(-heartbeat.ReopenCooldown), MaxRows: e.pageSize,
			})
			if err != nil {
				return fmt.Errorf("reivindicar heartbeats a abrir: %w", err)
			}
			fechar, err = q.ClaimHeartbeatsToClose(ctx, db.ClaimHeartbeatsToCloseParams{Now: now, MaxRows: e.pageSize})
			if err != nil {
				return fmt.Errorf("reivindicar heartbeats a fechar: %w", err)
			}
			for _, h := range abrir {
				if err := abrirIncidenteDeHeartbeat(ctx, q, h, now); err != nil {
					return err
				}
			}
			for _, h := range fechar {
				if err := fecharIncidenteDeHeartbeat(ctx, q, h, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += len(abrir) + len(fechar)
		if len(abrir) < int(e.pageSize) && len(fechar) < int(e.pageSize) {
			return total, nil
		}
	}
}

func abrirIncidenteDeHeartbeat(ctx context.Context, q *db.Queries, h db.ClaimHeartbeatsToOpenRow, now time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de incidente: %w", err)
	}
	causa, desde := heartbeat.Opening(h.DeadlineAt, h.FailingSince)
	if err := q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
		ID: id, HeartbeatID: h.ID, StartedAt: desde, Cause: causa,
	}); err != nil {
		return fmt.Errorf("abrir incidente de heartbeat: %w", err)
	}
	if err := q.MarkHeartbeatIncident(ctx, db.MarkHeartbeatIncidentParams{ID: h.ID, Now: now}); err != nil {
		return fmt.Errorf("marcar abertura no heartbeat: %w", err)
	}
	return avisarAbertura(ctx, q, h.UserID, id, now)
}

func fecharIncidenteDeHeartbeat(ctx context.Context, q *db.Queries, h db.ClaimHeartbeatsToCloseRow, now time.Time) error {
	fim, retomada := heartbeat.Closing(h.StartedAt, h.LastSuccessAt, now)
	linhas, err := q.ResolveHeartbeatIncident(ctx, db.ResolveHeartbeatIncidentParams{
		ID: h.IncidentID, ResolvedAt: fim, ClosedByResume: retomada,
	})
	if err != nil {
		return fmt.Errorf("resolver incidente de heartbeat: %w", err)
	}
	// O "voltou" só sai da transição que este tique fez. Com 0 linhas, outro
	// caminho já resolveu o incidente e o aviso, se houver, é dele.
	if linhas != 1 {
		return nil
	}
	return avisarFechamento(ctx, q, h.UserID, h.IncidentID, now)
}
