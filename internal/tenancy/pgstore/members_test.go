package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

// equipe é o projeto PA de A (titular), com B manager, C member e D viewer.
type equipe struct {
	a, b, c, d uuid.UUID
	pa         uuid.UUID
}

func novaEquipe(t *testing.T, pool *pgxpool.Pool) equipe {
	t.Helper()
	e := equipe{a: novoUsuario(t, pool), b: novoUsuario(t, pool), c: novoUsuario(t, pool), d: novoUsuario(t, pool)}
	e.pa = testdb.NovoProjeto(t, e.a, "Projeto A")
	testdb.Membro(t, e.pa, e.b, "manager")
	testdb.Membro(t, e.pa, e.c, "member")
	testdb.Membro(t, e.pa, e.d, "viewer")
	return e
}

func emailDe(u uuid.UUID) string { return u.String() + "@exemplo.com" }

func codigoPG(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestListMembersTitularFirst(t *testing.T) {
	pool := testdb.New(t)
	e := novaEquipe(t, pool)

	membros, err := pgstore.New(pool).ListMembers(context.Background(), e.d, e.pa)
	if err != nil {
		t.Fatal(err)
	}
	quer := []struct {
		id      uuid.UUID
		papel   tenancy.Role
		titular bool
	}{{e.a, tenancy.RoleOwner, true}, {e.b, tenancy.RoleManager, false}, {e.c, tenancy.RoleMember, false}, {e.d, tenancy.RoleViewer, false}}
	if len(membros) != len(quer) {
		t.Fatalf("membros = %+v, queria %d", membros, len(quer))
	}
	for i, q := range quer {
		m := membros[i]
		if m.UserID != q.id || m.Role != q.papel || m.IsTitular != q.titular || m.Email != emailDe(q.id) || m.JoinedAt.IsZero() {
			t.Errorf("membro %d = %+v, queria %s %s titular=%v", i, m, q.id, q.papel, q.titular)
		}
	}

	if _, err := pgstore.New(pool).ListMembers(context.Background(), novoUsuario(t, pool), e.pa); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("não membro listando: %v, queria ErrProjectNotFound", err)
	}
}

func TestChangeRoleRequiresOwnerInTx(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)
	st := pgstore.New(pool)

	if _, err := st.ChangeRole(ctx, e.b, e.pa, e.c, tenancy.RoleViewer); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("manager mudando papel: %v, queria ErrForbidden", err)
	}
	m, err := st.ChangeRole(ctx, e.a, e.pa, e.c, tenancy.RoleViewer)
	if err != nil || m.Role != tenancy.RoleViewer || m.UserID != e.c || m.Email != emailDe(e.c) {
		t.Errorf("owner mudando C para viewer: %+v, %v", m, err)
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'viewer'`, e.pa, e.c); n != 1 {
		t.Errorf("C não ficou viewer no banco")
	}
	if _, err := st.ChangeRole(ctx, e.a, e.pa, e.a, tenancy.RoleManager); !errors.Is(err, tenancy.ErrTitularProtected) {
		t.Errorf("titular se rebaixando: %v, queria ErrTitularProtected", err)
	}
	if _, err := st.ChangeRole(ctx, e.a, e.pa, uuid.New(), tenancy.RoleViewer); !errors.Is(err, tenancy.ErrNotMember) {
		t.Errorf("alvo inexistente: %v, queria ErrNotMember", err)
	}
}

// canal grava, por fora da RLS, um canal criado por dono no projeto.
func canal(t *testing.T, dono, projeto uuid.UUID, tipo string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, created_at)
		VALUES ($1, $2, $3, $4, '\x01', now(), now())`, id, dono, projeto, tipo); err != nil {
		t.Fatalf("criar canal %s: %v", tipo, err)
	}
	return id
}

func ligado(t *testing.T, canal uuid.UUID) bool {
	t.Helper()
	var v bool
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT enabled FROM notif_channels WHERE id = $1`, canal).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRemovedMemberEmailChannelsDisabled(t *testing.T) {
	pool := testdb.New(t)
	e := novaEquipe(t, pool)
	pc := testdb.NovoProjeto(t, e.c, "Projeto C")
	emailEmPA := canal(t, e.c, e.pa, "email")
	discordEmPA := canal(t, e.c, e.pa, "discord")
	emailEmPC := canal(t, e.c, pc, "email")
	emailDeBEmPA := canal(t, e.b, e.pa, "email")

	if err := pgstore.New(pool).RemoveMember(context.Background(), e.a, e.pa, e.c); err != nil {
		t.Fatal(err)
	}
	if ligado(t, emailEmPA) {
		t.Errorf("o canal de e-mail de C em PA segue ligado")
	}
	if !ligado(t, discordEmPA) {
		t.Errorf("o Discord de C em PA foi desligado; só os de e-mail param")
	}
	if !ligado(t, emailEmPC) {
		t.Errorf("o e-mail de C no projeto dele foi desligado")
	}
	if !ligado(t, emailDeBEmPA) {
		t.Errorf("o e-mail de B em PA foi desligado")
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2`, e.pa, e.c); n != 0 {
		t.Errorf("o vínculo de C segue em PA")
	}
}

func TestLeaveProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)
	st := pgstore.New(pool)

	if err := st.RemoveMember(ctx, e.d, e.pa, e.d); err != nil {
		t.Errorf("viewer saindo: %v", err)
	}
	if _, err := st.Membership(ctx, e.d, e.pa); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("Membership de quem saiu: %v, queria ErrProjectNotFound", err)
	}
	if err := st.RemoveMember(ctx, e.a, e.pa, e.a); !errors.Is(err, tenancy.ErrTitularProtected) {
		t.Errorf("titular saindo: %v, queria ErrTitularProtected", err)
	}
	if err := st.RemoveMember(ctx, e.b, e.pa, e.c); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("manager removendo member: %v, queria ErrForbidden", err)
	}
}

func TestRemovedMemberNextRequestIsNotFound(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)

	if err := pgstore.New(pool).RemoveMember(ctx, e.a, e.pa, e.c); err != nil {
		t.Fatal(err)
	}
	rodou := false
	err := storage.InProjectTx(ctx, pool, e.c, e.pa, func(*db.Queries, tenancy.ProjectScope) error {
		rodou = true
		return nil
	})
	if !errors.Is(err, tenancy.ErrProjectNotFound) || rodou {
		t.Errorf("pedido seguinte do removido: erro %v, fn rodou=%v; queria ErrProjectNotFound sem rodar", err, rodou)
	}
}

// O pedido que já estava em voo conclui, como na revogação de sessão: sem
// FOR SHARE em ActiveMembershipRole, a remoção não espera nem o derruba.
func TestRemovalLetsInFlightRequestFinish(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)

	dentro, retomar, fim := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	alvo := uuid.Must(uuid.NewV7())
	go func() {
		fim <- storage.InProjectPgxTx(ctx, pool, e.c, e.pa, func(tx pgx.Tx, _ tenancy.ProjectScope) error {
			close(dentro)
			<-retomar
			_, err := tx.Exec(ctx, `
				INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
				                     expected_status, enabled, next_run_at, created_at)
				VALUES ($1, $2, $3, 'em voo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, now(), now())`,
				alvo, e.c, e.pa)
			return err
		})
	}()
	<-dentro

	feito := make(chan error, 1)
	go func() { feito <- pgstore.New(pool).RemoveMember(ctx, e.a, e.pa, e.c) }()
	select {
	case err := <-feito:
		if err != nil {
			t.Fatalf("remover C com o pedido dele em voo: %v", err)
		}
	case <-time.After(10 * time.Second):
		close(retomar)
		t.Fatal("a remoção esperou o pedido em voo")
	}

	close(retomar)
	if err := <-fim; err != nil {
		t.Errorf("o pedido em voo não concluiu: %v", err)
	}
	if n := contar(t, `SELECT count(*) FROM targets WHERE id = $1`, alvo); n != 1 {
		t.Errorf("o alvo do pedido em voo não ficou gravado")
	}
	if _, err := pgstore.New(pool).Membership(ctx, e.c, e.pa); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("pedido seguinte: %v, queria ErrProjectNotFound", err)
	}
}

func TestRenameProjectOwnerOnly(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)
	st := pgstore.New(pool)

	if _, err := st.Rename(ctx, e.b, e.pa, "Do manager"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("manager renomeando: %v, queria ErrForbidden", err)
	}
	p, err := st.Rename(ctx, e.a, e.pa, "Novo nome")
	if err != nil || p.ID != e.pa || p.Name != "Novo nome" || p.IsDefault || p.Role != tenancy.RoleOwner || p.CreatedAt.IsZero() {
		t.Errorf("owner renomeando: %+v, %v", p, err)
	}

	padrao := testdb.ProjetoPadrao(t, e.a)
	p, err = st.Rename(ctx, e.a, padrao, "Meu padrão")
	if err != nil || !p.IsDefault || p.Name != "Meu padrão" {
		t.Errorf("renomear o padrão: %+v, %v; queria IsDefault preservado", p, err)
	}
	if _, err := st.Rename(ctx, novoUsuario(t, pool), e.pa, "x"); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("não membro renomeando: %v, queria ErrProjectNotFound", err)
	}
}

// A FK adiável é a segunda barreira: mesmo pulando o domínio, rebaixar o
// titular falha no commit.
func TestTitularDemotionBlockedByFKEvenIfDomainSkipped(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)

	err := storage.InProjectTx(ctx, pool, e.a, e.pa, func(q *db.Queries, _ tenancy.ProjectScope) error {
		return q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{ProjectID: e.pa, UserID: e.a, Role: "member"})
	})
	if codigoPG(err) != "23503" {
		t.Errorf("rebaixar o titular por fora do domínio: %v, queria 23503 no commit", err)
	}
}

// O papel de quem pede vale o de depois da trava: o PATCH de um owner que já
// passou pela conferência de pertença, e espera a trava enquanto o titular o
// rebaixa, é recusado com o papel novo, e não autorizado com o antigo.
func TestDemotedActorRefusedAfterWaitingForLock(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	e := novaEquipe(t, pool)
	b := novoUsuario(t, pool) // um segundo owner, não titular
	testdb.Membro(t, e.pa, b, "owner")

	admin := testdb.Admin(t)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stillon.members:' || $1::uuid::text, 0))`, e.pa); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE project_members SET role = 'viewer' WHERE project_id = $1 AND user_id = $2`, e.pa, b); err != nil {
		t.Fatal(err)
	}

	feito := make(chan error, 1)
	go func() {
		_, err := pgstore.New(pool).ChangeRole(ctx, b, e.pa, e.c, tenancy.RoleViewer)
		feito <- err
	}()
	esperarAlguemNaTrava(t, admin)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-feito:
		if !errors.Is(err, tenancy.ErrForbidden) {
			t.Errorf("o rebaixado mudou papel com o papel antigo: %v, queria ErrForbidden", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a mutação não terminou depois do commit")
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'member'`, e.pa, e.c); n != 1 {
		t.Errorf("C não é mais member: a mutação do rebaixado passou")
	}
}

// esperarAlguemNaTrava espera uma sessão aguardando um advisory lock.
func esperarAlguemNaTrava(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	for range 200 {
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("ninguém chegou a esperar a trava dos membros")
}
