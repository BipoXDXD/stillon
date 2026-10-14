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

// C1 da revisão: no encerramento, o lote em curso não tinha teto. Um webhook
// lento segurava o processo além do ShutdownGrace, o Docker o matava, e o
// aviso entregue mas não marcado voltava pelo lease e saía duplicado. Agora,
// depois do sinal, nenhum envio novo começa, o envio em curso é abortado em
// shutdownSendGrace, e os dois voltam à fila para já, sem gastar tentativa.
func TestShutdownBoundsTheBatchAndReturnsItToTheQueue(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 2)
	b.canal.bloquear = true
	b.canal.chegou = make(chan struct{}, len(ids))
	b.d.sendTimeout = time.Minute // sem o teto do encerramento, o lote esperaria isto
	b.d.shutdownSendGrace = 200 * time.Millisecond

	ctx, encerrar := context.WithCancel(context.Background())
	defer encerrar()
	feito := make(chan error, 1)
	go func() {
		_, err := b.d.Dispatch(ctx)
		feito <- err
	}()
	select {
	case <-b.canal.chegou:
	case <-time.After(esperaMaxima):
		t.Fatal("o primeiro envio não começou")
	}
	encerrar()

	if err := terminaNoPrazo(t, "Dispatch no encerramento", func() error { return <-feito }); err != nil {
		t.Fatalf("Dispatch = %v", err)
	}
	if n := b.canal.quantos(); n != 1 {
		t.Errorf("%d envios, queria 1: nenhum envio novo começa depois do sinal", n)
	}
	for i, id := range ids {
		l := b.estado(t, id)
		if l.Status != "pending" || l.Attempts != 0 || !l.Proxima.Equal(b.clk.Now()) || l.Erro != nil {
			t.Errorf("notificação %d: %+v; queria pendente para agora, sem tentativa gasta e sem erro", i, l)
		}
	}
}
