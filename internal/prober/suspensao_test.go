package prober

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
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

// A corrida da suspensão com o aviso: a suspensão comita depois que a
// abertura foi marcada como avisada e antes do fan-out, na mesma transação.
// O fan-out não olha a suspensão, então a notificação é gravada e espera; ao
// reativar, ela sai. Com o filtro no fan-out, ela sumiria para sempre: a
// marca já impede outro fan-out, e sem notificação não há lembrete.
func TestSuspensionBetweenClaimAndFanOutKeepsOpenedNotice(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	canal := b.canal(t, true, true)
	incidente := b.incidenteAberto(t, b.alvo(t, "https://exemplo.com", nil))
	suspenderNoMeio := func(ctx context.Context, q *db.Queries, id uuid.UUID, evento string, em time.Time) error {
		testdb.Suspender(t, b.dono)
		return enfileirar(ctx, q, id, evento, em)
	}
	if err := b.comoQueries(t, func(q *db.Queries) error {
		return avisarAbertura(ctx, q, suspenderNoMeio, incidente, b.clk.Now())
	}); err != nil {
		t.Fatal(err)
	}
	// Lida por fora da RLS: o escopo do dono suspenso não acha o projeto.
	var gravadas int
	if err := testdb.Admin(t).QueryRow(ctx, `
		SELECT count(*) FROM notifications
		 WHERE incident_id = $1 AND channel_id = $2 AND event = $3 AND status = 'pending'`,
		incidente, canal, incident.EventOpened).Scan(&gravadas); err != nil {
		t.Fatal(err)
	}
	if gravadas != 1 {
		t.Fatalf("%d notificações de abertura no canal %s; queria 1, gravada apesar da suspensão", gravadas, canal)
	}

	reivindicar := func() []db.ClaimDueNotificationsRow {
		t.Helper()
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
		return lote
	}
	if lote := reivindicar(); len(lote) != 0 {
		t.Fatalf("ClaimDueNotifications com o titular suspenso trouxe %+v; queria nada", lote)
	}

	testdb.Reativar(t, b.dono)
	if lote := reivindicar(); len(lote) != 1 || lote[0].Event != incident.EventOpened || lote[0].IncidentID != incidente {
		t.Errorf("ClaimDueNotifications depois de reativar trouxe %+v; queria a abertura do incidente %s", lote, incidente)
	}
}
