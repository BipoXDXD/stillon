package notify

import (
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// sorteioFixo devolve sempre o mesmo valor em [0, 1). Com 0 o backoff sai no
// piso do espalhamento (−20%); com 0.5, no valor exato.
func sorteioFixo(v float64) func() float64 { return func() float64 { return v } }

// Avisos que falharam juntos (o mesmo webhook fora do ar num apagão) não
// podem voltar todos no mesmo instante: a espera é espalhada em ±20%.
func TestRetryBackoffIsSpread(t *testing.T) {
	for _, tc := range []struct {
		nome    string
		sorteio float64
		espera  time.Duration
	}{
		{"piso", 0, 24 * time.Second},
		{"meio", 0.5, 30 * time.Second},
		{"perto do teto", 0.75, 33 * time.Second},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			b.d.sortear = sorteioFixo(tc.sorteio)
			b.canal.respostas = []error{&SendError{Reason: ReasonServerError}}
			id := b.enfileirar(t, incident.EventOpened)

			b.despachar(t, 1)
			if l := b.estado(t, id); l.Status != "pending" || !l.Proxima.Equal(b.clk.Now().Add(tc.espera)) {
				t.Errorf("depois da falha: %+v; queria pendente, próxima em %v", l, tc.espera)
			}
		})
	}
}

// O Retry-After do provedor é o mínimo pedido: o espalhamento nunca o encurta.
func TestSpreadNeverShortensRetryAfter(t *testing.T) {
	b := novaBancada(t, nil)
	b.d.sortear = sorteioFixo(0)
	b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited, RetryAfter: 30 * time.Second}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); !l.Proxima.Equal(b.clk.Now().Add(30 * time.Second)) {
		t.Errorf("próxima = %v, queria em 30s (Retry-After, sem encurtar)", l.Proxima)
	}
}

// A adiada atrás de uma falha definitiva calcula o próprio backoff, e ele
// também é espalhado.
func TestDeferredOwnBackoffIsSpread(t *testing.T) {
	b := novaBancada(t, nil)
	b.d.sortear = sorteioFixo(0)
	ids := b.apagao(t, 2)
	b.comoSondador(t, `UPDATE notifications SET attempts = $1 WHERE id = $2`, int32(MaxAttempts-1), ids[0])
	b.canal.respostas = []error{&SendError{Reason: ReasonNetwork}}

	b.despachar(t, 2)
	if l := b.estado(t, ids[1]); l.Status != "pending" || !l.Proxima.Equal(b.clk.Now().Add(24*time.Second)) {
		t.Errorf("adiada: %+v; queria pendente, próxima em 24s", l)
	}
}
