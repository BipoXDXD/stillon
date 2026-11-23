package turnstile_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/turnstile"
	"github.com/BipoXDXD/stillon/internal/turnstile/turnstiletest"
)

func TestVerificarAceitaOSecretQuePassa(t *testing.T) {
	v := turnstiletest.Verificador(t, turnstiletest.SecretPassa)

	if err := v.Verificar(context.Background(), turnstiletest.TokenFalso, "203.0.113.7", "signup"); err != nil {
		t.Fatalf("Verificar = %v, queria nil", err)
	}
	recebidos := turnstiletest.Recebidos(v)
	if len(recebidos) != 1 {
		t.Fatalf("o siteverify recebeu %d pedidos, queria 1", len(recebidos))
	}
	f := recebidos[0]
	if f.Get("secret") != turnstiletest.SecretPassa {
		t.Errorf("secret = %q, queria o secret do verificador", f.Get("secret"))
	}
	if f.Get("response") != turnstiletest.TokenFalso {
		t.Errorf("response = %q, queria o token", f.Get("response"))
	}
	if f.Get("remoteip") != "203.0.113.7" {
		t.Errorf("remoteip = %q, queria o IP do cliente", f.Get("remoteip"))
	}
	if f.Get("idempotency_key") == "" {
		t.Error("idempotency_key vazio")
	}
}

func TestVerificarRecusaOSecretQueFalha(t *testing.T) {
	v := turnstiletest.Verificador(t, turnstiletest.SecretFalha)

	err := v.Verificar(context.Background(), turnstiletest.TokenFalso, "203.0.113.7", "signup")
	if !errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("Verificar = %v, queria ErrRecusado", err)
	}
}

func TestVerificarRecusaTokenJaUsado(t *testing.T) {
	v := turnstiletest.Verificador(t, turnstiletest.SecretJaUsado)

	err := v.Verificar(context.Background(), turnstiletest.TokenFalso, "203.0.113.7", "signup")
	if !errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("Verificar = %v, queria ErrRecusado", err)
	}
}

func TestVerificarRecusaSemRede(t *testing.T) {
	casos := map[string]string{
		"token vazio":         "",
		"token de 2049 bytes": strings.Repeat("a", 2049),
	}
	for nome, token := range casos {
		t.Run(nome, func(t *testing.T) {
			v := turnstiletest.Verificador(t, turnstiletest.SecretPassa)

			err := v.Verificar(context.Background(), token, "203.0.113.7", "signup")
			if !errors.Is(err, turnstile.ErrRecusado) {
				t.Fatalf("Verificar = %v, queria ErrRecusado", err)
			}
			if n := len(turnstiletest.Recebidos(v)); n != 0 {
				t.Fatalf("o siteverify recebeu %d pedidos, queria nenhum", n)
			}
		})
	}
}

// servidorQueResponde sobe um siteverify que devolve sempre o mesmo corpo.
func servidorQueResponde(t *testing.T, status int, corpo string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func respostaDeSucesso(t *testing.T, hostname, action string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"success": true, "error-codes": []string{}, "hostname": hostname, "action": action,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func verificadorEm(endpoint, hostEsperado string) *turnstile.Verificador {
	return turnstile.NewComEndpoint(crypto.Secret("segredo-de-teste"), hostEsperado, endpoint, http.DefaultClient)
}

func TestVerificarRecusaOutroHost(t *testing.T) {
	endpoint := servidorQueResponde(t, http.StatusOK, respostaDeSucesso(t, "outro.test", ""))

	err := verificadorEm(endpoint, "stillon.tech").Verificar(context.Background(), "tok", "203.0.113.7", "signup")
	if !errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("host diferente: Verificar = %v, queria ErrRecusado", err)
	}
	if err := verificadorEm(endpoint, "").Verificar(context.Background(), "tok", "203.0.113.7", "signup"); err != nil {
		t.Fatalf("sem host esperado: Verificar = %v, queria nil", err)
	}
}

func TestVerificarRecusaOutraAcao(t *testing.T) {
	outra := servidorQueResponde(t, http.StatusOK, respostaDeSucesso(t, "stillon.tech", "forgot"))
	err := verificadorEm(outra, "stillon.tech").Verificar(context.Background(), "tok", "203.0.113.7", "signup")
	if !errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("ação diferente: Verificar = %v, queria ErrRecusado", err)
	}

	vazia := servidorQueResponde(t, http.StatusOK, respostaDeSucesso(t, "stillon.tech", ""))
	if err := verificadorEm(vazia, "stillon.tech").Verificar(context.Background(), "tok", "203.0.113.7", "signup"); err != nil {
		t.Fatalf("ação vazia: Verificar = %v, queria nil", err)
	}
}

func TestVerificarFalhaFechadoSemCloudflare(t *testing.T) {
	recusada := httptest.NewServer(http.NotFoundHandler())
	enderecoRecusado := recusada.URL
	recusada.Close()

	casos := map[string]string{
		"status 500":       servidorQueResponde(t, http.StatusInternalServerError, `{"success":true}`),
		"corpo inválido":   servidorQueResponde(t, http.StatusOK, `{"success":`),
		"conexão recusada": enderecoRecusado,
	}
	for nome, endpoint := range casos {
		t.Run(nome, func(t *testing.T) {
			err := verificadorEm(endpoint, "").Verificar(context.Background(), "tok", "203.0.113.7", "signup")
			if err == nil {
				t.Fatal("Verificar = nil, queria erro")
			}
			if errors.Is(err, turnstile.ErrRecusado) {
				t.Fatalf("Verificar = %v; falha da Cloudflare não pode virar recusa", err)
			}
			if strings.Contains(err.Error(), "segredo-de-teste") {
				t.Fatalf("o erro expõe o secret: %v", err)
			}
		})
	}
}

func TestVerificarNaoSegueRedirect(t *testing.T) {
	var seguiu atomic.Bool
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seguiu.Store(true)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(destino.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destino.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origem.Close)

	err := verificadorEm(origem.URL, "").Verificar(context.Background(), "tok", "203.0.113.7", "signup")
	if err == nil || errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("Verificar = %v, queria erro de infraestrutura", err)
	}
	if seguiu.Load() {
		t.Fatal("o verificador seguiu o redirect e reenviou o secret")
	}
}
