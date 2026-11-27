package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/BipoXDXD/stillon/internal/apierr"
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

// recusarCriacao traduz o erro de tenancy.Service.Create: nome inválido é 400
// com o campo, o teto do titular é 409, e o resto é erro interno.
func (h *ProjectHandlers) recusarCriacao(w http.ResponseWriter, r *http.Request, err error) {
	motivo := ""
	switch {
	case errors.Is(err, tenancy.ErrProjectNameRequired):
		motivo = "required"
	case errors.Is(err, tenancy.ErrProjectNameTooLong):
		motivo = "too_long"
	case errors.Is(err, tenancy.ErrProjectNameInvalid):
		motivo = "invalid"
	case errors.Is(err, tenancy.ErrProjectLimit):
		apierr.WriteError(w, r, apierr.CodeProjectLimit)
		return
	default:
		erroInterno(w, r, "criar projeto", err)
		return
	}
	apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "name", Reason: motivo}})
}

func toProjectResponse(p tenancy.ProjectSummary) projectResponse {
	return projectResponse{
		ID: p.ID.String(), Name: p.Name, IsDefault: p.IsDefault, Role: string(p.Role), CreatedAt: p.CreatedAt,
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
