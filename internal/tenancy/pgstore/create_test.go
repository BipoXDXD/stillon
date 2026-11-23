package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

// criarProjeto chama Store.Create com um id novo e devolve o resumo e o erro.
func criarProjeto(t *testing.T, st *pgstore.Store, titular uuid.UUID, nome string) (tenancy.ProjectSummary, error) {
	t.Helper()
	return st.Create(context.Background(), tenancy.NewProject{
		ID: uuid.Must(uuid.NewV7()), Name: nome, TitularID: titular, Now: time.Now(),
	})
}

// O projeto novo nasce com o titular como owner e is_default falso, e
// aparece na lista dele depois do padrão.
func TestCreateProjectRecordsOwnerInSameTransaction(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	padrao := testdb.ProjetoPadrao(t, u)
	st := pgstore.New(pool)

	resumo, err := criarProjeto(t, st, u, "Loja")
	if err != nil {
		t.Fatal(err)
	}
	p := resumo.ID
	if resumo.Name != "Loja" || resumo.IsDefault || resumo.Role != tenancy.RoleOwner || resumo.CreatedAt.IsZero() {
		t.Errorf("resumo = %+v, queria Loja, não padrão, owner", resumo)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE id = $1 AND titular_id = $2 AND NOT is_default`,
		p, u); n != 1 {
		t.Errorf("projeto gravado = %d, queria 1", n)
	}
	if n := contar(t, `SELECT count(*) FROM project_members WHERE project_id = $1 AND user_id = $2 AND role = 'owner'`,
		p, u); n != 1 {
		t.Errorf("vínculo owner = %d, queria 1", n)
	}

	lista, err := st.ListForUser(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 2 || lista[0].ID != padrao || lista[1].ID != p || lista[1].Role != tenancy.RoleOwner {
		t.Errorf("lista = %+v, queria o padrão e depois %s como owner", lista, p)
	}
}

// ligarGatilhoSemOwner faz o INSERT do vínculo de projeto falhar. O cleanup
// vem antes de criar, para um teste que pare no meio não deixar gatilho.
func ligarGatilhoSemOwner(t *testing.T, projeto uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	admin := testdb.Admin(t)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DROP TRIGGER IF EXISTS teste_sem_owner ON project_members`,
			`DROP FUNCTION IF EXISTS teste_recusa_owner()`,
		} {
			if _, err := admin.Exec(ctx, sql); err != nil {
				t.Errorf("remover gatilho de teste: %v", err)
			}
		}
	})
	// O id vem de uuid.UUID.String(): só hexadecimal e hífen no literal.
	for _, sql := range []string{
		fmt.Sprintf(`CREATE FUNCTION teste_recusa_owner() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.project_id = '%s'::uuid THEN RAISE EXCEPTION 'vínculo recusado'; END IF;
		   RETURN NEW;
		 END $$`, projeto),
		`CREATE TRIGGER teste_sem_owner BEFORE INSERT ON project_members
		 FOR EACH ROW EXECUTE FUNCTION teste_recusa_owner()`,
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatalf("criar gatilho de teste: %v", err)
		}
	}
}

// Se o vínculo owner falha, o projeto não fica: os dois INSERTs estão na
// mesma transação.
func TestCreateProjectRollsBackWithoutOwner(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, u)
	p := uuid.Must(uuid.NewV7())
	ligarGatilhoSemOwner(t, p)

	_, err := pgstore.New(pool).Create(context.Background(), tenancy.NewProject{
		ID: p, Name: "Sem owner", TitularID: u, Now: time.Now(),
	})
	if err == nil {
		t.Fatal("Create sem vínculo owner deu nil, queria erro")
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE id = $1`, p); n != 0 {
		t.Errorf("projetos com id %s = %d, queria 0 (sem owner, sem projeto)", p, n)
	}
}

func TestCreateProjectInvisibleToOtherUser(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	v := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, u)
	padraoV := testdb.ProjetoPadrao(t, v)
	st := pgstore.New(pool)

	resumo, err := criarProjeto(t, st, u, "Só de U")
	if err != nil {
		t.Fatal(err)
	}

	lista, err := st.ListForUser(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != padraoV {
		t.Errorf("lista de V = %+v, queria só o padrão dele", lista)
	}
	if _, err := st.Membership(context.Background(), v, resumo.ID); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("Membership(V, P) = %v, queria ErrProjectNotFound", err)
	}
}

func TestCreateProjectRespectsTitularLimit(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, u)
	st := pgstore.New(pool)
	for i := range tenancy.MaxProjectsPerTitular - 1 {
		if _, err := criarProjeto(t, st, u, fmt.Sprintf("Extra %d", i)); err != nil {
			t.Fatalf("projeto %d: %v", i+2, err)
		}
	}

	if _, err := criarProjeto(t, st, u, "Um a mais"); !errors.Is(err, tenancy.ErrProjectLimit) {
		t.Errorf("4º projeto = %v, queria ErrProjectLimit", err)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, u); n != tenancy.MaxProjectsPerTitular {
		t.Errorf("projetos = %d, queria %d", n, tenancy.MaxProjectsPerTitular)
	}
}

// Com dois projetos e cinco criações simultâneas, a trava do titular deixa
// passar exatamente uma: sem ela, todas contariam 2 e gravariam.
func TestCreateProjectLimitUnderRace(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, u)
	st := pgstore.New(pool)
	if _, err := criarProjeto(t, st, u, "Segundo"); err != nil {
		t.Fatal(err)
	}

	const corrida = 5
	var (
		wg    sync.WaitGroup
		erros [corrida]error
	)
	for i := range corrida {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = st.Create(context.Background(), tenancy.NewProject{
				ID: uuid.Must(uuid.NewV7()), Name: fmt.Sprintf("Corrida %d", i), TitularID: u, Now: time.Now(),
			})
		}()
	}
	wg.Wait()

	passaram := 0
	for i, err := range erros {
		switch {
		case err == nil:
			passaram++
		case !errors.Is(err, tenancy.ErrProjectLimit):
			t.Errorf("Create %d = %v, queria nil ou ErrProjectLimit", i, err)
		}
	}
	if passaram != 1 {
		t.Errorf("criações que passaram = %d, queria 1", passaram)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, u); n != tenancy.MaxProjectsPerTitular {
		t.Errorf("projetos = %d, queria %d", n, tenancy.MaxProjectsPerTitular)
	}
}

func TestOperatorProjectLimitIsHundred(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, u)
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`UPDATE users SET is_operator = true WHERE id = $1`, u); err != nil {
		t.Fatal(err)
	}
	st := pgstore.New(pool)
	for i := range tenancy.MaxProjectsPerTitular - 1 {
		if _, err := criarProjeto(t, st, u, fmt.Sprintf("Extra %d", i)); err != nil {
			t.Fatalf("projeto %d: %v", i+2, err)
		}
	}

	if _, err := criarProjeto(t, st, u, "Quarto do operador"); err != nil {
		t.Errorf("4º projeto do operador = %v, queria nil (teto %d)", err, tenancy.MaxProjectsForOperator)
	}
	if n := contar(t, `SELECT count(*) FROM projects WHERE titular_id = $1`, u); n != tenancy.MaxProjectsPerTitular+1 {
		t.Errorf("projetos = %d, queria %d", n, tenancy.MaxProjectsPerTitular+1)
	}
}
