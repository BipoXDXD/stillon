package prober

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// novoSemBanco monta o agendador de produção sem tocar o banco: New só guarda
// as dependências, e o que se observa aqui é o que ele faz ao nascer.
func novoSemBanco(t *testing.T, hospedagem []netip.Prefix) *Scheduler {
	t.Helper()
	m := NewMetrics(prometheus.NewRegistry())
	s := New(nil, nil, clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)), m, hospedagem)
	t.Cleanup(func() { s.Stop(time.Second) })
	return s
}

// avisosDeListaVazia conta as linhas WARN do aviso da lista vazia.
func avisosDeListaVazia(log string) int {
	n := 0
	for _, linha := range strings.Split(log, "\n") {
		if strings.Contains(linha, "level=WARN") && strings.Contains(linha, "var=PROBE_BLOCKED_CIDRS") &&
			strings.Contains(linha, avisoListaDaHospedagemVazia) {
			n++
		}
	}
	return n
}

// Sem a lista, o sondador sobe e avisa: uma linha, no nascimento, e não uma
// por sonda.
func TestListaDaHospedagemVaziaAvisaUmaVez(t *testing.T) {
	log := capturarLog(t)

	novoSemBanco(t, nil)

	if n := avisosDeListaVazia(log.String()); n != 1 {
		t.Errorf("o aviso da lista vazia saiu %d vezes, queria 1; log:\n%s", n, log.String())
	}
}

func TestListaDaHospedagemPreenchidaNaoAvisa(t *testing.T) {
	log := capturarLog(t)

	novoSemBanco(t, []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")})

	if n := avisosDeListaVazia(log.String()); n != 0 {
		t.Errorf("o aviso da lista vazia saiu %d vezes com a lista preenchida; log:\n%s", n, log.String())
	}
}

// O gauge é o sinal que um alerta consegue ler: 0 é a lista vazia.
func TestGaugeDaListaDaHospedagem(t *testing.T) {
	cases := []struct {
		nome       string
		hospedagem []netip.Prefix
		quer       float64
	}{
		{nome: "vazia", quer: 0},
		{nome: "preenchida", hospedagem: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("2001:db8::/32"),
		}, quer: 2},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s := novoSemBanco(t, c.hospedagem)
			if got := testutil.ToFloat64(s.metrics.blockedCIDRs); got != c.quer {
				t.Errorf("probe_blocked_cidrs = %v, queria %v", got, c.quer)
			}
		})
	}
}

// O fio de New até o cliente: a conferência da hospedagem roda antes da lista
// fixa, então o erro cita a faixa da hospedagem, e não a 127.0.0.0/8. Se New
// entregasse ao cliente outra lista (ou nil), a mesma recusa citaria a fixa.
func TestNewEntregaAListaDaHospedagemAoCliente(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	s := novoSemBanco(t, []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})

	resp, err := s.client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET %s conectou (status %d), queria ErrBlocked", srv.URL, resp.StatusCode)
	}
	if !errors.Is(err, safedial.ErrBlocked) || !strings.Contains(err.Error(), "pertence a 127.0.0.1/32") {
		t.Errorf("erro = %v, queria ErrBlocked citando a faixa da hospedagem 127.0.0.1/32", err)
	}
}
