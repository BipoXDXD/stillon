package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// http.ErrAbortHandler é o pedido explícito de derrubar a conexão (a
// exportação que falha no meio do download). Recover o deixa subir até o
// servidor, que fecha a conexão sem escrever nada; trocá-lo por um envelope
// 500 colaria JSON de erro no fim de uma resposta 200 já começada.
func TestRecoverDeixaErrAbortHandlerSubir(t *testing.T) {
	h := middleware.Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"parcial":`))
		panic(http.ErrAbortHandler)
	}))
	rec := httptest.NewRecorder()
	var motivo any
	func() {
		defer func() { motivo = recover() }()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if motivo != http.ErrAbortHandler {
		t.Fatalf("panic que saiu do Recover = %v, queria http.ErrAbortHandler", motivo)
	}
	if got := rec.Body.String(); got != `{"parcial":` {
		t.Errorf("corpo = %q: o Recover escreveu depois do abort", got)
	}
}

// Qualquer outro panic continua virando o envelope 500.
func TestRecoverTrocaOutroPanicPor500(t *testing.T) {
	h := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(errors.New("defeito"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, queria 500", rec.Code)
	}
}
