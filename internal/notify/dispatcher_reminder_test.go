package notify

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

func (b *bancada) lembrar(t *testing.T, seq int32) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.EnqueueReminder(ctx, db.EnqueueReminderParams{
			ID: id, IncidentID: b.incidente, ChannelID: b.canalID, Seq: seq, Now: b.clk.Now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) reconhecer(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.AckIncident(ctx, db.AckIncidentParams{UserID: b.dono, ID: b.incidente, Now: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (b *bancada) resolver(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: b.incidente, ResolvedAt: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReminderIsSentWithItsKindAndQueueTime(t *testing.T) {
	b := novaBancada(t, nil)
	enfileirado := b.clk.Now()
	id := b.lembrar(t, 1)
	b.clk.Advance(time.Minute)

	b.despachar(t, 1)
	if b.canal.quantos() != 1 {
		t.Fatalf("%d envios, queria 1", b.canal.quantos())
	}
	if ev := b.canal.envios[0]; ev.NotificationID != id || ev.Kind != incident.EventReminder || !ev.QueuedAt.Equal(enfileirado) {
		t.Errorf("evento = %+v; queria o lembrete, enfileirado em %s", ev, enfileirado)
	}
	if l := b.estado(t, id); l.Status != "sent" {
		t.Errorf("linha = %+v, queria sent", l)
	}
}

// Reconhecer interrompe os lembretes, inclusive o que já estava na fila.
func TestPendingReminderIsCancelledOnceAcknowledged(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.lembrar(t, 1)
	b.reconhecer(t)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "cancelled" {
		t.Errorf("lembrete depois do ack: %+v, queria cancelled", l)
	}
	if b.canal.quantos() != 0 {
		t.Errorf("enviou %d, queria 0", b.canal.quantos())
	}
}

func TestPendingReminderIsCancelledOnceResolved(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.lembrar(t, 1)
	b.resolver(t)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "cancelled" {
		t.Errorf("lembrete depois do fechamento: %+v, queria cancelled", l)
	}
	if b.canal.quantos() != 0 {
		t.Errorf("enviou %d, queria 0", b.canal.quantos())
	}
}

// Um lembrete já na fila quando o alvo é pausado não sai: o alvo pausado
// não gera lembrete, e o incidente segue aberto.
func TestPendingReminderIsCancelledOnceTargetIsPaused(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.lembrar(t, 1)
	pausarAlvoDoIncidente(t, b.pool, b.dono, b.incidente, true)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "cancelled" {
		t.Errorf("lembrete de alvo pausado: %+v, queria cancelled", l)
	}
	if b.canal.quantos() != 0 {
		t.Errorf("enviou %d, queria 0", b.canal.quantos())
	}
}

// O ack para só os lembretes: abertura e fechamento saem do mesmo jeito.
func TestAcknowledgeDoesNotStopOpenedNorResolved(t *testing.T) {
	b := novaBancada(t, nil)
	aberta := b.enfileirar(t, incident.EventOpened)
	b.reconhecer(t)
	b.despachar(t, 1)

	b.resolver(t)
	fechada := b.enfileirar(t, incident.EventResolved)
	b.despachar(t, 1)

	for _, id := range []uuid.UUID{aberta, fechada} {
		if l := b.estado(t, id); l.Status != "sent" {
			t.Errorf("notificação %s depois do ack: %+v, queria sent", id, l)
		}
	}
}

// renomearAlvo troca o nome do alvo do incidente, como o dono faria no painel.
func (b *bancada) renomearAlvo(t *testing.T, nome string) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE targets SET name = $1 WHERE id = (SELECT target_id FROM incidents WHERE id = $2)`, nome, b.incidente)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("renomear alvo: %v, %d linhas", err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// A retentativa repete o corpo da primeira tentativa, porque o Resend recusa
// a mesma Idempotency-Key com outro corpo (409 invalid_idempotent_request, que
// é falha definitiva). O nome do alvo é o do enfileiramento, e renomear o
// alvo entre as tentativas não muda o texto.
func TestRetryKeepsTheTargetNameOfTheQueueTime(t *testing.T) {
	for _, evento := range []string{incident.EventOpened, incident.EventReminder} {
		t.Run(evento, func(t *testing.T) {
			b := novaBancada(t, nil)
			b.canal.respostas = []error{&SendError{Reason: ReasonServerError}}
			if evento == incident.EventReminder {
				b.lembrar(t, 1)
			} else {
				b.enfileirar(t, evento)
			}

			b.despachar(t, 1)
			b.renomearAlvo(t, "API renomeada")
			b.clk.Advance(firstBackoff)
			b.despachar(t, 1)

			if b.canal.quantos() != 2 {
				t.Fatalf("%d envios, queria 2", b.canal.quantos())
			}
			for i, ev := range b.canal.envios {
				if ev.TargetName != "API" {
					t.Errorf("envio %d com o alvo %q; queria %q, o nome no enfileiramento", i+1, ev.TargetName, "API")
				}
			}
		})
	}
}

// Uma linha enfileirada antes da migration 00012 não tem o nome gravado e
// sai com o nome atual do alvo.
func TestRowWithoutTargetNameUsesTheCurrentName(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE notifications SET target_name = NULL WHERE id = $1`, id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("limpar target_name: %v, %d linhas", err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	b.renomearAlvo(t, "API renomeada")

	b.despachar(t, 1)
	if b.canal.quantos() != 1 || b.canal.envios[0].TargetName != "API renomeada" {
		t.Errorf("envios = %+v; queria um, com o alvo %q", b.canal.envios, "API renomeada")
	}
}
