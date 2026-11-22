package httpapi

import (
	"net/http"
	"time"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// ProjectHandlers serve os projetos do usuário. A lista é da conta, e não de
// um projeto: a rota fica em protegida, sem ProjetoPadrao nem
// ProjetoDoCaminho, e não cria o Projeto padrão de quem ainda não tem.
type ProjectHandlers struct {
	svc *tenancy.Service
}

func NewProjectHandlers(svc *tenancy.Service) *ProjectHandlers {
	return &ProjectHandlers{svc: svc}
}

// projectResponse é um item de GET /api/v1/projects, com o papel de quem
// pede naquele projeto.
type projectResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
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
		items = append(items, projectResponse{
			ID: p.ID.String(), Name: p.Name, IsDefault: p.IsDefault, Role: string(p.Role), CreatedAt: p.CreatedAt,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
