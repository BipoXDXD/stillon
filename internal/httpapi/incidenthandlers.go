package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/incident/history"
	"github.com/BipoXDXD/vigilume/internal/observability"
	"github.com/BipoXDXD/vigilume/internal/targets"
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

// List é GET /incidents?status=open|resolved&target_id=&cursor=&limit=
// (spec §9; target_id é do Plano 5).
func (h *IncidentHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := history.Filter{Limit: history.DefaultPageSize}

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
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", targets.ReasonInvalid)
			return
		}
		if n < 1 || n > int64(history.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
		f.Limit = int32(n)
	}
	if s := q.Get("cursor"); s != "" {
		c, err := uuid.Parse(s)
		if err != nil {
			campoInvalido(w, r, "cursor", targets.ReasonInvalid)
			return
		}
		f.Before = &c
	}
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

	items, err := h.svc.List(r.Context(), userID, f)
	if h.falhou(w, r, "listar incidentes", err) {
		return
	}
	resp := incidentListResponse{Items: make([]incidentResponse, 0, len(items))}
	for _, i := range items {
		resp.Items = append(resp.Items, toIncidentResponse(i))
	}
	if len(items) == int(f.Limit) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

// Ack é POST /incidents/{id}/ack. Idempotente: o segundo ack devolve o
// instante do primeiro.
func (h *IncidentHandlers) Ack(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	i, err := h.svc.Ack(r.Context(), userID, id)
	if h.falhou(w, r, "reconhecer incidente", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toIncidentResponse(i))
}

func (h *IncidentHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, history.ErrNotFound) {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return true
	}
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
	return true
}
