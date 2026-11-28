package tenancy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// Invitations é a fachada dos convites de projeto.
type Invitations struct {
	repo   InvitationRepository
	mailer InvitationMailer
	clk    clock.Clock
}

func NewInvitations(repo InvitationRepository, mailer InvitationMailer, clk clock.Clock) *Invitations {
	return &Invitations{repo: repo, mailer: mailer, clk: clk}
}

// Invite convida email (já normalizado por account.ValidateEmail) para o
// projeto com o papel dado. O e-mail sai só depois de o convite ser gravado,
// e é o mesmo exista ou não conta com o endereço. Falha ao enfileirar não
// muda a resposta: o convite está gravado, aparece no painel e pode ser
// reenviado; e a resposta não pode depender do envio.
func (s *Invitations) Invite(ctx context.Context, actorID, projectID uuid.UUID, email, role string) (Invitation, error) {
	// A coluna guarda o e-mail em caixa baixa e o aceite compara assim.
	email = strings.ToLower(email)
	papel, err := ParseRole(role)
	if err != nil {
		return Invitation{}, err
	}
	token, hash, err := newInvitationToken()
	if err != nil {
		return Invitation{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invitation{}, fmt.Errorf("gerar id do convite: %w", err)
	}
	inv, msg, err := s.repo.Create(ctx, actorID, projectID, NewInvitation{
		ID: id, Email: email, Role: papel, TokenHash: hash, Now: s.clk.Now(),
	})
	if err != nil {
		return Invitation{}, err
	}
	msg.To, msg.Role, msg.Token, msg.TokenHash, msg.InviterID = email, papel, token, hash, actorID
	if err := s.mailer.SendInvitation(ctx, msg); err != nil {
		// O erro pode conter dados da mensagem: o log é fixo.
		slog.ErrorContext(ctx, "e-mail de convite não enfileirado")
	}
	return inv, nil
}

// ListPending lista os convites pendentes não vencidos do projeto.
func (s *Invitations) ListPending(ctx context.Context, actorID, projectID uuid.UUID) ([]Invitation, error) {
	return s.repo.ListPending(ctx, actorID, projectID, s.clk.Now())
}

// Revoke apaga o convite pendente; o link dele morre.
func (s *Invitations) Revoke(ctx context.Context, actorID, projectID, invitationID uuid.UUID) error {
	return s.repo.Revoke(ctx, actorID, projectID, invitationID)
}

// Accept faz userID entrar no projeto do convite do token, com o papel dele.
// O token que não tem a forma dos gerados nem chega ao repositório.
func (s *Invitations) Accept(ctx context.Context, userID uuid.UUID, token string) (ProjectSummary, error) {
	hash, err := parseInvitationToken(token)
	if err != nil {
		return ProjectSummary{}, err
	}
	return s.repo.Accept(ctx, userID, hash, s.clk.Now())
}

// InvitationExpiry é o fim do prazo de um convite criado em now.
func InvitationExpiry(now time.Time) time.Time { return now.Add(InvitationTTL) }
