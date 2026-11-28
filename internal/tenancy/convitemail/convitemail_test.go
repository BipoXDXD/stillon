package convitemail_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/convitemail"
)

// envioFalso guarda a última mensagem enfileirada.
type envioFalso struct {
	para, assunto, corpo string
	entrega              account.EntregaDeEmail
	chamadas             int
}

func (e *envioFalso) SendAccountEmail(_ context.Context, to, subject, body string, entrega account.EntregaDeEmail) error {
	e.para, e.assunto, e.corpo, e.entrega = to, subject, body, entrega
	e.chamadas++
	return nil
}

func novoUsuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// O e-mail sai para o convidado, com o link do token; o Preparar diz true com
// o convite pendente e false depois de revogado, sem tocar a rede.
func TestPrepareSkipsRevokedInvitation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t)
	pa := testdb.NovoProjeto(t, a, "Loja")
	token := testdb.Convite(t, pa, "b@y.com", "member")

	relogio := clock.NewFake(time.Now())
	envio := &envioFalso{}
	m := convitemail.New(pool, relogio, envio, account.Links{Base: "https://stillon.tech"})
	if err := m.SendInvitation(ctx, tenancy.InvitationEmail{
		To: "b@y.com", ProjectName: "Loja", InviterEmail: "a@y.com", InviterID: a, Role: tenancy.RoleMember,
		Token: token, TokenHash: crypto.HashToken(token),
	}); err != nil {
		t.Fatal(err)
	}
	if envio.entrega.Conta != a {
		t.Errorf("o convite foi com a conta %v, queria quem convida (%v)", envio.entrega.Conta, a)
	}
	if envio.chamadas != 1 || envio.para != "b@y.com" || !strings.Contains(envio.corpo, "https://stillon.tech/invite#token="+token+"\n") {
		t.Fatalf("enfileirado: para %q, corpo %q", envio.para, envio.corpo)
	}
	if envio.entrega.Preparar == nil || envio.entrega.Concluir != nil {
		t.Fatal("o convite tem de ter Preparar e nenhuma conclusão")
	}
	if ok, err := envio.entrega.Preparar(ctx); err != nil || !ok {
		t.Fatalf("Preparar com o convite pendente: %v, %v; queria true", ok, err)
	}

	if _, err := testdb.Admin(t).Exec(ctx, `DELETE FROM project_invitations WHERE project_id = $1`, pa); err != nil {
		t.Fatal(err)
	}
	if ok, err := envio.entrega.Preparar(ctx); err != nil || ok {
		t.Errorf("Preparar com o convite revogado: %v, %v; queria false sem erro", ok, err)
	}
}

// Vencido enquanto esperava na fila: também não sai.
func TestPrepareSkipsExpiredInvitation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t)
	pa := testdb.NovoProjeto(t, a, "Loja")
	token := testdb.Convite(t, pa, "b@y.com", "viewer")

	relogio := clock.NewFake(time.Now())
	envio := &envioFalso{}
	m := convitemail.New(pool, relogio, envio, account.Links{Base: "https://stillon.tech"})
	if err := m.SendInvitation(ctx, tenancy.InvitationEmail{
		To: "b@y.com", Role: tenancy.RoleViewer, Token: token, TokenHash: crypto.HashToken(token),
	}); err != nil {
		t.Fatal(err)
	}
	relogio.Advance(tenancy.InvitationTTL + time.Minute)
	if ok, err := envio.entrega.Preparar(ctx); err != nil || ok {
		t.Errorf("Preparar com o convite vencido: %v, %v; queria false sem erro", ok, err)
	}
}
