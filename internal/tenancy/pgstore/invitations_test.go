package pgstore_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

// t0 é o relógio dos convites destes testes. Fica perto do relógio real: a
// política de aceite confere o prazo também com o now() do banco.
var t0 = time.Now().UTC().Truncate(time.Second)

// timeConvite é o projeto PA de A (titular), com M manager e V viewer: um
// membro além do titular conta para o teto (V também); são dois.
type timeConvite struct {
	a, m, v uuid.UUID
	pa      uuid.UUID
}

func novoTimeConvite(t *testing.T, pool *pgxpool.Pool) timeConvite {
	t.Helper()
	e := timeConvite{a: novoUsuario(t, pool), m: novoUsuario(t, pool), v: novoUsuario(t, pool)}
	e.pa = testdb.NovoProjeto(t, e.a, "Loja")
	testdb.Membro(t, e.pa, e.m, "manager")
	testdb.Membro(t, e.pa, e.v, "viewer")
	return e
}

// usuarioComEmail cria uma conta com o e-mail dado.
func usuarioComEmail(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, email); err != nil {
		t.Fatal(err)
	}
	return id
}

// novoTokenDeConvite gera um token como o do serviço e o hash dele.
func novoTokenDeConvite(t *testing.T) (string, []byte) {
	t.Helper()
	cru := make([]byte, 32)
	if _, err := rand.Read(cru); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(cru)
	h := sha256.Sum256([]byte(token))
	return token, h[:]
}

// convidar grava um convite com o relógio dado e devolve o hash do link.
func convidar(t *testing.T, st *pgstore.Invitations, ator, projeto uuid.UUID, email string, papel tenancy.Role, em time.Time) ([]byte, error) {
	t.Helper()
	_, hash := novoTokenDeConvite(t)
	_, _, err := st.Create(context.Background(), ator, projeto, tenancy.NewInvitation{
		ID: uuid.Must(uuid.NewV7()), Email: email, Role: papel, TokenHash: hash, Now: em,
	})
	return hash, err
}

func pendentes(t *testing.T, projeto uuid.UUID) int {
	t.Helper()
	return contar(t, `SELECT count(*) FROM project_invitations WHERE project_id = $1 AND accepted_at IS NULL`, projeto)
}

// Membros além do titular e pendentes válidos somam para o teto; o vencido
// não conta.
func TestPendingInvitationsCountTowardLimit(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool) // M e V: dois além do titular
	st := pgstore.NewInvitations(pool)

	if _, err := convidar(t, st, e.a, e.pa, "x1@y.com", tenancy.RoleMember, t0); err != nil {
		t.Fatalf("terceira vaga: %v", err)
	}
	if _, err := convidar(t, st, e.a, e.pa, "x2@y.com", tenancy.RoleMember, t0); !errors.Is(err, tenancy.ErrMemberLimit) {
		t.Fatalf("quarta vaga com um pendente: %v; queria ErrMemberLimit", err)
	}
	if n := pendentes(t, e.pa); n != 1 {
		t.Errorf("pendentes = %d; queria 1 (o recusado não grava)", n)
	}
	depois := t0.Add(tenancy.InvitationTTL + time.Second)
	if _, err := convidar(t, st, e.a, e.pa, "x2@y.com", tenancy.RoleMember, depois); err != nil {
		t.Errorf("com o pendente vencido: %v; queria sem erro", err)
	}
}

// Na última vaga, cinco convites simultâneos: exatamente um passa.
func TestConcurrentInvitesAtLimitOnlyOnePasses(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool) // duas vagas ocupadas, uma livre
	st := pgstore.NewInvitations(pool)

	const n = 5
	erros := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, erros[i] = convidar(t, st, e.a, e.pa, fmt.Sprintf("c%d@y.com", i), tenancy.RoleViewer, t0)
		})
	}
	wg.Wait()
	ok, limite := 0, 0
	for _, err := range erros {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, tenancy.ErrMemberLimit):
			limite++
		default:
			t.Errorf("erro inesperado: %v", err)
		}
	}
	if ok != 1 || limite != n-1 {
		t.Errorf("%d sucesso(s) e %d ErrMemberLimit; queria 1 e %d", ok, limite, n-1)
	}
	if p := pendentes(t, e.pa); p != 1 {
		t.Errorf("pendentes = %d; queria 1", p)
	}
}

// espiar diz se o hash ainda abre um convite pendente.
func espiar(t *testing.T, pool *pgxpool.Pool, hash []byte, em time.Time) error {
	t.Helper()
	ctx := context.Background()
	return storage.InInvitationTokenTx(ctx, pool, hash, func(q *db.Queries) error {
		_, err := q.PeekInvitation(ctx, db.PeekInvitationParams{TokenHash: hash, Now: em})
		return err
	})
}

// Reenviar ao mesmo e-mail troca o convite: fica um pendente, na mesma vaga,
// e o link antigo morre.
func TestReinviteSameEmailKeepsOneSlot(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	st := pgstore.NewInvitations(pool)

	antigo, err := convidar(t, st, e.a, e.pa, "x@y.com", tenancy.RoleMember, t0)
	if err != nil {
		t.Fatal(err)
	}
	novo, err := convidar(t, st, e.a, e.pa, "x@y.com", tenancy.RoleViewer, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("reenviar na última vaga: %v; queria sem erro", err)
	}
	if n := pendentes(t, e.pa); n != 1 {
		t.Errorf("pendentes = %d; queria 1", n)
	}
	if err := espiar(t, pool, antigo, t0); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("o link antigo ainda espia: %v; queria pgx.ErrNoRows", err)
	}
	if err := espiar(t, pool, novo, t0); err != nil {
		t.Errorf("o link novo não espia: %v", err)
	}
}

func TestInviteExistingMemberIsAlreadyMember(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	if _, err := convidar(t, pgstore.NewInvitations(pool), e.a, e.pa, emailDe(e.v), tenancy.RoleMember, t0); !errors.Is(err, tenancy.ErrAlreadyMember) {
		t.Errorf("convidar o e-mail de V: %v; queria ErrAlreadyMember", err)
	}
	if n := pendentes(t, e.pa); n != 0 {
		t.Errorf("pendentes = %d; queria 0", n)
	}
}

func TestOperatorTitularHasLargerMemberLimit(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a)
	st := pgstore.NewInvitations(pool)
	for i := range 3 {
		if _, err := convidar(t, st, e.a, e.pa, fmt.Sprintf("o%d@y.com", i), tenancy.RoleMember, t0); err != nil {
			t.Fatalf("convite %d de titular operador: %v", i+1, err)
		}
	}
}

// O manager convida member e viewer, nunca manager nem owner; o viewer nem
// convida nem lista.
func TestManagerCannotInviteOwner(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a) // folga no teto: o teste é de papel
	st := pgstore.NewInvitations(pool)
	for _, papel := range []tenancy.Role{tenancy.RoleOwner, tenancy.RoleManager} {
		if _, err := convidar(t, st, e.m, e.pa, "x@y.com", papel, t0); !errors.Is(err, tenancy.ErrForbidden) {
			t.Errorf("manager convidando %s: %v; queria ErrForbidden", papel, err)
		}
	}
	if n := pendentes(t, e.pa); n != 0 {
		t.Errorf("pendentes = %d; queria 0", n)
	}
	if _, err := convidar(t, st, e.m, e.pa, "x@y.com", tenancy.RoleMember, t0); err != nil {
		t.Errorf("manager convidando member: %v; queria sem erro", err)
	}
	if _, err := convidar(t, st, e.v, e.pa, "z@y.com", tenancy.RoleViewer, t0); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("viewer convidando: %v; queria ErrForbidden", err)
	}
}

func TestViewerCannotListInvitations(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	st := pgstore.NewInvitations(pool)
	if _, err := convidar(t, st, e.a, e.pa, "x@y.com", tenancy.RoleMember, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListPending(context.Background(), e.v, e.pa, t0); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("viewer listando: %v; queria ErrForbidden", err)
	}
	lista, err := st.ListPending(context.Background(), e.m, e.pa, t0)
	if err != nil || len(lista) != 1 || lista[0].Email != "x@y.com" || lista[0].Role != tenancy.RoleMember ||
		!lista[0].ExpiresAt.Equal(t0.Add(tenancy.InvitationTTL)) {
		t.Errorf("manager listando: %+v, erro %v", lista, err)
	}
	if lista, err := st.ListPending(context.Background(), e.m, e.pa, t0.Add(tenancy.InvitationTTL)); err != nil || len(lista) != 0 {
		t.Errorf("depois do prazo: %+v, erro %v; queria vazio", lista, err)
	}
	if _, err := st.ListPending(context.Background(), novoUsuario(t, pool), e.pa, t0); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("não membro listando: %v; queria ErrProjectNotFound", err)
	}
}

// Revogar pede o mesmo papel de convidar aquele papel: o manager revoga o
// convite de member, não o de owner; o viewer, nenhum.
func TestRevokeFollowsInvitePermission(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a)
	st := pgstore.NewInvitations(pool)
	ctx := context.Background()
	if _, err := convidar(t, st, e.a, e.pa, "dono@y.com", tenancy.RoleOwner, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := convidar(t, st, e.a, e.pa, "membro@y.com", tenancy.RoleMember, t0); err != nil {
		t.Fatal(err)
	}
	ids := map[string]uuid.UUID{}
	lista, err := st.ListPending(ctx, e.a, e.pa, t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range lista {
		ids[c.Email] = c.ID
	}

	if err := st.Revoke(ctx, e.v, e.pa, ids["membro@y.com"]); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("viewer revogando: %v; queria ErrForbidden", err)
	}
	if err := st.Revoke(ctx, e.m, e.pa, ids["dono@y.com"]); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("manager revogando o convite de owner: %v; queria ErrForbidden", err)
	}
	if n := pendentes(t, e.pa); n != 2 {
		t.Fatalf("pendentes = %d; queria 2 (a recusa não apaga)", n)
	}
	if err := st.Revoke(ctx, e.m, e.pa, ids["membro@y.com"]); err != nil {
		t.Errorf("manager revogando o convite de member: %v", err)
	}
	if err := st.Revoke(ctx, e.m, e.pa, ids["membro@y.com"]); !errors.Is(err, tenancy.ErrInvitationNotFound) {
		t.Errorf("revogar de novo: %v; queria ErrInvitationNotFound", err)
	}
	if n := pendentes(t, e.pa); n != 1 {
		t.Errorf("pendentes = %d; queria 1", n)
	}
}

// esperarTravaDosMembros segura a trava stillon.members do projeto numa
// transação de superusuário, roda mudar nela e devolve a função que comita.
func esperarTravaDosMembros(t *testing.T, projeto uuid.UUID, mudar string, args ...any) (comitar func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := testdb.Admin(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stillon.members:' || $1::uuid::text, 0))`, projeto); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, mudar, args...); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// O papel de quem convida e de quem revoga vale o de depois da trava: o
// manager rebaixado enquanto esperava não convida nem revoga.
func TestDemotedInviterRefusedAfterWaitingForLock(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a) // folga no teto: o teste é de papel
	st := pgstore.NewInvitations(pool)
	if _, err := convidar(t, st, e.a, e.pa, "x@y.com", tenancy.RoleMember, t0); err != nil {
		t.Fatal(err)
	}
	lista, err := st.ListPending(ctx, e.a, e.pa, t0)
	if err != nil || len(lista) != 1 {
		t.Fatalf("lista = %+v, erro %v", lista, err)
	}

	for _, caso := range []struct {
		nome string
		fn   func() error
	}{
		{"convidar", func() error {
			_, err := convidar(t, st, e.m, e.pa, "z@y.com", tenancy.RoleViewer, t0)
			return err
		}},
		{"revogar", func() error { return st.Revoke(ctx, e.m, e.pa, lista[0].ID) }},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			if _, err := testdb.Admin(t).Exec(ctx,
				`UPDATE project_members SET role = 'manager' WHERE project_id = $1 AND user_id = $2`, e.pa, e.m); err != nil {
				t.Fatal(err)
			}
			comitar := esperarTravaDosMembros(t, e.pa,
				`UPDATE project_members SET role = 'viewer' WHERE project_id = $1 AND user_id = $2`, e.pa, e.m)
			feito := make(chan error, 1)
			go func() { feito <- caso.fn() }()
			esperarAlguemNaTrava(t, testdb.Admin(t))
			comitar()
			select {
			case err := <-feito:
				if !errors.Is(err, tenancy.ErrForbidden) {
					t.Errorf("o rebaixado agiu com o papel antigo: %v; queria ErrForbidden", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("a mutação não terminou depois do commit")
			}
		})
	}
	if n := pendentes(t, e.pa); n != 1 {
		t.Errorf("pendentes = %d; queria 1 (só o do titular)", n)
	}
}

// aceitarConviteSetup cria um convite de A para email com o papel dado e devolve
// o token do link.
func aceitarConviteSetup(t *testing.T, pool *pgxpool.Pool, e timeConvite, email string, papel tenancy.Role) (token string, hash []byte) {
	t.Helper()
	token, hash = novoTokenDeConvite(t)
	if _, _, err := pgstore.NewInvitations(pool).Create(context.Background(), e.a, e.pa, tenancy.NewInvitation{
		ID: uuid.Must(uuid.NewV7()), Email: email, Role: papel, TokenHash: hash, Now: t0,
	}); err != nil {
		t.Fatal(err)
	}
	return token, hash
}

// Cinco aceites simultâneos do mesmo link: um vence, os outros dão convite
// inválido, e o vínculo é um só.
func TestConcurrentAcceptOnlyOneWins(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	b := usuarioComEmail(t, pool, "b@y.com")
	_, hash := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleMember)
	st := pgstore.NewInvitations(pool)

	const n = 5
	resumos := make([]tenancy.ProjectSummary, n)
	erros := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { resumos[i], erros[i] = st.Accept(context.Background(), b, hash, t0.Add(time.Hour)) })
	}
	wg.Wait()
	ok, invalidos := 0, 0
	for i, err := range erros {
		switch {
		case err == nil:
			ok++
			if r := resumos[i]; r.ID != e.pa || r.Name != "Loja" || r.Role != tenancy.RoleMember || r.Limites.MembrosPorProjeto != 3 {
				t.Errorf("resumo do aceite = %+v", r)
			}
		case errors.Is(err, tenancy.ErrInvitationInvalid):
			invalidos++
		default:
			t.Errorf("erro inesperado: %v", err)
		}
	}
	if ok != 1 || invalidos != n-1 {
		t.Errorf("%d aceite(s) e %d inválido(s); queria 1 e %d", ok, invalidos, n-1)
	}
	if v := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'member'`, e.pa, b); v != 1 {
		t.Errorf("vínculos de B = %d; queria 1", v)
	}
}

// Conta com outro e-mail: erro próprio, e o convite continua pendente.
func TestAcceptOtherAccount(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	c := usuarioComEmail(t, pool, "c@y.com")
	_, hash := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleMember)
	if _, err := pgstore.NewInvitations(pool).Accept(context.Background(), c, hash, t0); !errors.Is(err, tenancy.ErrInvitationOtherAccount) {
		t.Errorf("C aceitando o convite de b@y.com: %v; queria ErrInvitationOtherAccount", err)
	}
	if n := pendentes(t, e.pa); n != 1 {
		t.Errorf("pendentes = %d; queria 1", n)
	}
	if v := contar(t, `SELECT count(*) FROM project_members WHERE user_id = $1 AND project_id = $2`, c, e.pa); v != 0 {
		t.Errorf("C entrou no projeto")
	}
}

// A caixa do e-mail da conta não importa: o convite é do endereço normalizado.
func TestAcceptIgnoresEmailCase(t *testing.T) {
	pool := testdb.New(t)
	e := novoTimeConvite(t, pool)
	b := usuarioComEmail(t, pool, "B@Y.com")
	_, hash := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleViewer)
	if _, err := pgstore.NewInvitations(pool).Accept(context.Background(), b, hash, t0); err != nil {
		t.Errorf("aceite com o e-mail em outra caixa: %v", err)
	}
}

func TestRevokedOrExpiredInvitationIsTokenInvalid(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novoTimeConvite(t, pool)
	b := usuarioComEmail(t, pool, "b@y.com")
	st := pgstore.NewInvitations(pool)

	_, vencido := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleMember)
	if _, err := st.Accept(ctx, b, vencido, t0.Add(tenancy.InvitationTTL)); !errors.Is(err, tenancy.ErrInvitationInvalid) {
		t.Errorf("aceite no fim do prazo: %v; queria ErrInvitationInvalid", err)
	}

	_, revogado := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleMember)
	lista, err := st.ListPending(ctx, e.a, e.pa, t0)
	if err != nil || len(lista) != 1 {
		t.Fatalf("lista = %+v, erro %v", lista, err)
	}
	if err := st.Revoke(ctx, e.a, e.pa, lista[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Accept(ctx, b, revogado, t0); !errors.Is(err, tenancy.ErrInvitationInvalid) {
		t.Errorf("aceite do revogado: %v; queria ErrInvitationInvalid", err)
	}
	if v := contar(t, `SELECT count(*) FROM project_members WHERE user_id = $1`, b); v != 0 {
		t.Errorf("B tem %d vínculo(s); queria 0", v)
	}
}

// Quem aceitou e foi removido não volta sozinho com o link antigo: a remoção
// apaga o convite aceito, e a política de INSERT por convite o recusa.
func TestAcceptedInvitationCannotReinsertRemovedMember(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novoTimeConvite(t, pool)
	b := usuarioComEmail(t, pool, "b@y.com")
	_, hash := aceitarConviteSetup(t, pool, e, "b@y.com", tenancy.RoleMember)
	if _, err := pgstore.NewInvitations(pool).Accept(ctx, b, hash, t0); err != nil {
		t.Fatal(err)
	}
	if err := pgstore.New(pool).RemoveMember(ctx, e.a, e.pa, b); err != nil {
		t.Fatal(err)
	}
	err := storage.InInvitationAcceptTx(ctx, pool, b, hash, func(q *db.Queries) error {
		return q.InsertMemberFromInvitation(ctx, db.InsertMemberFromInvitationParams{
			ProjectID: e.pa, UserID: b, Role: "member", Now: t0,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o removido voltando com o link antigo: %v; queria 42501", err)
	}
}

// O owner removido leva junto os convites pendentes que mandou: o link do
// cúmplice convidado como owner morre na mesma transação. Os convites de
// outros ficam.
func TestRemovingInviterDropsTheirPendingInvitations(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a) // folga no teto: o teste é de quem convidou
	o := novoUsuario(t, pool)
	testdb.Membro(t, e.pa, o, "owner")
	st := pgstore.NewInvitations(pool)
	cumplice := usuarioComEmail(t, pool, "cumplice@y.com")
	_, doRemovido := aceitarConviteDe(t, pool, o, e.pa, "cumplice@y.com", tenancy.RoleOwner)
	doTitular, err := convidar(t, st, e.a, e.pa, "outro@y.com", tenancy.RoleMember, t0)
	if err != nil {
		t.Fatal(err)
	}

	if err := pgstore.New(pool).RemoveMember(ctx, e.a, e.pa, o); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Accept(ctx, cumplice, doRemovido, t0.Add(time.Hour)); !errors.Is(err, tenancy.ErrInvitationInvalid) {
		t.Errorf("aceite do convite do owner removido: %v; queria ErrInvitationInvalid", err)
	}
	if err := espiar(t, pool, doTitular, t0); err != nil {
		t.Errorf("o convite do titular sumiu junto: %v", err)
	}
}

// Rebaixar quem convidou apaga os pendentes que o papel novo não poderia
// mandar (AuthorizeInvite): a manager perde os de owner e manager e fica com
// os de member e viewer; a viewer, perde todos.
func TestDemotingInviterDropsInvitationsTheyCouldNoLongerSend(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novoTimeConvite(t, pool)
	testdb.Operador(t, e.a)
	o := novoUsuario(t, pool)
	testdb.Membro(t, e.pa, o, "owner")
	links := map[tenancy.Role][]byte{}
	for _, papel := range []tenancy.Role{tenancy.RoleOwner, tenancy.RoleManager, tenancy.RoleMember, tenancy.RoleViewer} {
		_, links[papel] = aceitarConviteDe(t, pool, o, e.pa, string(papel)+"@y.com", papel)
	}
	vivos := func() map[tenancy.Role]bool {
		out := map[tenancy.Role]bool{}
		for papel, hash := range links {
			out[papel] = espiar(t, pool, hash, t0) == nil
		}
		return out
	}
	membros := pgstore.New(pool)

	if _, err := membros.ChangeRole(ctx, e.a, e.pa, o, tenancy.RoleManager); err != nil {
		t.Fatal(err)
	}
	queria := map[tenancy.Role]bool{tenancy.RoleOwner: false, tenancy.RoleManager: false, tenancy.RoleMember: true, tenancy.RoleViewer: true}
	if got := vivos(); !maps.Equal(got, queria) {
		t.Errorf("pendentes vivos depois de rebaixar a manager = %v; queria %v", got, queria)
	}
	if _, err := membros.ChangeRole(ctx, e.a, e.pa, o, tenancy.RoleViewer); err != nil {
		t.Fatal(err)
	}
	queria = map[tenancy.Role]bool{tenancy.RoleOwner: false, tenancy.RoleManager: false, tenancy.RoleMember: false, tenancy.RoleViewer: false}
	if got := vivos(); !maps.Equal(got, queria) {
		t.Errorf("pendentes vivos depois de rebaixar a viewer = %v; queria %v", got, queria)
	}
}

// aceitarConviteDe grava um convite de quem, para email com o papel dado, e
// devolve o token e o hash do link.
func aceitarConviteDe(t *testing.T, pool *pgxpool.Pool, quem, projeto uuid.UUID, email string, papel tenancy.Role) (token string, hash []byte) {
	t.Helper()
	token, hash = novoTokenDeConvite(t)
	if _, _, err := pgstore.NewInvitations(pool).Create(context.Background(), quem, projeto, tenancy.NewInvitation{
		ID: uuid.Must(uuid.NewV7()), Email: email, Role: papel, TokenHash: hash, Now: t0,
	}); err != nil {
		t.Fatal(err)
	}
	return token, hash
}
