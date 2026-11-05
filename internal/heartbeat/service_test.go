package heartbeat_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Dois POST no último lugar se encontram entre contar e inserir: sem a trava,
// os dois veem 99 e os dois inserem. O gancho é o que garante o encontro; sem
// ele, as transações quase nunca se sobrepõem, e o teste passaria sem provar.
func TestHeartbeatLimitHoldsUnderConcurrency(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		for range heartbeat.MaxPerUser - 1 {
			_, hash := heartbeat.NewPingToken()
			if err := q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
				ID: uuid.Must(uuid.NewV7()), UserID: a.dono, Name: "h", PingTokenHash: hash, PeriodSec: &periodo,
				GraceSec: 300, Enabled: true, CreatedAt: agora, DeadlineAt: agora.Add(time.Hour),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := heartbeat.NewService(a.pool, a.clk)
	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Go(func() {
			_, _, erros[i] = svc.Create(ctx, a.dono, heartbeat.Input{Name: "h", Schedule: hora, Grace: tolerancia, Enabled: true})
		})
	}
	wg.Wait()

	var n int64
	err = storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		var err error
		n, err = q.CountHeartbeats(ctx, a.dono)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != heartbeat.MaxPerUser {
		t.Errorf("%d heartbeats depois de dois POST simultâneos (erros: %v), queria %d", n, erros, heartbeat.MaxPerUser)
	}
}
