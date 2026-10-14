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
