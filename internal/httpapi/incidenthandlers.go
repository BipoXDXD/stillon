package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

type IncidentHandlers struct {
	svc *history.Service
}

func NewIncidentHandlers(svc *history.Service) *IncidentHandlers {
	return &IncidentHandlers{svc: svc}
}

// incidentResponse é construído campo a campo, como targetResponse.
type incidentResponse struct {
	ID             string     `json:"id"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	Cause          string     `json:"cause"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

type incidentListResponse struct {
	Items      []incidentResponse `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func toIncidentResponse(i history.Incident) incidentResponse {
	return incidentResponse{
		ID: i.ID.String(), TargetID: i.TargetID.String(), TargetName: i.TargetName,
		StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, Cause: i.Cause, AcknowledgedAt: i.AckAt,
	}
}

var parametrosDaListaDeIncidentes = []string{"status", "target_id", "cursor", "limit"}

// List é GET /incidents?status=open|resolved&target_id=&cursor=&limit=
// (spec §9).
func (h *IncidentHandlers) List(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	q, ok := consultaPermitida(w, r, parametrosDaListaDeIncidentes)
	if !ok {
		return
	}
	var f history.Filter

	switch q.Get("status") {
	case "":
	case "open":
		f.Open = new(bool)
		*f.Open = true
	case "resolved":
		f.Open = new(bool)
	default:
		campoInvalido(w, r, "status", targets.ReasonInvalid)
		return
	}
	limite, cursor, ok := paginaDe(w, r, q, history.DefaultPageSize, history.MaxPageSize)
	if !ok {
		return
	}
	f.Limit, f.Before = limite, cursor
	// target_id malformado é 400, como o cursor: é parâmetro de consulta, e
	// não o recurso do caminho (que seria 404).
	if s := q.Get("target_id"); s != "" {
		alvo, err := uuid.Parse(s)
		if err != nil {
			campoInvalido(w, r, "target_id", targets.ReasonInvalid)
			return
		}
		f.TargetID = &alvo
	}

	items, err := h.svc.List(r.Context(), sc, f)
	if h.falhou(w, r, "listar incidentes", err) {
		return
	}
	resp := incidentListResponse{Items: make([]incidentResponse, 0, len(items))}
	for _, i := range items {
		resp.Items = append(resp.Items, toIncidentResponse(i))
	}
	resp.NextCursor = proximoCursor(items, f.Limit, func(i history.Incident) uuid.UUID { return i.ID })
	apierr.WriteJSON(w, http.StatusOK, resp)
}

// Ack é POST /incidents/{id}/ack. Idempotente: o segundo ack devolve o
// instante do primeiro.
func (h *IncidentHandlers) Ack(w http.ResponseWriter, r *http.Request) {
	sc, id, ok := escopoEID(w, r)
	if !ok {
		return
	}
	i, err := h.svc.Ack(r.Context(), sc, id)
	if h.falhou(w, r, "reconhecer incidente", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toIncidentResponse(i))
}

// falhou traduz o erro do serviço. Projeto sem pertença (ErrProjectNotFound,
// da transação) é o mesmo 404 de incidente inexistente; papel sem a permissão
// é 403.
func (h *IncidentHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, history.ErrNotFound), errors.Is(err, tenancy.ErrProjectNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, tenancy.ErrForbidden):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	default:
		erroInterno(w, r, op, err)
	}
	return true
}
