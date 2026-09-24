package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

type ChannelHandlers struct {
	svc *notify.ChannelService
}

func NewChannelHandlers(svc *notify.ChannelService) *ChannelHandlers {
	return &ChannelHandlers{svc: svc}
}

type channelRequest struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Email string `json:"email"`
}

// channelResponse não tem campo para o destino: só o hint. Nenhuma mudança
// no domínio faz a URL do webhook ou o endereço aparecerem aqui.
type channelResponse struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Hint      string    `json:"hint"`
	Status    string    `json:"status"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func toChannelResponse(c notify.Channel) channelResponse {
	return channelResponse{
		ID: c.ID.String(), Kind: string(c.Kind), Hint: c.Hint, Status: string(c.Status),
		Enabled: c.Enabled, CreatedAt: c.CreatedAt,
	}
}

func (h *ChannelHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	cs, err := h.svc.List(r.Context(), userID)
	if h.falhou(w, r, "listar canais", err) {
		return
	}
	items := make([]channelResponse, 0, len(cs))
	for _, c := range cs {
		items = append(items, toChannelResponse(c))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ChannelHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req channelRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	c, err := h.svc.Create(r.Context(), userID, notify.ChannelInput{
		Kind: notify.Kind(req.Kind), URL: req.URL, Email: req.Email,
	})
	if h.falhou(w, r, "criar canal", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toChannelResponse(c))
}

func (h *ChannelHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "apagar canal", h.svc.Delete(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChannelHandlers) Resend(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "reenviar verificação", h.svc.ResendVerification(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChannelHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *notify.ValidationError
	switch {
	case errors.As(err, &verr):
		campoInvalido(w, r, verr.Field, verr.Reason)
	case errors.Is(err, notify.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, notify.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeChannelLimit)
	case errors.Is(err, notify.ErrNotPending):
		apierr.WriteError(w, r, apierr.CodeChannelNotPending)
	case errors.Is(err, notify.ErrDeliveryFailed):
		apierr.WriteError(w, r, apierr.CodeDeliveryFailed)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}
