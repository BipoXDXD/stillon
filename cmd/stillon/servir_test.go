package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
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
