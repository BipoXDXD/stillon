package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// A marca d'água do desafio de 2FA (Plano 8): o primeiro desafio consome, o
// mesmo não consome de novo, um mais antigo nunca, e um mais novo sim.
func TestConsumeChallengeAdvancesTheMarkOnlyForward(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	passos := []struct {
		nome   string
		em     time.Time
		queria bool
	}{
		{"primeiro desafio, marca vazia", t0, true},
		{"o mesmo desafio de novo", t0, false},
		{"um microssegundo mais antigo", t0.Add(-time.Microsecond), false},
		{"um microssegundo mais novo", t0.Add(time.Microsecond), true},
	}
	for _, p := range passos {
		got, err := store.ConsumeChallenge(ctx, userID, p.em)
		if err != nil {
			t.Fatal(err)
		}
		if got != p.queria {
			t.Errorf("%s: consumido = %v, queria %v", p.nome, got, p.queria)
		}
	}
}

// Vinte /totp/verify simultâneos com o MESMO desafio: um consome, os outros
// não. É a propriedade que o UPDATE condicional garante sem trava explícita.
func TestConsumeChallengeIsAtomicUnderConcurrency(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	em := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	const n = 20
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		consumos  int
		largada   = make(chan struct{})
		primeiroE error
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-largada
			ok, err := store.ConsumeChallenge(context.Background(), userID, em)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && primeiroE == nil {
				primeiroE = err
			}
			if ok {
				consumos++
			}
		}()
	}
	close(largada)
	wg.Wait()

	if primeiroE != nil {
		t.Fatal(primeiroE)
	}
	if consumos != 1 {
		t.Errorf("%d consumos do mesmo desafio, queria 1", consumos)
	}
}
