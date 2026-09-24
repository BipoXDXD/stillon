package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// VerifyHandlers servem o link do e-mail de verificação (spec §5.8, §9).
//
// O GET só mostra um botão; quem verifica é o POST do formulário (D2 deste
// plano). Leitores de link de provedores de e-mail abrem os links das
// mensagens para checar malware. Se o GET verificasse, o endereço de um
// terceiro seria confirmado sem ele: o relay de spam que a §5.8 fecha.
type VerifyHandlers struct {
	svc *notify.ChannelService
}

func NewVerifyHandlers(svc *notify.ChannelService) *VerifyHandlers {
	return &VerifyHandlers{svc: svc}
}

// cspVerificacao troca a CSP da API só nestas páginas: form-action 'self'
// deixa o botão enviar o formulário para a própria URL; o resto continua
// fechado, sem script e sem estilo.
const cspVerificacao = "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// As páginas são texto fixo: nada da requisição nem do banco entra nelas.
// O formulário não tem action, e por isso envia para a URL atual; o token
// não é escrito no HTML.
const (
	paginaConfirmar = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: confirmar e-mail</title>` +
		`<h1>Confirmar este e-mail</h1>` +
		`<p>Este endereço vai receber os alertas de incidente do Vigilume.</p>` +
		`<form method="post"><button type="submit">Confirmar</button></form></html>`
	paginaConfirmado = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: e-mail confirmado</title>` +
		`<h1>E-mail confirmado</h1><p>Os alertas de incidente vão chegar neste endereço.</p></html>`
	paginaInvalida = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Vigilume: link inválido</title>` +
		`<h1>Link inválido ou vencido</h1><p>Peça um novo link no painel do Vigilume.</p></html>`
	paginaErro = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<title>Vigilume: erro</title><h1>Algo deu errado</h1><p>Tente de novo em instantes.</p></html>`
)

func (h *VerifyHandlers) Page(w http.ResponseWriter, r *http.Request) {
	estado, err := h.svc.CheckToken(r.Context(), r.PathValue("token"))
	if err != nil {
		slog.Error("verificar token", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusInternalServerError, paginaErro)
		return
	}
	switch estado {
	case notify.VerifyPending:
		pagina(w, http.StatusOK, paginaConfirmar)
	case notify.VerifyDone:
		pagina(w, http.StatusOK, paginaConfirmado)
	default:
		pagina(w, http.StatusNotFound, paginaInvalida)
	}
}

func (h *VerifyHandlers) Confirm(w http.ResponseWriter, r *http.Request) {
	err := h.svc.ConfirmToken(r.Context(), r.PathValue("token"))
	switch {
	case err == nil:
		pagina(w, http.StatusOK, paginaConfirmado)
	case errors.Is(err, notify.ErrInvalidToken):
		pagina(w, http.StatusNotFound, paginaInvalida)
	default:
		slog.Error("confirmar token", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusInternalServerError, paginaErro)
	}
}

func pagina(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", cspVerificacao)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}
