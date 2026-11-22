package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// CountTargets filtra só por user_id: U tem um alvo em A e outro em B, e ver
// 1 dentro de InProjectTx(U, A) prova que app.project_id foi ligado.
func TestInProjectTxScopesUserAndProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, _ := doisProjetos(t, pool)

	var visto tenancy.ProjectScope
	err := storage.InProjectTx(ctx, pool, u, a, func(q *db.Queries, sc tenancy.ProjectScope) error {
		visto = sc
		n, err := q.CountTargets(ctx, u)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("no escopo do projeto A, CountTargets(U) deu %d, queria 1", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	quer := tenancy.ProjectScope{ProjectID: a, UserID: u, Role: tenancy.RoleOwner}
	if visto != quer {
		t.Errorf("escopo = %+v, queria %+v", visto, quer)
	}
}

// Não membro e projeto inexistente dão o MESMO erro: a API devolve 404 nos
// dois casos, sem revelar que o projeto existe. E fn não roda.
func TestInProjectTxRefusesNonMemberLikeMissingProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	v := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, v)

	casos := []struct {
		nome    string
		projeto uuid.UUID
	}{
		{"projeto de outro usuário", a},
		{"projeto inexistente", uuid.New()},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			chamou := false
			err := storage.InProjectTx(ctx, pool, v, c.projeto, func(*db.Queries, tenancy.ProjectScope) error {
				chamou = true
				return nil
			})
			if !errors.Is(err, tenancy.ErrProjectNotFound) {
				t.Errorf("err = %v, queria tenancy.ErrProjectNotFound", err)
			}
			if chamou {
				t.Error("fn rodou sem a pertença conferida")
			}
		})
	}
}

// O caso do titular suspenso não basta: ele é membro E titular, e a condição
// sobre o titular o recusaria mesmo sem a do membro. O membro comum suspenso,
// com o titular ativo, é o que prova a condição sobre a conta de quem pede.
func TestInProjectTxRefusesSuspendedMember(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	casos := []struct {
		nome   string
		semear func(t *testing.T) (quem, projeto uuid.UUID)
	}{
		{"titular suspenso", func(t *testing.T) (uuid.UUID, uuid.UUID) {
			u := novoUsuario(t, pool)
			a := testdb.ProjetoPadrao(t, u)
			testdb.Suspender(t, u)
			return u, a
		}},
		{"membro suspenso com o titular ativo", func(t *testing.T) (uuid.UUID, uuid.UUID) {
			u := novoUsuario(t, pool)
			a := testdb.ProjetoPadrao(t, u)
			m := novoUsuario(t, pool)
			testdb.Membro(t, a, m, "member")
			testdb.Suspender(t, m)
			return m, a
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			quem, projeto := c.semear(t)
			chamou := false
			err := storage.InProjectTx(ctx, pool, quem, projeto, func(*db.Queries, tenancy.ProjectScope) error {
				chamou = true
				return nil
			})
			if !errors.Is(err, tenancy.ErrProjectNotFound) {
				t.Errorf("conta suspensa: err = %v, queria tenancy.ErrProjectNotFound", err)
			}
			if chamou {
				t.Error("fn rodou para uma conta suspensa")
			}
		})
	}
}

// Enquanto o titular estiver suspenso, os outros membros também perdem o
// acesso ao projeto.
func TestInProjectTxRefusesMemberWhenTitularIsSuspended(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	m := novoUsuario(t, pool)
	testdb.Membro(t, a, m, "viewer")

	var papel tenancy.Role
	err := storage.InProjectTx(ctx, pool, m, a, func(_ *db.Queries, sc tenancy.ProjectScope) error {
		papel = sc.Role
		return nil
	})
	if err != nil {
		t.Fatalf("titular ativo: o membro não entrou: %v", err)
	}
	if papel != tenancy.RoleViewer {
		t.Errorf("titular ativo: papel = %q, queria %q", papel, tenancy.RoleViewer)
	}

	testdb.Suspender(t, u)
	chamou := false
	err = storage.InProjectTx(ctx, pool, m, a, func(*db.Queries, tenancy.ProjectScope) error {
		chamou = true
		return nil
	})
	if !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("titular suspenso: err = %v, queria tenancy.ErrProjectNotFound", err)
	}
	if chamou {
		t.Error("fn rodou com o titular suspenso")
	}
}

func TestInProjectTxRejectsNilIDs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	nada := func(*db.Queries, tenancy.ProjectScope) error {
		t.Error("fn rodou com id nulo")
		return nil
	}

	if err := storage.InProjectTx(ctx, pool, uuid.Nil, a, nada); !errors.Is(err, storage.ErrNoUser) {
		t.Errorf("usuário nulo: err = %v, queria storage.ErrNoUser", err)
	}
	if err := storage.InProjectTx(ctx, pool, u, uuid.Nil, nada); !errors.Is(err, storage.ErrNoProject) {
		t.Errorf("projeto nulo: err = %v, queria storage.ErrNoProject", err)
	}
}

// No molde de TestScopeSurvivesConnectionReuse: app.project_id é SET LOCAL e
// não pode vazar para a próxima transação na mesma conexão do pool. Se
// vazasse, um InUserTx seguinte veria só um projeto do usuário.
func TestProjectScopeSurvivesConnectionReuse(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, _ := doisProjetos(t, pool)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	err = storage.InProjectTx(ctx, conn, u, a, func(q *db.Queries, _ tenancy.ProjectScope) error {
		_, err := q.CountTargets(ctx, u)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A premissa do teste: se o Postgres um dia devolver NULL aqui, o teste
	// deixa de provar o caso da string vazia, e precisa dizer isso.
	var vazio bool
	if err := conn.QueryRow(ctx, `SELECT current_setting('app.project_id', true) = ''`).Scan(&vazio); err != nil {
		t.Fatal(err)
	}
	if !vazio {
		t.Fatal("premissa quebrada: app.project_id não ficou '' depois do SET LOCAL; revise este teste")
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&n); err != nil {
		t.Fatalf("consulta sem escopo numa conexão reusada falhou: %v", err)
	}
	if n != 0 {
		t.Errorf("sem escopo viu %d alvos", n)
	}

	err = storage.InUserTx(ctx, conn, u, func(q *db.Queries) error {
		total, err := q.CountTargets(ctx, u)
		if err != nil {
			return err
		}
		if total != 2 {
			t.Errorf("InUserTx depois de InProjectTx na mesma conexão viu %d alvos, queria 2", total)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InUserTx numa conexão reusada falhou: %v", err)
	}
}

// InProjectPgxTx é o caminho de InProjectTx com o pgx.Tx cru, para o SQL que
// não tem query gerada: liga app.user_id e app.project_id depois de conferir a
// pertença. Sem pertença, o mesmo erro de InProjectTx e fn não roda.
func TestInProjectPgxTxScopesProjectAfterMembership(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, _ := doisProjetos(t, pool)

	var usuario, projeto string
	var visto tenancy.ProjectScope
	err := storage.InProjectPgxTx(ctx, pool, u, a, func(tx pgx.Tx, sc tenancy.ProjectScope) error {
		visto = sc
		return tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.user_id', true), ''),
		                                coalesce(current_setting('app.project_id', true), '')`).Scan(&usuario, &projeto)
	})
	if err != nil {
		t.Fatal(err)
	}
	if usuario != u.String() || projeto != a.String() {
		t.Errorf("GUCs dentro de fn = (%q, %q), queria (%q, %q)", usuario, projeto, u, a)
	}
	if visto != (tenancy.ProjectScope{ProjectID: a, UserID: u, Role: tenancy.RoleOwner}) {
		t.Errorf("escopo entregue = %+v, queria owner de %s", visto, a)
	}

	estranho := novoUsuario(t, pool)
	rodou := false
	err = storage.InProjectPgxTx(ctx, pool, estranho, a, func(pgx.Tx, tenancy.ProjectScope) error {
		rodou = true
		return nil
	})
	if !errors.Is(err, tenancy.ErrProjectNotFound) || rodou {
		t.Errorf("sem pertença: err = %v, fn rodou = %v; queria tenancy.ErrProjectNotFound sem rodar fn", err, rodou)
	}
}
