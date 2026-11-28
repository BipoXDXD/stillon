package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// ProjectHandlers serve os projetos do usuário. A lista e a criação são da
// conta, e não de um projeto: as rotas ficam sem ProjetoPadrao nem
// ProjetoDoCaminho, e não criam o Projeto padrão de quem ainda não tem.
type ProjectHandlers struct {
	svc *tenancy.Service
}

func NewProjectHandlers(svc *tenancy.Service) *ProjectHandlers {
	return &ProjectHandlers{svc: svc}
}

// projectResponse é um item de GET /api/v1/projects, com o papel de quem
// pede naquele projeto.
type projectResponse struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	IsDefault bool           `json:"is_default"`
	Role      string         `json:"role"`
	CreatedAt time.Time      `json:"created_at"`
	Limits    limitsResponse `json:"limits"`
	// PendingTitular: a titularidade foi proposta a quem pede (PR 4.3). Só
	// sai quando true: o schema Projeto do painel ainda não tem o campo, e o
	// teste de contrato dele exige exatamente as chaves do schema (o 4.4 o
	// acrescenta).
	PendingTitular bool `json:"pending_titular,omitempty"`
}

// limitsResponse são as cotas do titular do projeto (tenancy.Limites): o
// painel lê delas o intervalo mínimo do projeto atual.
type limitsResponse struct {
	Projects           int   `json:"projects"`
	MembersPerProject  int   `json:"members_per_project"`
	Targets            int64 `json:"targets"`
	Heartbeats         int64 `json:"heartbeats"`
	ChannelsPerProject int64 `json:"channels_per_project"`
	MinIntervalSec     int32 `json:"min_interval_sec"`
}

// List é GET /api/v1/projects: os projetos de que o usuário é membro, o
// padrão primeiro, no envelope de lista de GET /api/v1/channels. Projeto de
// titular suspenso não aparece (tenancy.Service.List).
func (h *ProjectHandlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	ps, err := h.svc.List(r.Context(), userID)
	if err != nil {
		erroInterno(w, r, "listar projetos", err)
		return
	}
	items := make([]projectResponse, 0, len(ps))
	for _, p := range ps {
		items = append(items, toProjectResponse(p))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// projectRequest é o corpo de POST /api/v1/projects. Não há campo de
// titular: ele vem do token, e decodeJSON recusa campo desconhecido.
type projectRequest struct {
	Name string `json:"name"`
}

// Create é POST /api/v1/projects: cria um projeto de que o usuário é titular
// e owner, e responde 201 com o mesmo item de GET /api/v1/projects.
func (h *ProjectHandlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req projectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := h.svc.Create(r.Context(), userID, req.Name)
	if err != nil {
		h.recusarCriacao(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toProjectResponse(p))
}

// Rename é PATCH /api/v1/projects/{projectID} com {"name": ...}: renomear é
// de owner, e a resposta é o item de GET /api/v1/projects. A rota fica atrás
// de ProjetoDoCaminho, para o não membro receber o 404 de projeto inexistente.
func (h *ProjectHandlers) Rename(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	var req projectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := h.svc.Rename(r.Context(), sc.UserID, sc.ProjectID, req.Name)
	switch {
	case err == nil:
		apierr.WriteJSON(w, http.StatusOK, toProjectResponse(p))
	case errors.Is(err, tenancy.ErrForbidden):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, tenancy.ErrProjectNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	default:
		h.recusarNome(w, r, "renomear projeto", err)
	}
}

// recusarCriacao traduz o erro de tenancy.Service.Create: nome inválido é 400
// com o campo, o teto do titular é 409, e o resto é erro interno.
func (h *ProjectHandlers) recusarCriacao(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, tenancy.ErrProjectLimit) {
		apierr.WriteError(w, r, apierr.CodeProjectLimit)
		return
	}
	h.recusarNome(w, r, "criar projeto", err)
}

// recusarNome traduz o nome inválido em 400 com o campo; o resto é erro
// interno de op.
func (h *ProjectHandlers) recusarNome(w http.ResponseWriter, r *http.Request, op string, err error) {
	motivo := ""
	switch {
	case errors.Is(err, tenancy.ErrProjectNameRequired):
		motivo = "required"
	case errors.Is(err, tenancy.ErrProjectNameTooLong):
		motivo = "too_long"
	case errors.Is(err, tenancy.ErrProjectNameInvalid):
		motivo = "invalid"
	default:
		erroInterno(w, r, op, err)
		return
	}
	apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "name", Reason: motivo}})
}

func toProjectResponse(p tenancy.ProjectSummary) projectResponse {
	return projectResponse{
		ID: p.ID.String(), Name: p.Name, IsDefault: p.IsDefault, Role: string(p.Role), CreatedAt: p.CreatedAt,
		PendingTitular: p.PendingTitular,
		Limits: limitsResponse{
			Projects:           p.Limites.Projetos,
			MembersPerProject:  p.Limites.MembrosPorProjeto,
			Targets:            p.Limites.Alvos,
			Heartbeats:         p.Limites.Heartbeats,
			ChannelsPerProject: p.Limites.CanaisPorProjeto,
			MinIntervalSec:     p.Limites.IntervaloMinimoSec,
		},
	}
}

// transferRequest é o corpo de POST /titular-transfer: o owner a quem a
// titularidade é proposta.
type transferRequest struct {
	UserID string `json:"user_id"`
}

// ProposeTransfer é POST /api/v1/projects/{projectID}/titular-transfer com
// {"user_id": ...}: só o titular propõe, e só a outro owner (409
// titular_transfer_target, também para quem não é membro e para o Projeto
// padrão). 202: a titularidade só muda quando quem recebe aceita.
func (h *ProjectHandlers) ProposeTransfer(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	var req transferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	alvo, err := uuid.Parse(req.UserID)
	if err != nil {
		campoInvalido(w, r, "user_id", "invalid")
		return
	}
	if h.transferenciaFalhou(w, r, "propor a transferência", h.svc.ProposeTransfer(r.Context(), sc.UserID, sc.ProjectID, alvo)) {
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// AcceptTransfer é POST /api/v1/projects/{projectID}/titular-transfer/accept:
// quem recebe aceita, se a cota dele comporta o projeto, e a resposta é o
// item de GET /api/v1/projects. Quem não é o pendente recebe 403.
func (h *ProjectHandlers) AcceptTransfer(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	p, err := h.svc.AcceptTransfer(r.Context(), sc.UserID, sc.ProjectID)
	if h.transferenciaFalhou(w, r, "aceitar a transferência", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toProjectResponse(p))
}

// CancelTransfer é DELETE /api/v1/projects/{projectID}/titular-transfer: o
// titular cancela, quem recebe recusa (204); outro membro recebe 403.
func (h *ProjectHandlers) CancelTransfer(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	if h.transferenciaFalhou(w, r, "cancelar a transferência", h.svc.CancelTransfer(r.Context(), sc.UserID, sc.ProjectID)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// transferenciaFalhou traduz o erro da transferência. Quem saiu do projeto no
// meio do caminho recebe o 404 de projeto inexistente; as cotas de quem
// recebe são os 409 das criações.
func (h *ProjectHandlers) transferenciaFalhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, tenancy.ErrNotMember), errors.Is(err, tenancy.ErrProjectNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, tenancy.ErrForbidden):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, tenancy.ErrTransferTarget):
		apierr.WriteError(w, r, apierr.CodeTransferTarget)
	case errors.Is(err, tenancy.ErrProjectLimit):
		apierr.WriteError(w, r, apierr.CodeProjectLimit)
	case errors.Is(err, tenancy.ErrMemberLimit):
		apierr.WriteError(w, r, apierr.CodeMemberLimit)
	case errors.Is(err, targets.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeTargetLimit)
	case errors.Is(err, targets.ErrContentHostLimit):
		apierr.WriteError(w, r, apierr.CodeContentHostLimit)
	case errors.Is(err, heartbeat.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeHeartbeatLimit)
	case errors.Is(err, notify.ErrLimitReached):
		apierr.WriteError(w, r, apierr.CodeChannelLimit)
	default:
		erroInterno(w, r, op, err)
	}
	return true
}
