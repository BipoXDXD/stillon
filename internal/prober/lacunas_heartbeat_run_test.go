package prober_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// prazoParada é o limite, no relógio da bolha do synctest, para o Run sair
// depois do cancelamento. Só o relógio falso o consome: nenhum prazo de parede.
const prazoParada = time.Minute

// Run tica na subida: um prazo que venceu com o processo fora do ar vira
// incidente sem esperar o primeiro intervalo, e o cancelamento encerra o laço
// sem erro.
//
// O Run roda numa bolha do synctest: o relógio da bolha só avança quando todas
// as goroutines dela estão paradas, então synctest.Wait devolve o controle com
// o Run já no select, depois do tique da subida e antes de qualquer tique
// periódico. Sem o tique da subida, o incidente não existe nesse ponto, e o
// teste falha sem depender de tempo de parede. O banco e o relógio do domínio
// ficam fora da bolha.
func TestHeartbeatRunAbreIncidenteNaSubidaEParaNoCancel(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.clk.Advance(prazoHB)

	synctest.Test(t, func(t *testing.T) {
		inicio := time.Now()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		terminou := make(chan error, 1)
		go func() { terminou <- b.av.Run(ctx) }()

		synctest.Wait() // o Run fez o tique da subida e parou no select
		if d := time.Since(inicio); d != 0 {
			t.Fatalf("passaram %v no relógio da bolha antes do primeiro tique periódico", d)
		}
		l := b.incidentes(t, id)
		if len(l) != 1 || l[0].Causa != heartbeat.CauseLate || !l[0].Inicio.Equal(inicioHB.Add(prazoHB)) || l[0].Fim != nil {
			t.Fatalf("incidentes depois do tique da subida: %+v", l)
		}

		cancel()
		// Sem synctest.Wait aqui: um Run que não pára manteria a bolha ativa e
		// o Wait nunca voltaria. O relógio da bolha avança entre os tiques e
		// faz o prazo abaixo estourar com uma mensagem legível.
		select {
		case err := <-terminou:
			if err != nil {
				t.Errorf("Run devolveu %v, queria nil", err)
			}
		case <-time.After(prazoParada):
			t.Fatalf("Run não parou %v (relógio da bolha) depois do cancelamento", prazoParada)
		}
	})
}
