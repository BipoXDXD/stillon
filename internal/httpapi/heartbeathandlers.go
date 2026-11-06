package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// Tipos do agendamento no contrato (spec de heartbeats, §7).
const (
	scheduleKindPeriod = "period"
	scheduleKindCron   = "cron"
)

type HeartbeatHandlers struct {
	svc     *heartbeat.Service
	baseURL string
}

func NewHeartbeatHandlers(svc *heartbeat.Service, publicBaseURL string) *HeartbeatHandlers {
	return &HeartbeatHandlers{svc: svc, baseURL: strings.TrimRight(publicBaseURL, "/")}
}

type scheduleDTO struct {
	Kind      string `json:"kind"`
	PeriodSec *int32 `json:"period_sec,omitempty"`
	Expr      string `json:"expr,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

type heartbeatRequest struct {
	Name     string       `json:"name"`
	Tags     []string     `json:"tags"`
	Schedule *scheduleDTO `json:"schedule"`
	GraceSec *int32       `json:"grace_sec"`
	Enabled  *bool        `json:"enabled"`
}

type heartbeatPatchRequest struct {
	Name     *string            `json:"name"`
	Tags     optional[[]string] `json:"tags"`
	Schedule *scheduleDTO       `json:"schedule"`
	GraceSec *int32             `json:"grace_sec"`
	Enabled  *bool              `json:"enabled"`
}

// heartbeatResponse é construído campo a campo. Não existe campo para o hash,
// e ping_url só aparece na variante de criação e rotação.
type heartbeatResponse struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Tags           []string    `json:"tags"`
	Enabled        bool        `json:"enabled"`
	Schedule       scheduleDTO `json:"schedule"`
	GraceSec       int64       `json:"grace_sec"`
	State          string      `json:"state"`
	DeadlineAt     time.Time   `json:"deadline_at"`
	LastPingAt     *time.Time  `json:"last_ping_at"`
	LastSuccessAt  *time.Time  `json:"last_success_at"`
	LastStartAt    *time.Time  `json:"last_start_at"`
	LastRuntimeMS  *int64      `json:"last_runtime_ms"`
	OpenIncidentID *string     `json:"open_incident_id"`
	CreatedAt      time.Time   `json:"created_at"`
}

// heartbeatWithURLResponse é a única saída com a URL de ping, que leva o
// token: sai uma vez, na criação ou na rotação.
type heartbeatWithURLResponse struct {
	heartbeatResponse
	PingURL string `json:"ping_url"`
}

type heartbeatListResponse struct {
	Items      []heartbeatResponse `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type heartbeatEventResponse struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	RuntimeMS *int64    `json:"runtime_ms"`
	ExitCode  *int16    `json:"exit_code"`
}

type heartbeatEventListResponse struct {
	Items      []heartbeatEventResponse `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type heartbeatIncidentResponse struct {
	ID             string     `json:"id"`
	HeartbeatID    string     `json:"heartbeat_id"`
	HeartbeatName  string     `json:"heartbeat_name"`
	StartedAt      time.Time  `json:"started_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	Cause          string     `json:"cause"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

type heartbeatIncidentListResponse struct {
	Items      []heartbeatIncidentResponse `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

func toHeartbeatResponse(h heartbeat.Heartbeat) heartbeatResponse {
	r := heartbeatResponse{
		ID: h.ID.String(), Name: h.Name, Tags: h.Tags, Enabled: h.Enabled, GraceSec: int64(h.Grace / time.Second),
		State: string(h.Status), DeadlineAt: h.DeadlineAt, LastPingAt: h.LastPingAt,
		LastSuccessAt: h.LastSuccessAt, LastStartAt: h.LastStartAt, LastRuntimeMS: h.LastRuntimeMS, CreatedAt: h.CreatedAt,
		Schedule: toScheduleDTO(h.Schedule),
	}
	if h.OpenIncidentID != nil {
		id := h.OpenIncidentID.String()
		r.OpenIncidentID = &id
	}
	return r
}

func toScheduleDTO(s heartbeat.Schedule) scheduleDTO {
	if s.IsCron() {
		return scheduleDTO{Kind: scheduleKindCron, Expr: s.CronExpr(), Timezone: s.Timezone()}
	}
	periodo := int32(s.Period() / time.Second) // #nosec G115 -- até 31 dias, validado em NewPeriod.
	return scheduleDTO{Kind: scheduleKindPeriod, PeriodSec: &periodo}
}

// schedule converte o DTO. Campo da outra forma é erro, e não é ignorado: um
// expr junto de period_sec diria uma coisa e faria outra.
func (d scheduleDTO) schedule() (heartbeat.Schedule, error) {
	switch d.Kind {
	case scheduleKindPeriod:
		if d.Expr != "" || d.Timezone != "" {
			return heartbeat.Schedule{}, &heartbeat.ValidationError{Field: "schedule", Reason: heartbeat.ReasonInvalid}
		}
		if d.PeriodSec == nil {
			return heartbeat.Schedule{}, &heartbeat.ValidationError{Field: "period_sec", Reason: heartbeat.ReasonRequired}
		}
		return heartbeat.NewPeriod(time.Duration(*d.PeriodSec) * time.Second)
	case scheduleKindCron:
		if d.PeriodSec != nil {
			return heartbeat.Schedule{}, &heartbeat.ValidationError{Field: "schedule", Reason: heartbeat.ReasonInvalid}
		}
		return heartbeat.NewCron(d.Expr, d.Timezone)
	}
	return heartbeat.Schedule{}, &heartbeat.ValidationError{Field: "schedule.kind", Reason: heartbeat.ReasonInvalid}
}

func (h *HeartbeatHandlers) pingURL(token string) string {
	return h.baseURL + "/ping/" + token
}

func (h *HeartbeatHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req heartbeatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Schedule == nil {
		campoInvalido(w, r, "schedule", heartbeat.ReasonRequired)
		return
	}
	sch, err := req.Schedule.schedule()
	if h.falhou(w, r, "validar heartbeat", err) {
		return
	}
	in := heartbeat.Input{Name: req.Name, Tags: req.Tags, Schedule: sch, Grace: heartbeat.DefaultGrace, Enabled: true}
	if req.GraceSec != nil {
		in.Grace = time.Duration(*req.GraceSec) * time.Second
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	criado, token, err := h.svc.Create(r.Context(), userID, in)
	if h.falhou(w, r, "criar heartbeat", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, heartbeatWithURLResponse{toHeartbeatResponse(criado), h.pingURL(token)})
}

func (h *HeartbeatHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	limite, cursor, ok := paginaDe(w, r)
	if !ok {
		return
	}
	items, err := h.svc.List(r.Context(), userID, cursor, limite)
	if h.falhou(w, r, "listar heartbeats", err) {
		return
	}
	resp := heartbeatListResponse{Items: make([]heartbeatResponse, 0, len(items))}
	for _, i := range items {
		resp.Items = append(resp.Items, toHeartbeatResponse(i))
	}
	if len(items) == int(limite) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

type cronPreviewResponse struct {
	NextRuns []time.Time `json:"next_runs"`
}

// parametrosDaPrevia são os únicos aceitos, uma vez cada: parâmetro desconhecido
// ou repetido é 400, como no resumo (parametrosDoResumo).
var parametrosDaPrevia = []string{"expr", "timezone"}

// CronPreview é GET /heartbeats/cron-preview: quem julga a expressão é o
// servidor (T11), e o formulário mostra o que ele calculou. Sem estado e sem
// recurso: nada de dono a filtrar, mas a rota é autenticada como as outras.
func (h *HeartbeatHandlers) CronPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := usuario(w, r); !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		campoInvalido(w, r, campoDaConsulta, heartbeat.ReasonInvalid)
		return
	}
	for chave, valores := range q {
		if !slices.Contains(parametrosDaPrevia, chave) || len(valores) != 1 {
			campoInvalido(w, r, campoDaConsulta, heartbeat.ReasonInvalid)
			return
		}
	}
	proximos, err := h.svc.CronPreview(q.Get("expr"), q.Get("timezone"))
	if h.falhou(w, r, "calcular prévia do cron", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, cronPreviewResponse{NextRuns: proximos})
}

func (h *HeartbeatHandlers) Get(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	item, err := h.svc.Get(r.Context(), userID, id)
	if h.falhou(w, r, "ler heartbeat", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toHeartbeatResponse(item))
}

func (h *HeartbeatHandlers) Update(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	var req heartbeatPatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p := heartbeat.Patch{SetTags: req.Tags.Set, Tags: req.Tags.Value, Name: req.Name, Enabled: req.Enabled}
	if req.Schedule != nil {
		sch, err := req.Schedule.schedule()
		if h.falhou(w, r, "validar heartbeat", err) {
			return
		}
		p.Schedule = &sch
	}
	if req.GraceSec != nil {
		grace := time.Duration(*req.GraceSec) * time.Second
		p.Grace = &grace
	}
	item, err := h.svc.Update(r.Context(), userID, id, p)
	if h.falhou(w, r, "atualizar heartbeat", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toHeartbeatResponse(item))
}

func (h *HeartbeatHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "apagar heartbeat", h.svc.Delete(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HeartbeatHandlers) RotateToken(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	item, token, err := h.svc.RotateToken(r.Context(), userID, id)
	if h.falhou(w, r, "rotacionar token", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, heartbeatWithURLResponse{toHeartbeatResponse(item), h.pingURL(token)})
}

func (h *HeartbeatHandlers) Events(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	limite, cursor, ok := paginaDe(w, r)
	if !ok {
		return
	}
	items, err := h.svc.Events(r.Context(), userID, id, cursor, limite)
	if h.falhou(w, r, "listar eventos", err) {
		return
	}
	resp := heartbeatEventListResponse{Items: make([]heartbeatEventResponse, 0, len(items))}
	for _, e := range items {
		resp.Items = append(resp.Items, heartbeatEventResponse{ID: e.ID.String(), At: e.At, Kind: e.Kind, RuntimeMS: e.RuntimeMS, ExitCode: e.ExitCode})
	}
	if len(items) == int(limite) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

func (h *HeartbeatHandlers) Incidents(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	limite, cursor, ok := paginaDe(w, r)
	if !ok {
		return
	}
	var aberto *bool
	switch r.URL.Query().Get("status") {
	case "":
	case "open":
		v := true
		aberto = &v
	case "resolved":
		aberto = new(bool)
	default:
		campoInvalido(w, r, "status", heartbeat.ReasonInvalid)
		return
	}
	items, err := h.svc.Incidents(r.Context(), userID, id, aberto, cursor, limite)
	if h.falhou(w, r, "listar incidentes de heartbeat", err) {
		return
	}
	resp := heartbeatIncidentListResponse{Items: make([]heartbeatIncidentResponse, 0, len(items))}
	for _, i := range items {
		resp.Items = append(resp.Items, toHeartbeatIncidentResponse(i))
	}
	if len(items) == int(limite) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

func (h *HeartbeatHandlers) AckIncident(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := donoEID(w, r)
	if !ok {
		return
	}
	incidente, err := uuid.Parse(r.PathValue("incident_id"))
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return
	}
	item, err := h.svc.Ack(r.Context(), userID, id, incidente)
	if h.falhou(w, r, "reconhecer incidente de heartbeat", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toHeartbeatIncidentResponse(item))
}

func toHeartbeatIncidentResponse(i heartbeat.Incident) heartbeatIncidentResponse {
	return heartbeatIncidentResponse{
		ID: i.ID.String(), HeartbeatID: i.HeartbeatID.String(), HeartbeatName: i.HeartbeatName,
		StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, Cause: i.Cause, AcknowledgedAt: i.AckAt,
	}
}

func donoEID(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := usuario(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, ok := pathID(w, r)
	return userID, id, ok
}

// paginaDe lê limit e cursor. Fora da faixa ou malformado é 400, nunca o
// padrão em silêncio.
func paginaDe(w http.ResponseWriter, r *http.Request) (int32, *uuid.UUID, bool) {
	q := r.URL.Query()
	limite := heartbeat.DefaultPageSize
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", heartbeat.ReasonInvalid)
			return 0, nil, false
		}
		if n < 1 || n > int64(heartbeat.MaxPageSize) {
			campoInvalido(w, r, "limit", heartbeat.ReasonOutOfRange)
			return 0, nil, false
		}
		limite = int32(n)
	}
	s := q.Get("cursor")
	if s == "" {
		return limite, nil, true
	}
	c, err := uuid.Parse(s)
	if err != nil {
		campoInvalido(w, r, "cursor", heartbeat.ReasonInvalid)
		return 0, nil, false
	}
	return limite, &c, true
}

func (h *HeartbeatHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *heartbeat.ValidationError
	switch {
	case errors.Is(err, heartbeat.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, heartbeat.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeHeartbeatLimit)
	case errors.As(err, &verr):
		campoInvalido(w, r, verr.Field, verr.Reason)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}
