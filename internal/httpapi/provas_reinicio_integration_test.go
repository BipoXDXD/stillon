package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

// reiniciar desliga o servidor e sobe outro com as mesmas dependências sobre o
// MESMO banco: é o restart ou o deploy, em que tudo o que só vive na memória do
// processo (o contador de provas inclusive) nasce de novo.
func (a *api) reiniciar(t *testing.T) {
	t.Helper()
	a.srv.Close()
	a.deps.Proofs = auth.NewFamilyProofs(a.pool, a.clk)
	a.srv = httptest.NewServer(httpapi.Routes(a.ctx, a.cfg, a.deps))
	t.Cleanup(a.srv.Close)
}

// O contador de provas erradas é da família e vive no banco: um restart no meio
// não devolve as tentativas. Quatro erradas, o restart, e a quinta derruba a
// família, que é o que a memória do processo não garantia.
func TestFifthWrongProofDropsTheFamilyEvenWithARestartInTheMiddle(t *testing.T) {
	a := apiOrigemPorPedido(t)
	token, _, familia := familiaComRefresh(t, a)
	errar := postEProva("/api/v1/auth/totp/enroll", func(*testing.T, *api) map[string]string {
		return map[string]string{"password": "senha-que-nao-e-a-dele"}
	})

	for i := 1; i < provasPorFamilia; i++ {
		if st, codigo := errar(t, a, token); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("prova errada %d: %d %s, queria 401 invalid_credentials", i, st, codigo)
		}
	}

	a.reiniciar(t)

	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusOK {
		t.Fatalf("a família caiu no restart, antes da quinta prova errada (GET /sessions: %d)", st)
	}
	if st, codigo := errar(t, a, token); st != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("quinta prova errada, depois do restart: %d %s, queria 401 unauthorized", st, codigo)
	}
	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusUnauthorized {
		t.Errorf("GET /sessions com o token da família derrubada: %d, queria 401", st)
	}
	if n := eventosDaFamilia(t, a, "session.proof_limit", familia); n != 1 {
		t.Errorf("%d eventos session.proof_limit da família, queria 1", n)
	}
}

// Quem erra e reinicia a cada prova gasta o mesmo orçamento de quem não
// reinicia: um restart por prova não zera nada.
func TestWrongProofBudgetSurvivesARestartPerProof(t *testing.T) {
	a := apiOrigemPorPedido(t)
	token, _, _ := familiaComRefresh(t, a)
	errar := postEProva("/api/v1/auth/totp/enroll", func(*testing.T, *api) map[string]string {
		return map[string]string{"password": "senha-que-nao-e-a-dele"}
	})

	for i := 1; i < provasPorFamilia; i++ {
		if st, codigo := errar(t, a, token); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("prova errada %d: %d %s, queria 401 invalid_credentials", i, st, codigo)
		}
		a.reiniciar(t)
	}
	if st, codigo := errar(t, a, token); st != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("quinta prova errada: %d %s, queria 401 unauthorized", st, codigo)
	}
	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusUnauthorized {
		t.Errorf("GET /sessions com o token da família derrubada: %d, queria 401", st)
	}
}
