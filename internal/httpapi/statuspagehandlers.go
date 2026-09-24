package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/statuspage"
)

// StatusPageHandlers servem o cadastro do dono (spec §9). A API é no
// plural, como a §9, mas cada usuário tem uma página só (spec §2.1): GET
// devolve uma lista de zero ou um item.
type StatusPageHandlers struct {
	svc     *statuspage.Service
	baseURL string
}

func NewStatusPageHandlers(svc *statuspage.Service, publicBaseURL string) *StatusPageHandlers {
	return &StatusPageHandlers{svc: svc, baseURL: strings.TrimSuffix(publicBaseURL, "/")}
}

type statusPageItemJSON struct {
	TargetID    uuid.UUID `json:"target_id"`
	DisplayName string    `json:"display_name"`
}

type statusPageRequest struct {
	Slug      string               `json:"slug"`
	Title     string               `json:"title"`
	Published bool                 `json:"published"`
	Items     []statusPageItemJSON `json:"items"`
}

type statusPagePatchRequest struct {
	Slug      *string               `json:"slug"`
	Title     *string               `json:"title"`
	Published *bool                 `json:"published"`
	Items     *[]statusPageItemJSON `json:"items"`
}

type statusPageResponse struct {
	ID        string               `json:"id"`
	Slug      string               `json:"slug"`
	Title     string               `json:"title"`
	Published bool                 `json:"published"`
	Items     []statusPageItemJSON `json:"items"`
	// PublicURL é o link que o painel mostra (spec §11.3). Existe mesmo em
	// rascunho: o dono vê o endereço antes de publicar, e ele dá 404 até lá.
	PublicURL string    `json:"public_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *StatusPageHandlers) toResponse(p statuspage.Page) statusPageResponse {
	items := make([]statusPageItemJSON, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, statusPageItemJSON{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return statusPageResponse{
		ID: p.ID.String(), Slug: p.Slug, Title: p.Title, Published: p.Published, Items: items,
		PublicURL: h.baseURL + "/p/" + p.Slug, CreatedAt: p.CreatedAt,
	}
}

func paraItens(in []statusPageItemJSON) []statuspage.Item {
	out := make([]statuspage.Item, 0, len(in))
	for _, it := range in {
		out = append(out, statuspage.Item{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return out
}

func (h *StatusPageHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	items := []statusPageResponse{}
	p, err := h.svc.Get(r.Context(), userID)
	switch {
	case errors.Is(err, statuspage.ErrNotFound):
	case h.falhou(w, r, "buscar status page", err):
		return
	default:
		items = append(items, h.toResponse(p))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *StatusPageHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req statusPageRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	p, err := h.svc.Create(r.Context(), userID, statuspage.Input{
		Slug: req.Slug, Title: req.Title, Published: req.Published, Items: paraItens(req.Items),
	})
	if h.falhou(w, r, "criar status page", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, h.toResponse(p))
}

func (h *StatusPageHandlers) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req statusPagePatchRequest
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	patch := statuspage.Patch{Slug: req.Slug, Title: req.Title, Published: req.Published}
	if req.Items != nil {
		itens := paraItens(*req.Items)
		patch.Items = &itens
	}
	p, err := h.svc.Update(r.Context(), userID, id, patch)
	if h.falhou(w, r, "atualizar status page", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, h.toResponse(p))
}

// falhou traduz o erro do domínio. Os campos de validação são vocabulário
// fechado; "items[3].display_name" leva só a posição, nunca o conteúdo.
func (h *StatusPageHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *statuspage.ValidationError
	switch {
	case errors.As(err, &verr):
		campos := make([]apierr.FieldError, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			campos = append(campos, apierr.FieldError{Field: f.Field, Reason: f.Reason})
		}
		apierr.WriteValidation(w, r, campos)
	case errors.Is(err, statuspage.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, statuspage.ErrExists):
		apierr.WriteError(w, r, apierr.CodeStatusPageExists)
	case errors.Is(err, statuspage.ErrSlugTaken):
		apierr.WriteError(w, r, apierr.CodeSlugTaken)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}
