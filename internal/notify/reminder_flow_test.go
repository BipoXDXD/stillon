package notify

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// O caminho inteiro com o relógio falso: a abertura sai, o lembrete vence,
// o job o enfileira, o dispatcher o entrega, e depois do ack não sai mais
// nenhum.
func TestReminderFlowFromOpenedToAcknowledge(t *testing.T) {
	b := novaBancada(t, nil)
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: b.incidente, Now: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	aberta := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	r := NewReminders(b.pool, b.clk, DefaultPolicy())
	ticar := func(quer int) {
		t.Helper()
		if n, err := r.Tick(ctx); err != nil || n != quer {
			t.Fatalf("Tick = %d, %v; queria %d", n, err, quer)
		}
	}

	b.clk.Advance(30 * time.Minute)
	ticar(1)
	b.despachar(t, 1)
	if b.canal.quantos() != 2 || b.canal.envios[1].Kind != incident.EventReminder {
		t.Fatalf("envios = %+v; queria a abertura e o lembrete", b.canal.envios)
	}
	if l := b.estado(t, aberta); l.Status != "sent" {
		t.Errorf("abertura: %+v, queria sent", l)
	}

	b.reconhecer(t)
	b.clk.Advance(30 * time.Minute)
	ticar(0)
	b.despachar(t, 0)
	if b.canal.quantos() != 2 {
		t.Errorf("%d envios depois do ack, queria 2", b.canal.quantos())
	}
}
