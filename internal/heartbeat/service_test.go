package heartbeat_test

import (
	"context"
	"errors"
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

// Duplicar conta no mesmo limite e pela mesma trava: duas cópias no último
// lugar se encontram entre contar e inserir, e só uma passa.
func TestCloneLimitHoldsUnderConcurrency(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	original, _ := a.novoHeartbeat(t)
	periodo := int32(3600)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		for range heartbeat.MaxPerUser - 2 {
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
			_, _, erros[i] = svc.Clone(ctx, a.dono, original)
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
	if n != 100 {
		t.Errorf("%d heartbeats depois de duas cópias simultâneas (erros: %v), queria 100", n, erros)
	}
	ok, noLimite := 0, 0
	for _, err := range erros {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, heartbeat.ErrLimitReached):
			noLimite++
		}
	}
	if ok != 1 || noLimite != 1 {
		t.Errorf("erros %v, queria uma cópia e um ErrLimitReached", erros)
	}
}

// Criar devolve o token uma vez: se a resposta não puder ser montada, a linha
// não pode ficar gravada com um token que ninguém recebeu.
func TestCreateLeavesNothingWhenTheResponseFails(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	svc.SetDepoisDeGravar(cancelar)

	_, token, err := svc.Create(ctx, a.dono, heartbeat.Input{Name: "h", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err == nil || token != "" {
		t.Fatalf("Create = %q, %v; queria erro sem token", token, err)
	}
	var n int64
	fundo := context.Background()
	err = storage.InUserTx(fundo, a.pool, a.dono, func(q *db.Queries) error {
		var err error
		n, err = q.CountHeartbeats(fundo, a.dono)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d heartbeats gravados sem que o token saísse", n)
	}
}

// Rotacionar revoga o token atual: se a resposta falhar, o antigo segue valendo,
// ou a tarefa ficaria sem token nenhum.
func TestRotateKeepsTheOldTokenWhenTheResponseFails(t *testing.T) {
	a := novoAmbiente(t)
	id, antigo := a.novoHeartbeat(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	svc.SetDepoisDeGravar(cancelar)

	_, novo, err := svc.RotateToken(ctx, a.dono, id)
	if err == nil || novo != "" {
		t.Fatalf("RotateToken = %q, %v; queria erro sem token", novo, err)
	}
	a.pingar(t, antigo, heartbeat.SignalSuccess)
}
