package tenancy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// InvitationTTL é o prazo do convite: 7 dias, uso único.
const InvitationTTL = 7 * 24 * time.Hour

// invitationTokenBytes são os 256 bits de entropia do token do link.
const invitationTokenBytes = 32

var (
	// ErrInvitationInvalid: convite inexistente, revogado, já aceito ou
	// vencido. Um erro só, para não contar a quem chuta qual dos quatro.
	ErrInvitationInvalid = errors.New("convite inválido")
	// ErrInvitationOtherAccount: o link é válido, mas o convite é de outro
	// e-mail. Só sai depois de o token se provar válido.
	ErrInvitationOtherAccount = errors.New("o convite é de outra conta")
	// ErrInvitationNotFound: revogar um convite pendente que não existe no
	// projeto.
	ErrInvitationNotFound = errors.New("convite não encontrado")
)

// Invitation é um convite pendente como a equipe o vê.
type Invitation struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Email     string
	Role      Role
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewInvitation é o convite a gravar. Email já normalizado; o token não é
// gravado, só o SHA-256 dele.
type NewInvitation struct {
	ID        uuid.UUID
	Email     string
	Role      Role
	TokenHash []byte
	Now       time.Time
}

// InvitationEmail é o que o e-mail do convite leva. O repositório preenche
// ProjectName e InviterEmail; o serviço, o resto.
type InvitationEmail struct {
	To           string
	ProjectName  string
	InviterEmail string
	// InviterID é quem convida: o e-mail conta no teto diário dessa conta.
	InviterID uuid.UUID
	Role      Role
	Token     string
	TokenHash []byte
}

// InvitationRepository é a porta de persistência dos convites. Create,
// ListPending e Revoke rodam no escopo do projeto, agindo como actorID, com a
// autorização pelo papel lido na própria transação, depois da trava dos
// membros; não membro dá ErrProjectNotFound.
type InvitationRepository interface {
	// Create grava o convite: ErrForbidden pelo papel (AuthorizeInvite),
	// ErrAlreadyMember se o e-mail já é membro, ErrMemberLimit se membros
	// além do titular mais pendentes válidos já chegaram a MemberLimit. Um
	// pendente anterior do mesmo e-mail é substituído (reenvio).
	Create(ctx context.Context, actorID, projectID uuid.UUID, inv NewInvitation) (Invitation, InvitationEmail, error)
	// ListPending lista os pendentes não vencidos (PermInviteBasic).
	ListPending(ctx context.Context, actorID, projectID uuid.UUID, now time.Time) ([]Invitation, error)
	// Revoke apaga o pendente (o link morre). Revogar pede o mesmo que
	// convidar com o papel do convite; ErrInvitationNotFound sem ele.
	Revoke(ctx context.Context, actorID, projectID, invitationID uuid.UUID) error
	// Accept consome o convite do hash e cria o vínculo de userID com o papel
	// dele, numa transação: ErrInvitationInvalid (inexistente, revogado,
	// aceito, vencido) ou ErrInvitationOtherAccount (outro e-mail).
	Accept(ctx context.Context, userID uuid.UUID, tokenHash []byte, now time.Time) (ProjectSummary, error)
}

// InvitationMailer enfileira o e-mail do convite e volta sem esperar a rede.
type InvitationMailer interface {
	SendInvitation(ctx context.Context, e InvitationEmail) error
}

// newInvitationToken gera o token do link e o hash que vai para o banco: 32
// bytes de crypto/rand em base64url sem padding, e o SHA-256 dele (o mesmo de
// crypto.HashToken). O token nunca é gravado.
func newInvitationToken() (token string, hash []byte, err error) {
	cru := make([]byte, invitationTokenBytes)
	if _, err := rand.Read(cru); err != nil {
		return "", nil, fmt.Errorf("gerar token do convite: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(cru)
	return token, invitationHash(token), nil
}

// invitationHash é o SHA-256 do token, como crypto.HashToken.
func invitationHash(token string) []byte {
	soma := sha256.Sum256([]byte(token))
	return soma[:]
}

// parseInvitationToken confere que o token tem a forma dos gerados (32 bytes
// em base64url sem padding) e devolve o hash; senão, ErrInvitationInvalid.
func parseInvitationToken(token string) ([]byte, error) {
	cru, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(cru) != invitationTokenBytes {
		return nil, ErrInvitationInvalid
	}
	return invitationHash(token), nil
}
