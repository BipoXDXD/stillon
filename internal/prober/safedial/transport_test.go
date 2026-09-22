package safedial_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/sentinela/internal/prober/safedial"
)

// Este é o teste que importa. httptest.NewServer escuta em 127.0.0.1, então
// um cliente comum o alcança sem esforço. Se o cliente do safedial conseguir
// falar com ele, a guarda inteira é decorativa.
func TestClientCannotReachLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := safedial.NewClient(5 * time.Second)

	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("o cliente alcançou %s; a guarda não funcionou", srv.URL)
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
}

// Um alvo público que redireciona para IP interno é a forma mais simples de
// contornar validação feita só na URL de entrada. Como o cliente não reusa
// conexão, cada salto disca de novo e revalida o IP.
func TestRedirectToLoopbackIsBlocked(t *testing.T) {
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("segredo interno"))
	}))
	defer interno.Close()

	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, interno.URL, http.StatusFound)
	}))
	defer redirecionador.Close()

	client := safedial.NewClient(5 * time.Second)

	resp, err := client.Get(redirecionador.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("o redirecionamento para loopback não foi bloqueado")
	}
}

func TestRedirectToDisallowedSchemeIsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer srv.Close()

	client := safedial.NewClient(5 * time.Second)
	if resp, err := client.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("redirecionamento para file:// não foi bloqueado")
	}
}

func TestTransportDoesNotReuseConnections(t *testing.T) {
	// Reuso de conexão pula o dialer e, com ele, a revalidação do IP. Este
	// teste trava a configuração para que ninguém "otimize" o keep-alive de
	// volta sem perceber a consequência.
	tr := safedial.NewTransport()
	if !tr.DisableKeepAlives {
		t.Error("keep-alive ligado: uma conexão reusada não passa pelo ControlContext")
	}
	if tr.Proxy != nil {
		t.Error("Proxy não é nil: o sondador herdaria HTTP_PROXY do ambiente")
	}
}

// Os dois testes de redirecionamento acima nunca chegam a seguir um salto: o
// próprio httptest.Server escuta em loopback, então o bloqueio acontece já no
// primeiro dial. Eles provam a guarda de IP, não a revalidação por salto.
// Este teste exercita CheckRedirect diretamente, que é onde o esquema é
// revalidado e a cadeia é limitada.
func TestCheckRedirectRevalidaEsquemaELimitaSaltos(t *testing.T) {
	client := safedial.NewClient(5 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect é nil: nenhum salto seria revalidado")
	}

	req := func(raw string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("requisição de teste inválida: %v", err)
		}
		return r
	}

	// Um salto legítimo passa.
	if err := client.CheckRedirect(req("https://exemplo.com/x"), nil); err != nil {
		t.Errorf("salto legítimo recusado: %v", err)
	}

	// Um salto para esquema não permitido é recusado antes de discar.
	err := client.CheckRedirect(req("file:///etc/passwd"), nil)
	if !errors.Is(err, safedial.ErrInvalidURL) {
		t.Errorf("salto para file:// deu erro = %v, queria envolver ErrInvalidURL", err)
	}

	// A cadeia é limitada: no quarto salto a requisição para.
	via := []*http.Request{
		req("https://exemplo.com/1"),
		req("https://exemplo.com/2"),
		req("https://exemplo.com/3"),
	}
	err = client.CheckRedirect(req("https://exemplo.com/4"), via)
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("cadeia longa deu erro = %v, queria envolver ErrBlocked", err)
	}
}
