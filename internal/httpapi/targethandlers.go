package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/targets"
)

type TargetHandlers struct {
	svc *targets.Service
	clk clock.Clock
}

func NewTargetHandlers(svc *targets.Service, clk clock.Clock) *TargetHandlers {
	return &TargetHandlers{svc: svc, clk: clk}
}

type targetRequest struct {
	Name                string            `json:"name"`
	URL                 string            `json:"url"`
	Method              string            `json:"method"`
	IntervalSec         int32             `json:"interval_sec"`
	TimeoutMS           int32             `json:"timeout_ms"`
	ExpectedStatus      int32             `json:"expected_status"`
	DegradedThresholdMS optional[int32]   `json:"degraded_threshold_ms"`
	Enabled             *bool             `json:"enabled"`
	Headers             map[string]string `json:"headers"`
}

type targetPatchRequest struct {
	Name                *string                     `json:"name"`
	URL                 *string                     `json:"url"`
	Method              *string                     `json:"method"`
	IntervalSec         *int32                      `json:"interval_sec"`
	TimeoutMS           *int32                      `json:"timeout_ms"`
	ExpectedStatus      *int32                      `json:"expected_status"`
	DegradedThresholdMS optional[int32]             `json:"degraded_threshold_ms"`
	Enabled             *bool                       `json:"enabled"`
	Headers             optional[map[string]string] `json:"headers"`
}

// targetResponse é o DTO de saída, construído campo a campo. Não existe
// campo para valor de header, então nenhuma mudança futura no domínio o faz
// aparecer aqui por acidente (D5).
type targetResponse struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	Method              string    `json:"method"`
	IntervalSec         int32     `json:"interval_sec"`
	TimeoutMS           int32     `json:"timeout_ms"`
	ExpectedStatus      int32     `json:"expected_status"`
	DegradedThresholdMS *int32    `json:"degraded_threshold_ms"`
	Enabled             bool      `json:"enabled"`
	HeaderNames         []string  `json:"header_names"`
	CreatedAt           time.Time `json:"created_at"`
}

type targetListResponse struct {
	Items      []targetResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type checkResponse struct {
	CheckedAt  time.Time `json:"checked_at"`
	Status     string    `json:"status"`
	LatencyMS  int32     `json:"latency_ms"`
	StatusCode *int32    `json:"status_code"`
	ErrorKind  *string   `json:"error_kind"`
}

// rollupResponse é uma hora da série quando resolution é "1h" (D6).
type rollupResponse struct {
	Bucket time.Time `json:"bucket"`
	Total  int32     `json:"total"`
	OK     int32     `json:"ok"`
	P50    *int32    `json:"p50_ms"`
	P95    *int32    `json:"p95_ms"`
	P99    *int32    `json:"p99_ms"`
	Max    *int32    `json:"max_ms"`
}

func toTargetResponse(t targets.Target) targetResponse {
	return targetResponse{
		ID: t.ID.String(), Name: t.Name, URL: t.URL, Method: t.Method,
		IntervalSec: t.IntervalSec, TimeoutMS: t.TimeoutMS, ExpectedStatus: t.ExpectedStatus,
		DegradedThresholdMS: t.DegradedThresholdMS, Enabled: t.Enabled,
		HeaderNames: t.HeaderNames, CreatedAt: t.CreatedAt,
	}
}

func (h *TargetHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req targetRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := targets.Input{
		Name: req.Name, URL: req.URL, Method: req.Method,
		IntervalSec: req.IntervalSec, TimeoutMS: req.TimeoutMS, ExpectedStatus: req.ExpectedStatus,
		Enabled: true, Headers: req.Headers,
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	// Ausente → padrão; null → desativado; número → valor (spec §11.5).
	switch {
	case !req.DegradedThresholdMS.Set:
		v := targets.DefaultDegradedThresholdMS
		in.DegradedThresholdMS = &v
	case !req.DegradedThresholdMS.Null:
		v := req.DegradedThresholdMS.Value
		in.DegradedThresholdMS = &v
	}

	t, err := h.svc.Create(r.Context(), userID, in)
	if h.falhou(w, r, "criar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toTargetResponse(t))
}

func (h *TargetHandlers) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Get(r.Context(), userID, id)
	if h.falhou(w, r, "buscar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toTargetResponse(t))
}

func (h *TargetHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	limit := targets.DefaultPageSize
	if s := q.Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			campoInvalido(w, r, "limit", targets.ReasonInvalid)
			return
		}
		if n < 1 || n > int64(targets.MaxPageSize) {
			campoInvalido(w, r, "limit", targets.ReasonOutOfRange)
			return
		}
		limit = int32(n)
	}
	var before *uuid.UUID
	if s := q.Get("cursor"); s != "" {
		c, err := uuid.Parse(s)
		if err != nil {
			campoInvalido(w, r, "cursor", targets.ReasonInvalid)
			return
		}
		before = &c
	}

	items, err := h.svc.List(r.Context(), userID, before, limit)
	if h.falhou(w, r, "listar alvos", err) {
		return
	}
	resp := targetListResponse{Items: make([]targetResponse, 0, len(items))}
	for _, t := range items {
		resp.Items = append(resp.Items, toTargetResponse(t))
	}
	// Página cheia pode ter continuação; página incompleta, certamente não.
	if len(items) == int(limit) {
		resp.NextCursor = items[len(items)-1].ID.String()
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

func (h *TargetHandlers) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req targetPatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	p := targets.Patch{
		Name: req.Name, URL: req.URL, Method: req.Method,
		IntervalSec: req.IntervalSec, TimeoutMS: req.TimeoutMS, ExpectedStatus: req.ExpectedStatus,
		Enabled: req.Enabled,
	}
	if req.DegradedThresholdMS.Set {
		p.SetDegradedThreshold = true
		if !req.DegradedThresholdMS.Null {
			v := req.DegradedThresholdMS.Value
			p.DegradedThresholdMS = &v
		}
	}
	if req.Headers.Set {
		p.SetHeaders = true
		p.Headers = req.Headers.Value // null deixa Value nil: limpa
	}

	t, err := h.svc.Update(r.Context(), userID, id, p)
	if h.falhou(w, r, "atualizar alvo", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toTargetResponse(t))
}

func (h *TargetHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "apagar alvo", h.svc.Delete(r.Context(), userID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// janelasDaSerie são as janelas que GET /targets/{id}/checks aceita em
// ?window= (B6 do Plano 10): as do gráfico do painel, e só elas.
var janelasDaSerie = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

func (h *TargetHandlers) Checks(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	to := h.clk.Now()
	if s := q.Get("to"); s != "" {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			campoInvalido(w, r, "to", targets.ReasonInvalid)
			return
		}
		to = v
	}
	from := to.Add(-24 * time.Hour)
	// B6 (Plano 10): com window, o servidor calcula o from, e o gráfico não
	// depende do relógio do navegador. window e from juntos seriam duas
	// respostas para a mesma pergunta.
	if q.Has("window") {
		janela, ok := janelasDaSerie[q.Get("window")]
		if !ok || q.Has("from") {
			campoInvalido(w, r, "window", targets.ReasonInvalid)
			return
		}
		from = to.Add(-janela)
	}
	if s := q.Get("from"); s != "" {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			campoInvalido(w, r, "from", targets.ReasonInvalid)
			return
		}
		from = v
	}

	serie, err := h.svc.Checks(r.Context(), userID, id, from, to)
	if h.falhou(w, r, "listar checks", err) {
		return
	}
	// resolution discrimina o formato dos itens: o cliente não adivinha pela
	// forma do JSON.
	if serie.Resolution == targets.Resolution1h {
		items := make([]rollupResponse, 0, len(serie.Rollups))
		for _, b := range serie.Rollups {
			items = append(items, rollupResponse{
				Bucket: b.Bucket, Total: b.Total, OK: b.OK, P50: b.P50, P95: b.P95, P99: b.P99, Max: b.Max,
			})
		}
		apierr.WriteJSON(w, http.StatusOK, map[string]any{"resolution": serie.Resolution, "items": items})
		return
	}
	items := make([]checkResponse, 0, len(serie.Checks))
	for _, c := range serie.Checks {
		items = append(items, checkResponse{
			CheckedAt: c.CheckedAt, Status: c.Status, LatencyMS: c.LatencyMS,
			StatusCode: c.StatusCode, ErrorKind: c.ErrorKind,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"resolution": serie.Resolution, "items": items})
}

// falhou traduz o erro do domínio para o envelope e devolve true se havia
// erro. O detalhe de um erro inesperado vai só para o log, correlacionado
// pelo trace_id (spec §9.1).
func (h *TargetHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	if err == nil {
		return false
	}
	var verr *targets.ValidationError
	switch {
	case errors.Is(err, targets.ErrNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, targets.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeTargetLimit)
	case errors.As(err, &verr):
		campos := make([]apierr.FieldError, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			campos = append(campos, apierr.FieldError{Field: f.Field, Reason: f.Reason})
		}
		apierr.WriteValidation(w, r, campos)
	default:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	}
	return true
}

func usuario(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	}
	return id, ok
}

type sparkResponse struct {
	Bucket time.Time `json:"bucket"`
	Total  int32     `json:"total"`
	OK     int32     `json:"ok"`
	P50MS  *int32    `json:"p50_ms"`
}

// targetSummaryResponse é o item de GET /targets/summary, no shape do
// contrato dos Planos 5–7. Campo a campo, como targetResponse: nada de URL
// nem de headers.
type targetSummaryResponse struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Enabled        bool            `json:"enabled"`
	State          string          `json:"state"`
	LastCheckedAt  *time.Time      `json:"last_checked_at"`
	LastLatencyMS  *int32          `json:"last_latency_ms"`
	Uptime24h      *float64        `json:"uptime_24h"`
	OpenIncidentID *string         `json:"open_incident_id"`
	Spark          []sparkResponse `json:"spark"`
}

// Summary é GET /targets/summary: o dashboard numa chamada só (spec §11.3),
// sem paginação (o teto de alvos por usuário é o limite).
func (h *TargetHandlers) Summary(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	resumo, err := h.svc.Summary(r.Context(), userID)
	if h.falhou(w, r, "resumir alvos", err) {
		return
	}
	itens := make([]targetSummaryResponse, 0, len(resumo))
	for _, s := range resumo {
		item := targetSummaryResponse{
			ID: s.ID.String(), Name: s.Name, Enabled: s.Enabled, State: string(s.State),
			LastCheckedAt: s.LastCheckedAt, LastLatencyMS: s.LastLatencyMS, Uptime24h: s.Uptime24h,
			Spark: make([]sparkResponse, 0, len(s.Spark)),
		}
		if s.OpenIncidentID != nil {
			id := s.OpenIncidentID.String()
			item.OpenIncidentID = &id
		}
		for _, hr := range s.Spark {
			item.Spark = append(item.Spark, sparkResponse{Bucket: hr.Bucket, Total: hr.Total, OK: hr.OK, P50MS: hr.P50})
		}
		itens = append(itens, item)
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": itens})
}

// pathID lê o {id} da rota. Malformado é 404 (D16): o recurso não existe de
// jeito nenhum, e um 400 ensinaria o formato do id a quem varre.
func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func campoInvalido(w http.ResponseWriter, r *http.Request, campo, motivo string) {
	apierr.WriteValidation(w, r, []apierr.FieldError{{Field: campo, Reason: motivo}})
}
