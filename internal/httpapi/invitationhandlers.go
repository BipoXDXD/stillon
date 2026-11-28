package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// InvitationHandlers servem os convites de um projeto e o aceite. Criar,
// listar e revogar estão atrás de ProjetoDoCaminho (o não membro recebe 404);
// o papel que autoriza é o que o pgstore relê na transação, sob a trava dos
// membros. O aceite é da conta: o projeto vem do convite.
type InvitationHandlers struct {
	svc *tenancy.Invitations
}

func NewInvitationHandlers(svc *tenancy.Invitations) *InvitationHandlers {
	return &InvitationHandlers{svc: svc}
}

// invitationResponse é um item de GET /invitations e a resposta do POST: a
// mesma forma exista ou não conta com o e-mail convidado.
type invitationResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toInvitationResponse(i tenancy.Invitation) invitationResponse {
	return invitationResponse{
		ID: i.ID.String(), Email: i.Email, Role: string(i.Role), CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
	}
}

// List é GET /invitations: os pendentes não vencidos. De manager para cima.
func (h *InvitationHandlers) List(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	is, err := h.svc.ListPending(r.Context(), sc.UserID, sc.ProjectID)
	if h.falhou(w, r, "listar convites", err) {
		return
	}
	items := make([]invitationResponse, 0, len(is))
	for _, i := range is {
		items = append(items, toInvitationResponse(i))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Create é POST /invitations com {"email", "role"}: 201 com o convite, a
// mesma resposta exista ou não conta com o e-mail (o e-mail sai depois do
// commit, e a resposta não depende do envio). Convidar de novo o mesmo
// e-mail troca o convite (o link antigo morre).
func (h *InvitationHandlers) Create(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := account.ValidateEmail(req.Email)
	if err != nil {
		campoInvalido(w, r, "email", "invalid")
		return
	}
	inv, err := h.svc.Invite(r.Context(), sc.UserID, sc.ProjectID, email, req.Role)
	if h.falhou(w, r, "convidar", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toInvitationResponse(inv))
}

// Revoke é DELETE /invitations/{invitationID}: 204; o link morre. Revogar
// pede o mesmo papel que convidar com o papel do convite.
func (h *InvitationHandlers) Revoke(w http.ResponseWriter, r *http.Request) {
	sc, ok := escopo(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("invitationID"))
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return
	}
	if h.falhou(w, r, "revogar convite", h.svc.Revoke(r.Context(), sc.UserID, sc.ProjectID, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Accept é POST /invitations/accept com {"token"}: 200 com o projeto em que
// entrou, na forma de GET /projects. Não exige e-mail confirmado (o token
// prova a caixa de entrada) e não o confirma.
//   - 400 token_invalid: inexistente, revogado, aceito ou vencido, sem
//     distinção;
//   - 403 invitation_other_account: o link é válido, mas de outro e-mail.
func (h *InvitationHandlers) Accept(w http.ResponseWriter, r *http.Request) {
	userID, ok := usuario(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !tokenDeContaPlausivel(req.Token) {
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
		return
	}
	p, err := h.svc.Accept(r.Context(), userID, req.Token)
	if h.falhou(w, r, "aceitar convite", err) {
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toProjectResponse(p))
}

// falhou traduz o erro do serviço de convites.
func (h *InvitationHandlers) falhou(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, tenancy.ErrUnknownRole):
		campoInvalido(w, r, "role", "invalid")
	case errors.Is(err, tenancy.ErrInvitationInvalid):
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
	case errors.Is(err, tenancy.ErrInvitationOtherAccount):
		apierr.WriteError(w, r, apierr.CodeInvitationOther)
	case errors.Is(err, tenancy.ErrNotMember), errors.Is(err, tenancy.ErrProjectNotFound),
		errors.Is(err, tenancy.ErrInvitationNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case errors.Is(err, tenancy.ErrForbidden):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, tenancy.ErrMemberLimit):
		apierr.WriteError(w, r, apierr.CodeMemberLimit)
	case errors.Is(err, tenancy.ErrAlreadyMember):
		apierr.WriteError(w, r, apierr.CodeAlreadyMember)
	default:
		erroInterno(w, r, op, err)
	}
	return true
}
