package prober

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// prazoDeTeste substitui os prazos das rodadas: curto, para o teste não
// esperar o prazo de produção, e longe de esperaMaxima, que é o que acusa a
// rodada presa.
const prazoDeTeste = 200 * time.Millisecond

// terminaNoPrazo roda rodada numa goroutine e devolve o erro dela, ou
// reprova o teste se ela seguir presa depois de esperaMaxima.
func terminaNoPrazo(t *testing.T, oque string, rodada func() error) error {
	t.Helper()
	feito := make(chan error, 1)
	go func() { feito <- rodada() }()
	select {
	case err := <-feito:
		return err
	case <-time.After(esperaMaxima):
		t.Fatalf("%s seguiu preso depois do prazo", oque)
		return nil
	}
}

// C2 da revisão: uma consulta presa (conexão meio-aberta, tabela travada)
// prendia o Tick, e o sondador parava de despachar todos os alvos com o
// /readyz em 200. O Tick tem prazo, e o seguinte tenta de novo.
func TestTickGivesUpAtTheDeadline(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	b.alvo(t, "https://exemplo.com", nil)
	b.s.tickTimeout = prazoDeTeste
	testdb.LockTable(t, b.pool, "targets")

	err := terminaNoPrazo(t, "Tick", func() error {
		_, err := b.s.Tick(context.Background())
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Tick travado = %v; queria context.DeadlineExceeded", err)
	}
}

// A reconciliação divide o laço de Run com o Tick: presa, ela prendia o
// despacho junto.
func TestReconcileGivesUpAtTheDeadline(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	b.s.reconcileTimeout = prazoDeTeste
	testdb.LockTable(t, b.pool, "incidents")

	err := terminaNoPrazo(t, "Reconcile", func() error { return b.s.Reconcile(context.Background()) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Reconcile travado = %v; queria context.DeadlineExceeded", err)
	}
}
