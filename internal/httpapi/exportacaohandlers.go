package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/exportacao"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// Exportador escreve a exportação da conta em w: o exportacao.Servico em
// produção. ipHash vai para o registro de auditoria. Um erro pode chegar
// depois de bytes já escritos.
type Exportador interface {
	Escrever(ctx context.Context, userID uuid.UUID, ipHash []byte, w io.Writer) error
}

// prazoDeEscrita é o prazo de cada escrita do anexo, renovado antes de cada
// uma: um cliente que para de ler derruba a conexão em 60 s, e um download
// longo que segue lendo não esbarra no WriteTimeout fixo do servidor.
const prazoDeEscrita = 60 * time.Second

// ExportacaoHandlers serve GET /api/v1/me/export.
type ExportacaoHandlers struct {
	exp     Exportador
	clk     clock.Clock
	proxies middleware.Proxies
	// ipKey é a chave do HMAC do IP, a mesma do cadastro e da conta.
	ipKey []byte
}

func NewExportacaoHandlers(exp Exportador, clk clock.Clock, proxies middleware.Proxies, ipKey []byte) *ExportacaoHandlers {
	return &ExportacaoHandlers{exp: exp, clk: clk, proxies: proxies, ipKey: ipKey}
}

// semHead responde HEAD com 405 (Allow: GET) antes de qualquer trabalho: o
// ServeMux casa o HEAD no padrão GET da exportação, e cada HEAD gastaria o
// balde e gravaria auditoria sem entregar nada. É um embrulho, e não um
// padrão "HEAD ..." próprio, para a rota continuar uma só nas varreduras de
// rotasRegistradas.
func semHead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Allow", http.MethodGet)
			apierr.WriteError(w, r, apierr.CodeMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
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
		rc:   http.NewResponseController(w),
		nome: "stillon-export-" + h.clk.Now().UTC().Format("2006-01-02") + ".json",
	}
	ipHash := crypto.HashIP(h.ipKey, middleware.ClientIP(r, h.proxies))
	err := h.exp.Escrever(r.Context(), userID, ipHash, anexo)
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

// anexoPreguicoso põe os headers do anexo na primeira escrita, e não antes, e
// renova o prazo de escrita da conexão antes de cada uma (prazoDeEscrita).
type anexoPreguicoso struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	nome    string
	comecou bool
}

func (a *anexoPreguicoso) Write(p []byte) (int, error) {
	// Sem suporte (um ResponseWriter de teste), fica o prazo do servidor.
	// Qualquer outro erro é falha de escrita.
	if err := a.rc.SetWriteDeadline(time.Now().Add(prazoDeEscrita)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, fmt.Errorf("renovar o prazo de escrita: %w", err)
	}
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
