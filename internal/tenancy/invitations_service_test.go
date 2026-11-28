package tenancy_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// repoDeConvitesFalso grava a ordem das chamadas em eventos, que divide com o
// mailer falso: assim o teste vê se o envio veio depois do Create.
type repoDeConvitesFalso struct {
	eventos   *[]string
	criarErr  error
	recebido  tenancy.NewInvitation
	aceitou   bool
	hashAceit []byte
}

func (r *repoDeConvitesFalso) Create(_ context.Context, _, projeto uuid.UUID, inv tenancy.NewInvitation) (tenancy.Invitation, tenancy.InvitationEmail, error) {
	*r.eventos = append(*r.eventos, "create")
	r.recebido = inv
	if r.criarErr != nil {
		return tenancy.Invitation{}, tenancy.InvitationEmail{}, r.criarErr
	}
	return tenancy.Invitation{ID: inv.ID, ProjectID: projeto, Email: inv.Email, Role: inv.Role,
			CreatedAt: inv.Now, ExpiresAt: inv.Now.Add(tenancy.InvitationTTL)},
		tenancy.InvitationEmail{ProjectName: "Loja", InviterEmail: "a@x.com"}, nil
}

func (r *repoDeConvitesFalso) ListPending(context.Context, uuid.UUID, uuid.UUID, time.Time) ([]tenancy.Invitation, error) {
	return nil, nil
}

func (r *repoDeConvitesFalso) Revoke(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *repoDeConvitesFalso) Accept(_ context.Context, _ uuid.UUID, hash []byte, _ time.Time) (tenancy.ProjectSummary, error) {
	r.aceitou = true
	r.hashAceit = hash
	return tenancy.ProjectSummary{}, nil
}

type mailerDeConviteFalso struct {
	eventos  *[]string
	recebido tenancy.InvitationEmail
	err      error
}

func (m *mailerDeConviteFalso) SendInvitation(_ context.Context, e tenancy.InvitationEmail) error {
	*m.eventos = append(*m.eventos, "send")
	m.recebido = e
	return m.err
}

func novoServicoDeConvites(t *testing.T) (*tenancy.Invitations, *repoDeConvitesFalso, *mailerDeConviteFalso) {
	t.Helper()
	var eventos []string
	repo := &repoDeConvitesFalso{eventos: &eventos}
	mailer := &mailerDeConviteFalso{eventos: &eventos}
	return tenancy.NewInvitations(repo, mailer, clock.NewFake(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))), repo, mailer
}

// O e-mail sai depois do Create, com o token cujo SHA-256 é o hash gravado, o
// destinatário e o papel do convite.
func TestInviteSendsAfterCommitWithToken(t *testing.T) {
	svc, repo, mailer := novoServicoDeConvites(t)
	inv, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "b@x.com", "member")
	if err != nil {
		t.Fatal(err)
	}
	if got := *repo.eventos; !slices.Equal(got, []string{"create", "send"}) {
		t.Fatalf("ordem das chamadas = %v; queria create e depois send", got)
	}
	soma := sha256.Sum256([]byte(mailer.recebido.Token))
	if mailer.recebido.Token == "" || string(soma[:]) != string(repo.recebido.TokenHash) {
		t.Errorf("o token enviado não é o do hash gravado")
	}
	if len(repo.recebido.TokenHash) != sha256.Size {
		t.Errorf("hash gravado com %d bytes; queria %d", len(repo.recebido.TokenHash), sha256.Size)
	}
	if mailer.recebido.To != "b@x.com" || mailer.recebido.Role != tenancy.RoleMember ||
		mailer.recebido.ProjectName != "Loja" || mailer.recebido.InviterEmail != "a@x.com" {
		t.Errorf("e-mail recebido = %+v", mailer.recebido)
	}
	if inv.Email != "b@x.com" || inv.Role != tenancy.RoleMember || !inv.ExpiresAt.Equal(inv.CreatedAt.Add(7*24*time.Hour)) {
		t.Errorf("convite devolvido = %+v", inv)
	}
	if strings.Contains(mailer.recebido.Token, "=") {
		t.Errorf("token %q com padding: tem de ser base64url sem padding", mailer.recebido.Token)
	}
}

// A coluna guarda o e-mail em caixa baixa (CHECK da 00049) e o aceite compara
// assim: o domínio baixa a caixa antes do repositório e do envio.
func TestInviteLowercasesEmail(t *testing.T) {
	svc, repo, mailer := novoServicoDeConvites(t)
	if _, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "Novo@Exemplo.com", "viewer"); err != nil {
		t.Fatal(err)
	}
	if repo.recebido.Email != "novo@exemplo.com" || mailer.recebido.To != "novo@exemplo.com" {
		t.Errorf("repositório recebeu %q e o envio %q; queria novo@exemplo.com nos dois",
			repo.recebido.Email, mailer.recebido.To)
	}
}

// Dois convites nunca dividem o token.
func TestInviteTokensAreFresh(t *testing.T) {
	svc, _, mailer := novoServicoDeConvites(t)
	vistos := map[string]bool{}
	for range 3 {
		if _, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "b@x.com", "viewer"); err != nil {
			t.Fatal(err)
		}
		if vistos[mailer.recebido.Token] {
			t.Fatalf("token repetido: %q", mailer.recebido.Token)
		}
		vistos[mailer.recebido.Token] = true
	}
}

// Sem convite gravado, nenhum e-mail.
func TestInviteDoesNotSendWhenRepoFails(t *testing.T) {
	svc, repo, _ := novoServicoDeConvites(t)
	repo.criarErr = tenancy.ErrMemberLimit
	if _, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "b@x.com", "member"); !errors.Is(err, tenancy.ErrMemberLimit) {
		t.Fatalf("erro = %v; queria ErrMemberLimit", err)
	}
	if got := *repo.eventos; !slices.Equal(got, []string{"create"}) {
		t.Errorf("chamadas = %v; queria só create", got)
	}
}

// A fila cheia ou fechada não muda a resposta: o convite foi gravado e pode
// ser reenviado; a resposta não pode depender do envio (enumeração).
func TestInviteIgnoresMailerFailure(t *testing.T) {
	svc, _, mailer := novoServicoDeConvites(t)
	mailer.err = errors.New("fila cheia")
	if _, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "b@x.com", "member"); err != nil {
		t.Errorf("erro = %v; queria nil", err)
	}
}

// Papel desconhecido não chega ao repositório.
func TestInviteUnknownRoleNeverReachesRepo(t *testing.T) {
	svc, repo, _ := novoServicoDeConvites(t)
	if _, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), "b@x.com", "admin"); !errors.Is(err, tenancy.ErrUnknownRole) {
		t.Fatalf("erro = %v; queria ErrUnknownRole", err)
	}
	if len(*repo.eventos) != 0 {
		t.Errorf("chamadas = %v; queria nenhuma", *repo.eventos)
	}
}

// Token vazio, que não decodifica ou que não tem 32 bytes não vai ao
// repositório; o válido vai com o SHA-256 dele.
func TestAcceptHashesTokenBeforeRepo(t *testing.T) {
	for _, token := range []string{"", "!!!", "AAAA", strings.Repeat("A", 43) + "=", strings.Repeat("A", 44)} {
		svc, repo, _ := novoServicoDeConvites(t)
		if _, err := svc.Accept(context.Background(), uuid.New(), token); !errors.Is(err, tenancy.ErrInvitationInvalid) {
			t.Errorf("token %q: erro %v; queria ErrInvitationInvalid", token, err)
		}
		if repo.aceitou {
			t.Errorf("token %q chegou ao repositório", token)
		}
	}
	svc, repo, _ := novoServicoDeConvites(t)
	valido := strings.Repeat("A", 43) // 32 bytes em base64url sem padding
	if _, err := svc.Accept(context.Background(), uuid.New(), valido); err != nil {
		t.Fatal(err)
	}
	soma := sha256.Sum256([]byte(valido))
	if !repo.aceitou || string(repo.hashAceit) != string(soma[:]) {
		t.Errorf("o repositório recebeu %x; queria o SHA-256 do token", repo.hashAceit)
	}
}

// O hash do link é o mesmo de crypto.HashToken: o banco e os outros tokens
// do StillOn falam a mesma língua.
func TestInvitationHashMatchesCryptoHashToken(t *testing.T) {
	for _, token := range []string{"", "abc", strings.Repeat("A", 43)} {
		if got, quer := tenancy.InvitationHashForTest(token), crypto.HashToken(token); string(got) != string(quer) {
			t.Errorf("token %q: %x; queria %x", token, got, quer)
		}
	}
}
