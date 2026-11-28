package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// MemberHandlers serve a equipe de um projeto. As rotas existem só por
// projeto (/api/v1/projects/{projectID}/members), atrás de ProjetoDoCaminho:
// quem não é membro recebe o 404 de projeto inexistente antes de chegar aqui.
// O papel de quem pede é conferido de novo na transação do pgstore, sob a
// trava dos membros, e não pelo escopo do middleware.
type MemberHandlers struct {
	svc *tenancy.Service
}

func NewMemberHandlers(svc *tenancy.Service) *MemberHandlers {
	return &MemberHandlers{svc: svc}
}

// memberResponse é um item de GET /members. Campo a campo: nada do vínculo
// além do que a tela de membros mostra.
type memberResponse struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	IsTitular bool      `json:"is_titular"`
	JoinedAt  time.Time `json:"joined_at"`
}

func toMemberResponse(m tenancy.MemberView) memberResponse {
	return memberResponse{
		UserID: m.UserID.String(), Email: m.Email, Role: string(m.Role), IsTitular: m.IsTitular, JoinedAt: m.JoinedAt,
	}
}

// List é GET /members: a equipe, o titular primeiro. Qualquer papel lê.
func (h *MemberHandlers) List(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	ms, err := h.svc.Members(r.Context(), sc.UserID, sc.ProjectID)
	if h.falhou(w, r, "listar membros", err) {
		return
	}
	items := make([]memberResponse, 0, len(ms))
	for _, m := range ms {
		items = append(items, toMemberResponse(m))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ChangeRole é PATCH /members/{userID} com {"role": ...}: só owner muda papel,
// e o titular nunca deixa de ser owner (409). Papel ausente ou fora dos quatro
// é role: invalid.
func (h *MemberHandlers) ChangeRole(w http.ResponseWriter, r *http.Request) {
	sc, alvo, ok := escopoEMembro(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := h.svc.ChangeRole(r.Context(), sc.UserID, sc.ProjectID, alvo, req.Role)
	if h.falhou(w, r, "mudar papel", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toMemberResponse(m))
}

// Remove é DELETE /members/{userID}: o owner remove; qualquer membro sai
// (userID é ele mesmo); o titular não sai nem é removido (409).
func (h *MemberHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	sc, alvo, ok := escopoEMembro(w, r)
	if !ok {
		return
	}
	if h.falhou(w, r, "remover membro", h.svc.RemoveMember(r.Context(), sc.UserID, sc.ProjectID, alvo)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// falhou traduz o erro do serviço. Alvo que não é membro e projeto sem
// pertença são o mesmo 404; papel sem a permissão é 403.
func (h *MemberHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, tenancy.ErrUnknownRole):
		campoInvalido(w, r, "role", "invalid")
	case errors.Is(err, tenancy.ErrNotMember), errors.Is(err, tenancy.ErrProjectNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, tenancy.ErrForbidden):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, tenancy.ErrTitularProtected):
		apierr.WriteError(w, r, apierr.CodeTitularProtected)
	default:
		erroInterno(w, r, op, err)
	}
	return true
}

// escopoEMembro abre os handlers com {userID}: o escopo do projeto e o
// membro-alvo. userID malformado é o 404 de membro inexistente.
func escopoEMembro(w http.ResponseWriter, r *http.Request) (tenancy.ProjectScope, uuid.UUID, bool) {
	sc, ok := escopo(w, r)
	if !ok {
		return tenancy.ProjectScope{}, uuid.Nil, false
	}
	alvo, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return tenancy.ProjectScope{}, uuid.Nil, false
	}
	return sc, alvo, true
}
