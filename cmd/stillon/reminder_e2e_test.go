package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O papel prober liga o job de lembretes. O incidente já está aberto há uma
// hora, com a abertura entregue há 31 min a um canal Discord: o lembrete
// venceu, e o job o enfileira no primeiro tique, que roda na subida. A cadência
// e as regras ficam nos testes do pacote notify; aqui só a fiação.
func TestProberRoleEnqueuesOverdueReminders(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'lembrete@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}

	alvo, canal, incidente := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agora := time.Now()
	avisada := agora.Add(-31 * time.Minute)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		// Desligado: o sondador não mexe no alvo durante o teste.
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "fora", URL: "https://exemplo.com/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: false, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: canal, UserID: dono, Kind: "discord", ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: incidente, TargetID: alvo, StartedAt: agora.Add(-time.Hour), Cause: "timeout"}); err != nil {
			return err
		}
		if _, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: incidente, Now: avisada}); err != nil {
			return err
		}
		aberta := uuid.Must(uuid.NewV7())
		if err := q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: aberta, IncidentID: incidente, ChannelID: canal, Event: incident.EventOpened, Now: avisada,
		}); err != nil {
			return err
		}
		return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: aberta, Now: avisada})
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t), MasterKey: chave, ShutdownGrace: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	prazo := time.Now().Add(15 * time.Second)
	for {
		var lembretes int
		escopoDoDono(t, pool, dono, `SELECT count(*) FROM notifications WHERE channel_id = $1 AND event = 'reminder'`, []any{canal}, &lembretes)
		if lembretes == 1 {
			break
		}
		if time.Now().After(prazo) {
			t.Fatalf("%d lembretes depois de 15s, queria 1", lembretes)
		}
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes do lembrete: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-fim:
		if err != nil {
			t.Errorf("run encerrou com erro: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run não encerrou depois do cancelamento")
	}
}
