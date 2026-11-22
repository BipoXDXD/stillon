package pgstore_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

func novoUsuario(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// contar lê por fora da RLS, como superusuário.
func contar(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Duas chamadas simultâneas para um usuário sem projeto criam um único
// Projeto padrão com um único vínculo, e as duas devolvem o mesmo id.
func TestEnsureDefaultCreatesOneProjectUnderRace(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	st := pgstore.New(pool)
	agora := time.Now()

	var (
		wg      sync.WaitGroup
		escopos [2]tenancy.ProjectScope
		erros   [2]error
	)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			escopos[i], erros[i] = st.EnsureDefault(ctx, u, agora)
		}()
	}
	wg.Wait()

	for i, err := range erros {
		if err != nil {
			t.Fatalf("EnsureDefault %d: %v", i, err)
		}
	}
	if escopos[0].ProjectID != escopos[1].ProjectID {
		t.Errorf("ids diferentes: %s e %s", escopos[0].ProjectID, escopos[1].ProjectID)
	}
	quer := tenancy.ProjectScope{ProjectID: escopos[0].ProjectID, UserID: u, Role: tenancy.RoleOwner}
	if escopos[0] != quer {
		t.Errorf("escopo = %+v, queria %+v", escopos[0], quer)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, u); n != 1 {
		t.Errorf("projetos = %d, queria 1", n)
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE user_id = $1`, u); n != 1 {
		t.Errorf("vínculos = %d, queria 1", n)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1 AND is_default AND name = $2`,
		u, tenancy.DefaultProjectName); n != 1 {
		t.Errorf("projeto padrão com o nome %q = %d, queria 1", tenancy.DefaultProjectName, n)
	}
}

func TestEnsureDefaultKeepsExistingDefault(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	existente := testdb.ProjetoPadrao(t, u)

	sc, err := pgstore.New(pool).EnsureDefault(context.Background(), u, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sc.ProjectID != existente {
		t.Errorf("ProjectID = %s, queria o existente %s", sc.ProjectID, existente)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, u); n != 1 {
		t.Errorf("projetos = %d, queria 1", n)
	}
}

// ligarGatilhosDeEscrita faz todo INSERT em projects e project_members falhar
// com "escrita no caminho quente". O cleanup é registrado antes de criar, para
// um teste que pare no meio não deixar gatilho para trás.
func ligarGatilhosDeEscrita(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin := testdb.Admin(t)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DROP TRIGGER IF EXISTS teste_sem_escrita ON projects`,
			`DROP TRIGGER IF EXISTS teste_sem_escrita ON project_members`,
			`DROP FUNCTION IF EXISTS teste_recusa_escrita()`,
		} {
			if _, err := admin.Exec(ctx, sql); err != nil {
				t.Errorf("remover gatilho de teste: %v", err)
			}
		}
	})
	for _, sql := range []string{
		`CREATE FUNCTION teste_recusa_escrita() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RAISE EXCEPTION 'escrita no caminho quente'; END $$`,
		`CREATE TRIGGER teste_sem_escrita BEFORE INSERT ON projects
		 FOR EACH ROW EXECUTE FUNCTION teste_recusa_escrita()`,
		`CREATE TRIGGER teste_sem_escrita BEFORE INSERT ON project_members
		 FOR EACH ROW EXECUTE FUNCTION teste_recusa_escrita()`,
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatalf("criar gatilho de teste: %v", err)
		}
	}
}

// Com o Projeto padrão já criado, EnsureDefault só lê: os gatilhos recusam
// qualquer INSERT e a chamada ainda devolve o escopo. O usuário sem projeto,
// com os mesmos gatilhos, dá erro: o caminho lento continua escrevendo.
func TestEnsureDefaultSkipsWritesWhenProjectExists(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	semProjeto := novoUsuario(t, pool)
	existente := testdb.ProjetoPadrao(t, u)
	ligarGatilhosDeEscrita(t)
	st := pgstore.New(pool)

	sc, err := st.EnsureDefault(ctx, u, time.Now())
	if err != nil {
		t.Fatalf("EnsureDefault com o padrão existente: %v", err)
	}
	quer := tenancy.ProjectScope{ProjectID: existente, UserID: u, Role: tenancy.RoleOwner}
	if sc != quer {
		t.Errorf("escopo = %+v, queria %+v", sc, quer)
	}

	if _, err := st.EnsureDefault(ctx, semProjeto, time.Now()); err == nil ||
		!strings.Contains(err.Error(), "escrita no caminho quente") {
		t.Errorf("EnsureDefault sem projeto = %v, queria o erro do gatilho", err)
	}
}

// Sem projeto e sem gatilhos, a primeira chamada cria o padrão com owner e a
// segunda devolve o mesmo id.
func TestEnsureDefaultStillCreatesOnFirstCall(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	st := pgstore.New(pool)

	primeiro, err := st.EnsureDefault(ctx, u, time.Now())
	if err != nil {
		t.Fatalf("primeira chamada: %v", err)
	}
	if primeiro.Role != tenancy.RoleOwner || primeiro.UserID != u || primeiro.ProjectID == uuid.Nil {
		t.Errorf("escopo = %+v, queria owner de um projeto novo", primeiro)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE id = $1 AND titular_id = $2 AND is_default`,
		primeiro.ProjectID, u); n != 1 {
		t.Errorf("projeto padrão gravado = %d, queria 1", n)
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'owner'`,
		primeiro.ProjectID, u); n != 1 {
		t.Errorf("vínculo owner = %d, queria 1", n)
	}

	segundo, err := st.EnsureDefault(ctx, u, time.Now())
	if err != nil {
		t.Fatalf("segunda chamada: %v", err)
	}
	if segundo.ProjectID != primeiro.ProjectID {
		t.Errorf("segunda chamada = %s, queria o mesmo %s", segundo.ProjectID, primeiro.ProjectID)
	}
}

func TestListForUserShowsOnlyOwnMemberships(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	v := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	b := testdb.NovoProjeto(t, v, "B de V")
	testdb.Membro(t, b, u, "viewer")
	testdb.ProjetoPadrao(t, v) // de V, sem U

	lista, err := pgstore.New(pool).ListForUser(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 2 {
		t.Fatalf("lista = %+v, queria 2 projetos", lista)
	}
	if lista[0].ID != a || !lista[0].IsDefault || lista[0].Role != tenancy.RoleOwner ||
		lista[0].Name != tenancy.DefaultProjectName || lista[0].CreatedAt.IsZero() {
		t.Errorf("primeiro = %+v, queria A padrão como owner", lista[0])
	}
	if lista[1].ID != b || lista[1].IsDefault || lista[1].Role != tenancy.RoleViewer || lista[1].Name != "B de V" {
		t.Errorf("segundo = %+v, queria B como viewer", lista[1])
	}
}

func TestListForUserHidesProjectsOfSuspendedTitular(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	v := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	b := testdb.NovoProjeto(t, v, "B de V")
	testdb.Membro(t, b, u, "member")
	testdb.Suspender(t, v)

	lista, err := pgstore.New(pool).ListForUser(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != a {
		t.Errorf("lista = %+v, queria só A (o titular de B está suspenso)", lista)
	}
}
