package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
)

// A consulta da página pública se desliga do contexto do visitante (o voo é
// de todos) e tem o próprio teto de 5 s, então um banco preso não mostra o
// prazo da cadeia de fora. O teste olha o contexto que a página recebe: ele
// tem de vencer com o prazo da configuração, e não com 15 s fixos.
func TestPaginaPublicaAplicaOPrazoDaConfiguracao(t *testing.T) {
	const prazo = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var restante time.Duration
	cadeia := paginaPublica(ctx, &config.Config{RequestTimeout: prazo}, clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)),
		func(w http.ResponseWriter, r *http.Request) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("página sem prazo")
			}
			restante = time.Until(deadline)
			w.WriteHeader(http.StatusNoContent)
		})
	w := httptest.NewRecorder()
	cadeia.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p/prazo-da-configuracao", nil))
	if w.Code != http.StatusNoContent || restante <= 0 || restante > prazo {
		t.Fatalf("status=%d restante=%v, queria até %v", w.Code, restante, prazo)
	}
}
