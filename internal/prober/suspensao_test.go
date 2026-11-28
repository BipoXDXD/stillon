package prober

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A suspensão não apaga nem muda nada: tirar a marca devolve o projeto aos
// workers como estava. O alvo que o usuário pausou continua pausado, e a
// notificação que esperava sai.
func TestUnsuspendRestoresStateAndKeepsPauses(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	ctx := context.Background()
	a1 := b.alvo(t, srv.URL, nil)
	a2 := b.alvo(t, srv.URL, func(p *db.InsertTargetParams) { p.Enabled = false })
	pendente := uuid.Must(uuid.NewV7())
	canal := b.canal(t, true, true)
	incidente := b.incidenteAberto(t, a1)
	if err := b.comoQueries(t, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: pendente, IncidentID: incidente, ChannelID: canal, Event: "opened", Now: b.clk.Now(),
		})
	}); err != nil {
		t.Fatal(err)
	}

	testdb.Suspender(t, b.dono)
	if n, err := b.s.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("Tick com o titular suspenso = %d, %v; queria 0 disparos", n, err)
	}

	if _, err := testdb.Admin(t).Exec(ctx, `UPDATE users SET suspended_at = NULL WHERE id = $1`, b.dono); err != nil {
		t.Fatal(err)
	}
	b.clk.Advance(time.Second)
	if n, err := b.s.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("Tick depois de tirar a suspensão = %d, %v; queria 1 disparo (o alvo ativo)", n, err)
	}
	b.s.Stop(esperaMaxima)

	if rows := b.checks(t, a1); len(rows) != 1 {
		t.Errorf("o alvo ativo tem %d checks; queria 1", len(rows))
	}
	var habilitado bool
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT enabled FROM targets WHERE id = $1`, a2).Scan(&habilitado); err != nil {
		t.Fatal(err)
	}
	if habilitado {
		t.Error("o alvo pausado pelo usuário voltou ligado")
	}

	var lote []db.ClaimDueNotificationsRow
	if err := b.comoQueries(t, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: b.clk.Now(), LeaseUntil: b.clk.Now().Add(time.Minute), MaxRows: 10,
			Kinds: []string{"discord", "email"}, Events: []string{"opened", "resolved", "reminder"},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, n := range lote {
		if n.ID == pendente {
			return
		}
	}
	t.Errorf("a notificação pendente não saiu depois de tirar a suspensão: lote %+v", lote)
}
