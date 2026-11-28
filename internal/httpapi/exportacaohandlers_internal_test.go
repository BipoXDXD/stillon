package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/exportacao"
)

// exportadorFalso escreve saida e devolve err: com saida vazia, o erro chega
// antes do primeiro byte; com saida, depois.
type exportadorFalso struct {
	saida string
	err   error
}

func (e exportadorFalso) Escrever(_ context.Context, _ uuid.UUID, w io.Writer) error {
	if e.saida != "" {
		if _, err := io.WriteString(w, e.saida); err != nil {
			return err
		}
	}
	return e.err
}

var diaDaExportacao = time.Date(2026, 11, 28, 23, 59, 0, 0, time.UTC)

func exportarCom(t *testing.T, e Exportador) *httptest.ResponseRecorder {
	t.Helper()
	h := NewExportacaoHandlers(e, clock.NewFake(diaDaExportacao))
	return comUsuario(t, h.Exportar, httptest.NewRequest(http.MethodGet, "/api/v1/me/export", nil))
}

// O handler põe os próprios headers, sem contar com a cadeia: o no-store
// global de SecurityHeaders cobre a API inteira, e este teste prova que o
// download não depende dele.
func TestExportarHandlerAnexoSemCache(t *testing.T) {
	rec := exportarCom(t, exportadorFalso{saida: `{"format":"stillon-export"}` + "\n"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, queria 200: %s", rec.Code, rec.Body)
	}
	for nome, esperado := range map[string]string{
		"Content-Type":        "application/json",
		"Content-Disposition": `attachment; filename="stillon-export-2026-11-28.json"`,
		"Cache-Control":       "no-store",
	} {
		if got := rec.Header().Get(nome); got != esperado {
			t.Errorf("%s = %q, queria %q", nome, got, esperado)
		}
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Errorf("corpo não é JSON válido: %s", rec.Body)
	}
}

// Antes do primeiro byte ainda dá para responder o envelope: 500
// internal_error, sem o header de anexo (o navegador não salva o erro como
// arquivo).
func TestExportarErroAntesDoPrimeiroByteDaEnvelope(t *testing.T) {
	rec := exportarCom(t, exportadorFalso{err: errors.New("banco fora")})
	if rec.Code != http.StatusInternalServerError || lerCodigo(t, rec) != string(apierr.CodeInternal) {
		t.Fatalf("status = %d %s, queria 500 internal_error", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q no erro, queria vazio", got)
	}
}

// Usuário que sumiu entre o token e a leitura: 401, como no /me.
func TestExportarUsuarioInexistenteDa401(t *testing.T) {
	rec := exportarCom(t, exportadorFalso{err: exportacao.ErrUsuarioInexistente})
	if rec.Code != http.StatusUnauthorized || lerCodigo(t, rec) != string(apierr.CodeUnauthorized) {
		t.Fatalf("status = %d %s, queria 401 unauthorized", rec.Code, rec.Body)
	}
}

// Depois do primeiro byte, o status 200 já saiu: o handler aborta a resposta
// (http.ErrAbortHandler) para o download falhar em vez de chegar truncado.
func TestExportarErroDepoisDoPrimeiroByteAborta(t *testing.T) {
	var motivo any
	func() {
		defer func() { motivo = recover() }()
		exportarCom(t, exportadorFalso{saida: `{"format":`, err: errors.New("banco caiu no meio")})
	}()
	if motivo != http.ErrAbortHandler {
		t.Fatalf("panic = %v, queria http.ErrAbortHandler", motivo)
	}
}
