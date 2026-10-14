package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// esperaMaxima é o que acusa uma rodada presa: bem acima dos prazos que os
// testes configuram, bem abaixo dos de produção.
const esperaMaxima = 10 * time.Second

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

// C2 da revisão: a reivindicação do lote não tinha prazo, e uma consulta
// presa parava a entrega de todos os avisos.
func TestDispatchClaimGivesUpAtTheDeadline(t *testing.T) {
	b := novaBancada(t, nil)
	b.enfileirar(t, incident.EventOpened)
	b.d.claimTimeout = 200 * time.Millisecond
	testdb.LockTable(t, b.pool, "notifications")

	err := terminaNoPrazo(t, "Dispatch", func() error {
		_, err := b.d.Dispatch(context.Background())
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Dispatch travado = %v; queria context.DeadlineExceeded", err)
	}
	if n := b.canal.quantos(); n != 0 {
		t.Errorf("%d envios com a reivindicação presa, queria 0", n)
	}
}
