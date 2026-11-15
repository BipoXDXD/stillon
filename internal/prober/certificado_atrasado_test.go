package prober

import (
	"testing"
	"time"
)

// Um check atrasado (duas sondas do mesmo alvo em voo, a mais nova grava
// primeiro) é ignorado pelo detector e também não mexe na validade guardada:
// sem a guarda, o certificado antigo lido antes da renovação voltaria por
// cima do renovado, com o observed_at velho, e só sairia depois da janela de
// troca.
func TestLateCheckDoesNotOverwriteTheCertificate(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	a := b.linhaDoAlvo(alvo)
	t1 := b.clk.Now()

	b.s.gravar(a, t1, comCertificado(validadeB))
	b.s.gravar(a, t1.Add(-30*time.Second), comCertificado(validadeA))

	if l := b.exigirLinhaTLS(t, alvo); !l.NotAfter.Equal(validadeB) || !l.ObservedAt.Equal(t1) {
		t.Errorf("target_tls = %v visto em %v; queria %v visto em %v", l.NotAfter, l.ObservedAt, validadeB, t1)
	}
}
