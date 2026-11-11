package prober_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// Run tica na subida: um prazo que venceu com o processo fora do ar vira
// incidente sem esperar o primeiro intervalo, e o cancelamento encerra o laço
// sem erro. O relógio é o falso; a espera abaixo é só pela goroutine.
func TestHeartbeatRunAbreIncidenteNaSubidaEParaNoCancel(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.clk.Advance(prazoHB)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	terminou := make(chan error, 1)
	go func() { terminou <- b.av.Run(ctx) }()

	limite := time.After(4 * time.Second)
	passo := time.NewTicker(10 * time.Millisecond)
	defer passo.Stop()
	for len(b.incidentes(t, id)) == 0 {
		select {
		case <-limite:
			t.Fatal("o tique da subida não abriu o incidente em 4 s")
		case <-passo.C:
		}
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Causa != heartbeat.CauseLate || !l[0].Inicio.Equal(inicioHB.Add(prazoHB)) || l[0].Fim != nil {
		t.Fatalf("incidentes: %+v", l)
	}

	cancel()
	select {
	case err := <-terminou:
		if err != nil {
			t.Errorf("Run devolveu %v, queria nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run não parou em 1 s depois do cancelamento")
	}
}
