package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// A guarda deve recusar wiring incompleto mesmo fora do Recover de Routes.
// Tirar a checagem de serviço ausente causa panic e torna o teste vermelho.
func TestLiveSessionGuardMissingAccountServiceFailsClosed(t *testing.T) {
	agora := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	chave := []byte("segredo-publico-de-teste-32bytes!!")
	token, err := auth.NewAccessToken(chave, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	h := &AccountHandlers{}
	handler := middleware.Authn(chave, clock.NewFake(agora))(h.exigirSessaoViva(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("wiring sem serviço de conta autorizou o handler")
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != nil {
				t.Error("a guarda entrou em panic com serviço de conta ausente")
			}
		}()
		handler.ServeHTTP(rec, req)
	}()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &env)
	if err != nil || rec.Code != http.StatusInternalServerError || env.Error.Code != "internal_error" {
		t.Errorf("wiring ausente: %d, código %q, JSON válido %t; queria 500/internal_error", rec.Code, env.Error.Code, err == nil)
	}
}
