package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

const (
	// remindEvery é de quanto em quanto o job procura lembretes vencidos. O
	// menor intervalo da política é 30 min, então o atraso de até 30 s não
	// pesa.
	remindEvery = 30 * time.Second
	// reminderPageSize é o teto de pares (incidente, canal) lidos por
	// consulta; o tique pagina por chave até esgotar.
	reminderPageSize = 200
	// reminderTickTimeout é o prazo de um tique inteiro, espera por conexão
	// do pool incluída. Fica abaixo de remindEvery: um banco que trava não
	// segura o job para sempre, e o tique seguinte tenta de novo.
	reminderTickTimeout = 20 * time.Second
)

// Reminders enfileira os lembretes de incidente aberto e sem ack
// (docs/superpowers/specs/2026-10-14-lembretes-design.md). Roda no papel do
// sondador, ao lado do Dispatcher, que os entrega como qualquer outra linha
// da outbox.
type Reminders struct {
	pool        *pgxpool.Pool
	clk         clock.Clock
	policy      Policy
	pageSize    int32
	tickTimeout time.Duration
}

func NewReminders(pool *pgxpool.Pool, clk clock.Clock, policy Policy) *Reminders {
	return &Reminders{pool: pool, clk: clk, policy: policy, pageSize: reminderPageSize, tickTimeout: reminderTickTimeout}
}

// Run roda Tick na subida e depois a cada remindEvery, até ctx acabar: um
// lembrete que venceu com o processo fora do ar sai logo. Um tique que falha
// fica no log, e o seguinte tenta de novo: o estado está todo no banco.
func (r *Reminders) Run(ctx context.Context) error {
	t := time.NewTicker(remindEvery)
	defer t.Stop()
	for {
		if _, err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Error("enfileirar lembretes", "erro", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Tick enfileira um lembrete para cada par (incidente, canal) cujo próximo
// lembrete já venceu, e devolve quantos enfileirou. Cada página roda numa
// transação própria; um par que outro sondador acabou de lembrar colide no
// UNIQUE e não conta. O tique inteiro tem o prazo reminderTickTimeout.
func (r *Reminders) Tick(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, r.tickTimeout)
	defer cancel()
	now := r.clk.Now()
	total := 0
	var after db.ListReminderCandidatesParams
	for {
		after.MaxRows = r.pageSize
		var pagina []db.ListReminderCandidatesRow
		var n int
		err := storage.InProberTx(ctx, r.pool, func(q *db.Queries) error {
			var err error
			pagina, err = q.ListReminderCandidates(ctx, after)
			if err != nil {
				return fmt.Errorf("listar candidatos a lembrete: %w", err)
			}
			n, err = r.enqueueDue(ctx, q, pagina, now)
			return err
		})
		if err != nil {
			return total, err
		}
		total += n
		if len(pagina) < int(r.pageSize) {
			return total, nil
		}
		ultimo := pagina[len(pagina)-1]
		after.AfterIncident, after.AfterChannel = ultimo.IncidentID, ultimo.ChannelID
	}
}

// enqueueDue enfileira, dentro da transação da página, o lembrete de cada
// par vencido.
func (r *Reminders) enqueueDue(ctx context.Context, q *db.Queries, pagina []db.ListReminderCandidatesRow, now time.Time) (int, error) {
	enfileirados := 0
	for _, c := range pagina {
		vence, err := r.policy.NextReminder(Kind(c.Kind), c.StartedAt, c.BaseAt)
		if err != nil {
			return enfileirados, fmt.Errorf("incidente %s, canal %s: %w", c.IncidentID, c.ChannelID, err)
		}
		if now.Before(vence) {
			continue
		}
		id, err := uuid.NewV7()
		if err != nil {
			return enfileirados, fmt.Errorf("gerar id de lembrete: %w", err)
		}
		n, err := q.EnqueueReminder(ctx, db.EnqueueReminderParams{
			ID: id, IncidentID: c.IncidentID, ChannelID: c.ChannelID, Seq: c.LastSeq + 1, Now: now,
		})
		if err != nil {
			return enfileirados, fmt.Errorf("enfileirar lembrete: %w", err)
		}
		enfileirados += int(n)
	}
	return enfileirados, nil
}
