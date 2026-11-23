// Package turnstiletest dá aos testes um siteverify falso que imita a
// semântica documentada das chaves de teste públicas da Cloudflare
// (developers.cloudflare.com/turnstile/troubleshooting/testing). As chaves
// abaixo são públicas e só servem para teste e dev: nunca para produção.
package turnstiletest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/turnstile"
)

// Chaves de teste públicas da Cloudflare.
const (
	// SiteKeyPassa é a site key do widget visível que sempre passa.
	SiteKeyPassa = "1x00000000000000000000AA"
	// SecretPassa sempre aceita o TokenFalso.
	SecretPassa = "1x0000000000000000000000000000000AA"
	// SecretFalha sempre recusa (invalid-input-response).
	SecretFalha = "2x0000000000000000000000000000000AA"
	// SecretJaUsado sempre responde token já usado (timeout-or-duplicate).
	SecretJaUsado = "3x0000000000000000000000000000000AA"
	// TokenFalso é o token que as site keys de teste geram.
	TokenFalso = "XXXX.DUMMY.TOKEN.XXXX"
)

// hostDeTeste é o hostname que o siteverify devolve para as chaves de teste.
const hostDeTeste = "example.com"

type gravador struct {
	mu        sync.Mutex
	recebidos []url.Values
}

var (
	mu       sync.Mutex
	gravados = map[*turnstile.Verificador]*gravador{}
)

// Verificador devolve um turnstile.Verificador ligado a um siteverify falso
// em httptest, com o secret dado e sem conferência de host (as chaves de
// teste respondem example.com). Como o de verdade, as chaves de teste só
// aceitam o TokenFalso, e um secret desconhecido dá invalid-input-secret.
func Verificador(t testing.TB, secret string) *turnstile.Verificador {
	t.Helper()
	g := &gravador{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulário inválido", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.recebidos = append(g.recebidos, r.PostForm)
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responder(r.PostForm))
	}))
	t.Cleanup(srv.Close)

	v := turnstile.NewComEndpoint(crypto.Secret(secret), "", srv.URL, srv.Client())
	mu.Lock()
	gravados[v] = g
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(gravados, v)
		mu.Unlock()
	})
	return v
}

func responder(f url.Values) map[string]any {
	falha := func(codigo string) map[string]any {
		return map[string]any{"success": false, "error-codes": []string{codigo}}
	}
	switch f.Get("secret") {
	case SecretPassa:
		if f.Get("response") != TokenFalso {
			return falha("invalid-input-response")
		}
		return map[string]any{
			"success": true, "error-codes": []string{},
			"challenge_ts": "2022-02-28T15:14:30.096Z", "hostname": hostDeTeste,
		}
	case SecretFalha:
		return falha("invalid-input-response")
	case SecretJaUsado:
		return falha("timeout-or-duplicate")
	default:
		return falha("invalid-input-secret")
	}
}

// Recebidos devolve os formulários que o siteverify falso do verificador
// recebeu, na ordem. Verificador que não veio deste pacote dá nil.
func Recebidos(v *turnstile.Verificador) []url.Values {
	mu.Lock()
	g := gravados[v]
	mu.Unlock()
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]url.Values(nil), g.recebidos...)
}
