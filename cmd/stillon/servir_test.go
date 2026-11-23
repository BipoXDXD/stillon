package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/BipoXDXD/stillon/internal/contaemail"
)

// Um pedido preso além do grace não transforma o SIGTERM em falha: o
// servidor corta as conexões que sobraram e o grupo termina sem erro.
func TestShutdownPastTheGraceClosesWithoutError(t *testing.T) {
	addr := portaLivre(t)
	preso := make(chan struct{})
	solto := make(chan struct{})
	t.Cleanup(func() { close(solto) })
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(preso)
		<-solto
	})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var g errgroup.Group
	servir(ctx, &g, srv, "teste", 50*time.Millisecond)

	pedido := make(chan error, 1)
	cliente := &http.Client{Timeout: 5 * time.Second}
	go func() {
		prazo := time.Now().Add(5 * time.Second)
		for {
			resp, err := cliente.Get("http://" + addr)
			if err == nil {
				resp.Body.Close()
			}
			// Antes de o servidor escutar, a conexão é recusada: tenta de novo.
			if err == nil || time.Now().After(prazo) || pedidoChegou(preso) {
				pedido <- err
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-preso:
	case <-time.After(5 * time.Second):
		t.Fatal("o pedido não chegou ao handler")
	}

	cancel()
	if err := g.Wait(); err != nil {
		t.Errorf("encerrar com um pedido preso = %v; queria nil", err)
	}
	select {
	case err := <-pedido:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("o pedido preso terminou com %v; queria a conexão cortada pelo servidor", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a conexão do pedido preso continuou aberta depois do encerramento")
	}
}

func pedidoChegou(preso <-chan struct{}) bool {
	select {
	case <-preso:
		return true
	default:
		return false
	}
}

// filaFalsa registra quando o contexto de Rodar acaba.
type filaFalsa struct{ cancelada chan struct{} }

func (f *filaFalsa) Rodar(ctx context.Context) {
	<-ctx.Done()
	close(f.cancelada)
}

func filaCancelada(f *filaFalsa, d time.Duration) bool {
	select {
	case <-f.cancelada:
		return true
	case <-time.After(d):
		return false
	}
}

// A fila dos e-mails de conta só começa a encerrar depois que a API parou
// de aceitar pedidos: um forgot respondido no fim ainda tem o e-mail drenado.
func TestFilaDeContaEncerraDepoisDaAPI(t *testing.T) {
	f := &filaFalsa{cancelada: make(chan struct{})}
	apiParou := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	var g errgroup.Group
	rodarFilaDeConta(ctx, &g, f, apiParou, contaemail.PrazoDreno+time.Hour)

	cancel()
	if filaCancelada(f, 200*time.Millisecond) {
		t.Fatal("a fila encerrou antes de a API parar")
	}
	close(apiParou)
	if !filaCancelada(f, 5*time.Second) {
		t.Fatal("a fila não encerrou depois de a API parar")
	}
	if err := g.Wait(); err != nil {
		t.Errorf("g.Wait = %v, queria nil", err)
	}
}

// Com a API presa no encerramento, a fila encerra assim mesmo a tempo de o
// dreno (PrazoDreno) caber no ShutdownGrace contado do sinal.
func TestFilaDeContaCabeNoShutdownGrace(t *testing.T) {
	f := &filaFalsa{cancelada: make(chan struct{})}
	const folga = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	var g errgroup.Group
	rodarFilaDeConta(ctx, &g, f, make(chan struct{}), contaemail.PrazoDreno+folga)

	inicio := time.Now()
	cancel()
	if !filaCancelada(f, 5*time.Second) {
		t.Fatal("a fila esperou a API presa além do ShutdownGrace")
	}
	if d := time.Since(inicio); d < folga {
		t.Errorf("a fila encerrou %v depois do sinal, antes da folga de %v para a API", d, folga)
	}
	if err := g.Wait(); err != nil {
		t.Errorf("g.Wait = %v, queria nil", err)
	}
}

// servir avisa quando o servidor terminou de encerrar: é o sinal que a fila
// dos e-mails de conta espera.
func TestServirAvisaQuandoEncerrou(t *testing.T) {
	srv := &http.Server{Addr: portaLivre(t), Handler: http.NotFoundHandler()}
	ctx, cancel := context.WithCancel(context.Background())
	var g errgroup.Group
	encerrou := servir(ctx, &g, srv, "teste", time.Second)
	select {
	case <-encerrou:
		t.Fatal("servir avisou o encerramento antes do cancelamento")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-encerrou:
	case <-time.After(5 * time.Second):
		t.Fatal("servir não avisou o encerramento")
	}
	if err := g.Wait(); err != nil {
		t.Errorf("g.Wait = %v, queria nil", err)
	}
}
