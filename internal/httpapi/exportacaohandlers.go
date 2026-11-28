package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/exportacao"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// Exportador escreve a exportação da conta em w: o exportacao.Servico em
// produção. Um erro pode chegar depois de bytes já escritos.
type Exportador interface {
	Escrever(ctx context.Context, userID uuid.UUID, w io.Writer) error
}

// ExportacaoHandlers serve GET /api/v1/me/export.
type ExportacaoHandlers struct {
	exp Exportador
	clk clock.Clock
}

func NewExportacaoHandlers(exp Exportador, clk clock.Clock) *ExportacaoHandlers {
	return &ExportacaoHandlers{exp: exp, clk: clk}
}

// Exportar entrega os dados da conta como anexo JSON, escrito em fluxo.
//
// Os headers do anexo só saem com o primeiro byte: até ali, um erro ainda vira
// o envelope de sempre (500 internal_error, ou 401 se a conta sumiu). Depois,
// o 200 já foi enviado, e o handler aborta a resposta (http.ErrAbortHandler)
// para o download falhar em vez de chegar truncado.
func (h *ExportacaoHandlers) Exportar(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	anexo := &anexoPreguicoso{
		w:    w,
		nome: "stillon-export-" + h.clk.Now().UTC().Format("2006-01-02") + ".json",
	}
	err := h.exp.Escrever(r.Context(), userID, anexo)
	if err == nil {
		return
	}
	if anexo.comecou {
		traceID := observability.TraceID(r.Context())
		if errors.Is(err, context.Canceled) && errors.Is(r.Context().Err(), context.Canceled) {
			slog.Info("exportação interrompida: cliente desistiu", "erro", err, "trace_id", traceID)
		} else {
			slog.Error("exportação interrompida depois do primeiro byte", "erro", err, "trace_id", traceID)
		}
		panic(http.ErrAbortHandler)
	}
	if errors.Is(err, exportacao.ErrUsuarioInexistente) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	erroInterno(w, r, "exportar a conta", err)
}

// anexoPreguicoso põe os headers do anexo na primeira escrita, e não antes.
type anexoPreguicoso struct {
	w       http.ResponseWriter
	nome    string
	comecou bool
}

func (a *anexoPreguicoso) Write(p []byte) (int, error) {
	if !a.comecou {
		a.comecou = true
		hd := a.w.Header()
		hd.Set("Content-Type", "application/json")
		hd.Set("Content-Disposition", `attachment; filename="`+a.nome+`"`)
		// O no-store global de SecurityHeaders já cobre a API; este é do
		// próprio download, que não pode depender da cadeia.
		hd.Set("Cache-Control", "no-store")
		a.w.WriteHeader(http.StatusOK)
	}
	return a.w.Write(p)
}
