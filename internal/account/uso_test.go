package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// emailsFalsos devolve contagens fixas e guarda a conta que pediram.
type emailsFalsos struct {
	global, daConta int
	pedida          uuid.UUID
}

func (e *emailsFalsos) EnviadosHoje(context.Context) (int, error) { return e.global, nil }

func (e *emailsFalsos) EnviadosHojePelaConta(_ context.Context, conta uuid.UUID) (int, error) {
	e.pedida = conta
	return e.daConta, nil
}

// servicoDeUso monta o serviço sem 2FA e sem chave de IP, como o CLI.
func servicoDeUso(t *testing.T) *account.Service {
	t.Helper()
	pool := testdb.New(t)
	return account.NewService(pool, nil, clock.NewFake(time.Now()), nil)
}

// usuarioDeUso insere uma conta comum com o Projeto padrão.
func usuarioDeUso(t *testing.T, email string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, email); err != nil {
		t.Fatal(err)
	}
	return id, testdb.ProjetoPadrao(t, id)
}

func somaDosAlvos(uso account.Uso) int64 {
	var n int64
	for _, p := range uso.Projetos {
		n += p.Alvos
	}
	return n
}

// Conta grátis acima do teto de alvos: os limites são os do plano, a soma
// passa de 20, e as duas contagens de e-mail vêm cada uma no seu campo.
func TestUsageMarksOverQuota(t *testing.T) {
	svc := servicoDeUso(t)
	u, padrao := usuarioDeUso(t, "x@exemplo.com")
	testdb.Recursos(t, u, padrao, 21, 0, 0)
	emails := &emailsFalsos{global: 17, daConta: 4}

	uso, err := svc.Usage(context.Background(), " x@exemplo.com ", emails)
	if err != nil {
		t.Fatal(err)
	}
	if uso.Limites != tenancy.LimitesDe(false) {
		t.Errorf("Limites = %+v; queria os do plano grátis %+v", uso.Limites, tenancy.LimitesDe(false))
	}
	if n := somaDosAlvos(uso); n != 21 {
		t.Errorf("a soma dos alvos deu %d; queria 21", n)
	}
	if uso.EmailsHojeGlobal != 17 || uso.EmailsHoje != 4 {
		t.Errorf("EmailsHojeGlobal %d e EmailsHoje %d; queria 17 e 4", uso.EmailsHojeGlobal, uso.EmailsHoje)
	}
	if emails.pedida != u {
		t.Errorf("a contagem da conta foi pedida para %s; queria %s", emails.pedida, u)
	}
	if uso.Operador || uso.SuspensaEm != nil {
		t.Errorf("Operador %v e SuspensaEm %v; queria false e nil", uso.Operador, uso.SuspensaEm)
	}
	if uso.Email != "x@exemplo.com" {
		t.Errorf("Email = %q; queria o e-mail sem os espaços", uso.Email)
	}
}

// O operador recebe os limites dele; a conta suspensa traz o instante e ainda
// lista os projetos.
func TestUsageOperatorAndSuspended(t *testing.T) {
	svc := servicoDeUso(t)
	o, _ := usuarioDeUso(t, "operador@exemplo.com")
	testdb.Operador(t, o)
	u, padrao := usuarioDeUso(t, "suspensa@exemplo.com")
	testdb.Suspender(t, u)

	uso, err := svc.Usage(context.Background(), "operador@exemplo.com", &emailsFalsos{})
	if err != nil {
		t.Fatal(err)
	}
	if !uso.Operador || uso.Limites != tenancy.LimitesDe(true) {
		t.Errorf("operador: Operador %v, Limites %+v; queria true e %+v", uso.Operador, uso.Limites, tenancy.LimitesDe(true))
	}

	uso, err = svc.Usage(context.Background(), "suspensa@exemplo.com", &emailsFalsos{})
	if err != nil {
		t.Fatal(err)
	}
	if uso.SuspensaEm == nil {
		t.Error("a conta suspensa voltou com SuspensaEm nil")
	}
	if len(uso.Projetos) != 1 || uso.Projetos[0].ID != padrao || !uso.Projetos[0].Padrao {
		t.Errorf("os projetos da conta suspensa são %+v; queria o padrão %s", uso.Projetos, padrao)
	}
}

func TestUsageUnknownEmail(t *testing.T) {
	svc := servicoDeUso(t)
	if _, err := svc.Usage(context.Background(), "ninguem@exemplo.com", &emailsFalsos{}); !errors.Is(err, account.ErrUserNotFound) {
		t.Errorf("Usage de e-mail sem conta deu %v; queria ErrUserNotFound", err)
	}
}
