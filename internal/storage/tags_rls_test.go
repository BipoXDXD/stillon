package storage_test

import (
	"context"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As tags são visíveis só através do alvo: a query filtra por user_id e a
// política filtra de novo, e cada barreira vale sozinha.
// No escopo do projeto, vermelho sem a comparação de projeto em
// target_tags_do_projeto, targets_do_projeto e targets_so_projeto_do_escopo.
func TestTagsSaoVisiveisSoAtravesDoAlvo(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.ProjetoPadrao(t, b)
	alvoA := novoAlvo(t, pool, a, agora)

	err := emProjetoTx(t, pool, a, func(q *db.Queries) error {
		return q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"prod", "api"}, ProjectID: pa, TargetID: alvoA})
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := contarNoProjeto(t, pool, a, "target_tags"); n != 2 {
		t.Errorf("sob o escopo de A, %d tags; queria 2", n)
	}
	if n := contarNoProjeto(t, pool, b, "target_tags"); n != 0 {
		t.Errorf("sob o escopo de B, SELECT sem WHERE viu %d tags de A", n)
	}

	// A política sozinha: o WHERE pede o projeto certo, mas o escopo é o de B.
	err = emProjetoTx(t, pool, b, func(q *db.Queries) error {
		daA, err := q.ListProjectTargetTags(ctx, pa)
		if err != nil {
			return err
		}
		if len(daA) != 0 {
			t.Errorf("sob o escopo de B, listar as tags de A devolveu %d", len(daA))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// O filtro sozinho: a política deixa A ver as próprias tags, e o WHERE
	// pede as do projeto de B, que A não tem.
	err = emProjetoTx(t, pool, a, func(q *db.Queries) error {
		deB, err := q.ListProjectTargetTags(ctx, pb)
		if err != nil {
			return err
		}
		if len(deB) != 0 {
			t.Errorf("sob o escopo de A, pedir as tags de B devolveu %d", len(deB))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// O INSERT cru, sem o filtro da query: só a política pode barrar.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	escopoDoProjetoDe(t, pool, tx, b)
	_, err = tx.Exec(ctx, `INSERT INTO target_tags (target_id, tag) VALUES ($1, 'invadida')`, alvoA)
	if codigoPG(err) != "42501" {
		t.Errorf("B inserir tag no alvo de A deu %v, queria 42501", err)
	}
}

func TestCheckDaTagRecusaOQueOCodigoNaoDeixariaPassar(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, a)
	alvo := novoAlvo(t, pool, a, agora)

	for _, tag := range []string{"", "a b", "a,b", "a\nb", "aaaaaaaaaaaaaaaaaaaaaaaaa"} {
		err := emProjetoTx(t, pool, a, func(q *db.Queries) error {
			return q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{tag}, ProjectID: testdb.ProjetoDoAlvo(t, alvo), TargetID: alvo})
		})
		if codigoPG(err) != "23514" {
			t.Errorf("tag %q: %v; queria violação de CHECK (23514)", tag, err)
		}
	}
}

func TestApagarOAlvoLevaAsTags(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, a)
	alvo := novoAlvo(t, pool, a, agora)
	err := emProjetoTx(t, pool, a, func(q *db.Queries) error {
		if err := q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"prod"}, ProjectID: testdb.ProjetoDoAlvo(t, alvo), TargetID: alvo}); err != nil {
			return err
		}
		n, err := q.DeleteTarget(ctx, db.DeleteTargetParams{ProjectID: testdb.ProjetoDoAlvo(t, alvo), ID: alvo})
		if n != 1 {
			t.Errorf("apagou %d alvos", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := contarNoProjeto(t, pool, a, "target_tags"); n != 0 {
		t.Errorf("sobraram %d tags do alvo apagado", n)
	}
}
