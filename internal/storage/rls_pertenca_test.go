package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// A migration que troca as políticas de dono pelas de pertença ao projeto, e
// a anterior a ela, achadas pelo nome.
const (
	sufixoPertenca     = "rls_por_pertenca_ao_projeto"
	sufixoAntesDaTroca = "guarda_notificacao_projeto"
)

// noEscopoDoProjeto roda fn com o pgx.Tx cru em storage.InProjectPgxTx: a
// conferência de pertença e o app.project_id de produção.
func noEscopoDoProjeto(t *testing.T, pool *pgxpool.Pool, usuario, projeto uuid.UUID, fn func(pgx.Tx) error) error {
	t.Helper()
	return storage.InProjectPgxTx(context.Background(), pool, usuario, projeto,
		func(tx pgx.Tx, _ tenancy.ProjectScope) error { return fn(tx) })
}

// contarNoEscopoDoProjeto roda um SELECT count(*) SEM WHERE no escopo do
// projeto: a consulta que "esqueceu o filtro", contida só pela RLS.
func contarNoEscopoDoProjeto(t *testing.T, pool *pgxpool.Pool, usuario, projeto uuid.UUID, tabela string) int {
	t.Helper()
	var n int
	err := noEscopoDoProjeto(t, pool, usuario, projeto, func(tx pgx.Tx) error {
		// tabela é sempre um literal do próprio teste.
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+tabela).Scan(&n)
	})
	if err != nil {
		t.Fatalf("contar %s no escopo do projeto %s como %s: %v", tabela, projeto, usuario, err)
	}
	return n
}

// alvosNoEscopoDoUsuario conta, em storage.InUserTx, os alvos que a RLS deixa
// o usuário ver com a query gerada.
func alvosNoEscopoDoUsuario(t *testing.T, pool *pgxpool.Pool, usuario uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := storage.InUserTx(ctx, pool, usuario, func(q *db.Queries) error {
		var err error
		n, err = q.CountTargets(ctx, usuario)
		return err
	})
	if err != nil {
		t.Fatalf("contar os alvos de %s no escopo de usuário: %v", usuario, err)
	}
	return n
}

// alvoCriadoNoProjeto grava, no escopo do projeto, um alvo criado por quem
// age: user_id é o criador, project_id o projeto.
func alvoCriadoNoProjeto(t *testing.T, pool *pgxpool.Pool, criador, projeto uuid.UUID) uuid.UUID {
	t.Helper()
	id := novoID()
	err := noEscopoDoProjeto(t, pool, criador, projeto, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `
			INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
			                     expected_status, enabled, next_run_at, created_at)
			VALUES ($1, $2, $3, 'do membro', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $4, $4)`,
			id, criador, projeto, agora)
		return err
	})
	if err != nil {
		t.Fatalf("criar alvo de %s no projeto %s: %v", criador, projeto, err)
	}
	return id
}

// O member enxerga cada linha que o titular criou no projeto e edita o que o
// papel permite: a política é por projeto, não por quem criou a linha.
func TestMemberSeesAndEditsRowsCreatedByTitular(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	r := semearProjeto(t, u, a, "a", "pagina-a")
	m := novoUsuario(t, pool)
	testdb.Membro(t, a, m, "member")

	for _, tabela := range tabelasDoProjeto {
		doTitular := contarNoEscopoDoProjeto(t, pool, u, a, tabela)
		if doTitular == 0 {
			t.Fatalf("o titular não vê nada em %s: o teste não prova o que o member vê", tabela)
		}
		if doMembro := contarNoEscopoDoProjeto(t, pool, m, a, tabela); doMembro != doTitular {
			t.Errorf("%s no escopo de A: o member vê %d linhas, o titular %d", tabela, doMembro, doTitular)
		}
	}

	for _, c := range []struct {
		nome, sql string
		id        uuid.UUID
	}{
		{"renomear o alvo", `UPDATE targets SET name = 'm' WHERE id = $1`, r.alvo},
		{"reconhecer o incidente do alvo", `UPDATE incidents SET ack_at = now() WHERE id = $1`, r.incAlvo},
		{"reconhecer o incidente do heartbeat", `UPDATE incidents SET ack_at = now() WHERE id = $1`, r.incHeartbeat},
		{"zerar a saúde do alvo", `UPDATE target_health SET consecutive_failures = 0, consecutive_successes = 0,
		                                                    streak_since = NULL WHERE target_id = $1`, r.alvo},
		{"apagar a tag do alvo", `DELETE FROM target_tags WHERE target_id = $1`, r.alvo},
		{"apagar o certificado do alvo", `DELETE FROM target_tls WHERE target_id = $1`, r.alvo},
		{"marcar o heartbeat", `INSERT INTO heartbeat_tags (heartbeat_id, tag) VALUES ($1, 'nova')`, r.heartbeat},
	} {
		t.Run(c.nome, func(t *testing.T) {
			var afetadas int64
			err := noEscopoDoProjeto(t, pool, m, a, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, c.sql, c.id)
				afetadas = tag.RowsAffected()
				return err
			})
			if err != nil || afetadas != 1 {
				t.Errorf("no escopo do member: %d linha(s) e erro %v; queria 1 linha", afetadas, err)
			}
		})
	}
}

// Sem app.project_id nenhuma política de recurso concede: o escopo só do
// usuário não vê linha de projeto nenhum, nem dos dois de que é titular, e
// não grava alvo, nem nomeando o projeto, nem deixando o gatilho preencher.
func TestUserScopeSeesNoProjectRows(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, _ := doisProjetos(t, pool)

	for _, tabela := range tabelasDoProjeto {
		if n := contarComEscopo(t, pool, u, tabela); n != 0 {
			t.Errorf("%s no escopo de usuário: %d linhas; queria 0", tabela, n)
		}
	}
	if n := alvosNoEscopoDoUsuario(t, pool, u); n != 0 {
		t.Errorf("InUserTx vê %d alvos; queria 0", n)
	}

	for nome, projeto := range map[string]*uuid.UUID{"sem project_id": nil, "com project_id de A": &a} {
		t.Run(nome, func(t *testing.T) {
			err := emEscopo(t, pool, "app.user_id", u.String(), func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
					                     expected_status, enabled, next_run_at, created_at)
					VALUES ($1, $2, $3, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $4, $4)`,
					novoID(), u, projeto, agora)
				return err
			})
			if codigoPG(err) != "42501" {
				t.Errorf("INSERT de alvo no escopo de usuário, %s: %v; queria 42501", nome, err)
			}
		})
	}
}

// A pertença é o que dá acesso: quem sai do projeto perde até os alvos que
// criou nele, e eles seguem no projeto para o titular.
func TestRemovedMemberLosesRowsTheyCreated(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	m := novoUsuario(t, pool)
	testdb.Membro(t, a, m, "member")
	alvo := alvoCriadoNoProjeto(t, pool, m, a)

	if _, err := testdb.Admin(t).Exec(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, a, m); err != nil {
		t.Fatalf("apagar o vínculo: %v", err)
	}

	if err := noEscopoDoProjeto(t, pool, m, a, func(pgx.Tx) error { return nil }); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("InProjectTx do ex-member: erro %v, queria tenancy.ErrProjectNotFound", err)
	}
	if n := alvosNoEscopoDoUsuario(t, pool, m); n != 0 {
		t.Errorf("o ex-member vê %d alvos no escopo de usuário; queria 0", n)
	}
	var n int
	err := noEscopoDoProjeto(t, pool, u, a, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM targets WHERE id = $1`, alvo).Scan(&n)
	})
	if err != nil || n != 1 {
		t.Errorf("o titular vê %d alvo(s) do ex-member (erro %v); queria 1", n, err)
	}
}

// Titular suspenso tira o projeto de todos os membros, e o escopo de usuário
// não é porta dos fundos: o member não vê nem o alvo que ele mesmo criou.
func TestSuspendedTitularHidesProjectFromMemberEvenInUserScope(t *testing.T) {
	pool := testdb.New(t)
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	semearProjeto(t, u, a, "a", "")
	m := novoUsuario(t, pool)
	testdb.Membro(t, a, m, "member")
	alvoCriadoNoProjeto(t, pool, m, a)

	testdb.Suspender(t, u)

	if err := noEscopoDoProjeto(t, pool, m, a, func(pgx.Tx) error { return nil }); !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("InProjectTx do member com o titular suspenso: erro %v, queria tenancy.ErrProjectNotFound", err)
	}
	if n := alvosNoEscopoDoUsuario(t, pool, m); n != 0 {
		t.Errorf("o member vê %d alvos no escopo de usuário; queria 0", n)
	}
}

// Nenhuma política das tabelas de projeto (nem das partições de checks)
// concede por app.user_id: uma que sobrasse seria acesso por quem criou a
// linha, sem a conferência de pertença de InProjectTx.
func TestNoProjectTablePolicyNamesAppUserID(t *testing.T) {
	pool := testdb.New(t)
	// Uma partição de checks, com as políticas que protect_checks_partition
	// copia da mãe, para a consulta ter partição a olhar.
	if _, err := pool.Exec(context.Background(), `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, p.polname,
		       coalesce(pg_get_expr(p.polqual, p.polrelid), ''),
		       coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '')
		  FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		 WHERE c.relname = ANY($1)
		    OR c.oid IN (SELECT inhrelid FROM pg_inherits WHERE inhparent = 'checks'::regclass)
		 ORDER BY 1, 2`, tabelasDoProjeto)
	if err != nil {
		t.Fatal(err)
	}
	type politica struct{ tabela, nome, using, check string }
	politicas, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (politica, error) {
		var p politica
		err := r.Scan(&p.tabela, &p.nome, &p.using, &p.check)
		return p, err
	})
	if err != nil {
		t.Fatal(err)
	}

	tabelas, particoes, porProjeto := map[string]bool{}, 0, 0
	for _, p := range politicas {
		tabelas[p.tabela] = true
		if strings.HasPrefix(p.tabela, "checks_") {
			particoes++
		}
		if strings.Contains(p.using+p.check, "app.project_id") {
			porProjeto++
		}
		if strings.Contains(p.using+p.check, "app.user_id") {
			t.Errorf("%s.%s cita app.user_id:\nUSING %s\nWITH CHECK %s", p.tabela, p.nome, p.using, p.check)
		}
	}
	// Sem ver as tabelas, as partições e as políticas por projeto, o teste
	// passaria sem olhar o catálogo.
	for _, nome := range tabelasDoProjeto {
		if !tabelas[nome] {
			t.Errorf("a consulta não achou política em %s", nome)
		}
	}
	if particoes == 0 {
		t.Error("a consulta não achou política em partição de checks")
	}
	if porProjeto == 0 {
		t.Error("a consulta não achou política que cite app.project_id")
	}
}

// O banco recusa por si a vitrine com alvo de outro projeto do mesmo titular:
// no escopo de A, o alvo de B não existe para o WITH CHECK dos itens.
func TestStatusPageItemRefusesTargetOfOtherProjectInDB(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, rb := doisProjetos(t, pool)
	var pagina uuid.UUID
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT id FROM status_pages WHERE slug = 'pagina-a'`).Scan(&pagina); err != nil {
		t.Fatal(err)
	}
	alvoDeA := alvoCriadoNoProjeto(t, pool, u, a)

	inserirItem := func(alvo uuid.UUID) error {
		return noEscopoDoProjeto(t, pool, u, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO status_page_items (status_page_id, target_id, display_name, position)
			                        VALUES ($1, $2, 'Serviço', 1)`, pagina, alvo)
			return err
		})
	}
	if err := inserirItem(rb.alvo); codigoPG(err) != "42501" {
		t.Errorf("item com o alvo de B na página de A: %v; queria 42501", err)
	}
	if err := inserirItem(alvoDeA); err != nil {
		t.Errorf("item com o alvo de A na página de A: %v; queria sem erro", err)
	}
}

// A migration faz o último passe do backfill antes de tirar as políticas de
// dono: a linha que o binário anterior gravou sem projeto (usuário sem
// Projeto padrão) entra no padrão criado e segue visível no escopo dele.
func TestPolicySwapBackfillsRowsWrittenBefore(t *testing.T) {
	ctx := context.Background()
	pool, admin, dsn := bancoNaVersao(t, versaoDaMigration(t, sufixoAntesDaTroca))
	x := novoUsuario(t, pool)
	alvo := novoAlvoDoBinarioAnterior(t, pool, x, agora)
	if n := contar(t, admin, `SELECT count(*) FROM targets WHERE id = $1 AND project_id IS NULL`, alvo); n != 1 {
		t.Fatalf("o alvo do binário anterior já nasceu com projeto (%d órfãos); o teste não prova o backfill", n)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versaoDaMigration(t, sufixoPertenca)); err != nil {
		t.Fatalf("subir a troca das políticas: %v", err)
	}

	var projetoDoAlvo, padrao *uuid.UUID
	if err := admin.QueryRow(ctx, `
		SELECT (SELECT project_id FROM targets WHERE id = $1),
		       (SELECT id FROM projects WHERE titular_id = $2 AND is_default)`, alvo, x).
		Scan(&projetoDoAlvo, &padrao); err != nil {
		t.Fatal(err)
	}
	if padrao == nil {
		t.Fatalf("o backfill não criou o Projeto padrão de %s", x)
	}
	if projetoDoAlvo == nil || *projetoDoAlvo != *padrao {
		t.Fatalf("project_id do alvo = %v; queria o Projeto padrão %s", projetoDoAlvo, *padrao)
	}
	var n int
	err := noEscopoDoProjeto(t, pool, x, *padrao, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM targets WHERE id = $1`, alvo).Scan(&n)
	})
	if err != nil || n != 1 {
		t.Errorf("no escopo do Projeto padrão de X: %d alvo(s) e erro %v; queria 1", n, err)
	}
}

// catalogoDaTroca é o que a migration muda, lido por fora da RLS: as
// políticas de public, a definição do guard de project_id e a função de
// backfill (definição e ACL), quando existe.
func catalogoDaTroca(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	rows, err := admin.Query(ctx, `
		SELECT c.relname || ' ' || p.polname || ' ' || p.polcmd::text || ' ' || p.polpermissive::text
		       || E'\n  USING ' || coalesce(pg_get_expr(p.polqual, p.polrelid), '-')
		       || E'\n  CHECK ' || coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '-')
		  FROM pg_policy p
		  JOIN pg_class c ON c.oid = p.polrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public'
		 ORDER BY c.relname, p.polname`)
	if err != nil {
		t.Fatal(err)
	}
	linhas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var guard, backfill string
	if err := admin.QueryRow(ctx, `
		SELECT pg_get_functiondef('guard_project_id_imutavel'::regproc),
		       coalesce((SELECT pg_get_functiondef(p.oid) || E'\nACL ' || coalesce(p.proacl::text, '-')
		                   FROM pg_proc p WHERE p.oid = to_regprocedure('backfill_projetos_padrao()')),
		                'sem backfill_projetos_padrao')`).Scan(&guard, &backfill); err != nil {
		t.Fatal(err)
	}
	return strings.Join(linhas, "\n") + "\n" + guard + "\n" + backfill
}

// O down devolve o catálogo da 00043 (políticas, guard e backfill) sem tocar
// numa linha: o binário anterior volta a ver pelos donos o que o novo gravou
// no projeto.
func TestPolicySwapDownRestoresCatalogAndKeepsRows(t *testing.T) {
	ctx := context.Background()
	anterior := versaoDaMigration(t, sufixoAntesDaTroca)
	pool, admin, dsn := bancoNaVersao(t, anterior)
	antes := catalogoDaTroca(t, admin)

	if err := storage.MigrateUpTo(ctx, dsn, versaoDaMigration(t, sufixoPertenca)); err != nil {
		t.Fatalf("subir a troca das políticas: %v", err)
	}
	if catalogoDaTroca(t, admin) == antes {
		t.Fatal("o catálogo não mudou com a migration: o teste não prova o down")
	}
	u := novoUsuario(t, pool)
	novoAlvo(t, pool, u, agora)
	novoAlvo(t, pool, u, agora)
	contagens := contagensDeRecursos(t, admin)

	if err := storage.MigrateDownTo(ctx, dsn, anterior); err != nil {
		t.Fatalf("descer até a %d: %v", anterior, err)
	}

	if depois := catalogoDaTroca(t, admin); depois != antes {
		t.Errorf("o catálogo depois do down difere do da %d:\n--- antes\n%s\n--- depois\n%s", anterior, antes, depois)
	}
	if depois := contagensDeRecursos(t, admin); !mapasIguais(contagens, depois) {
		t.Errorf("contagens mudaram no down: antes %v, depois %v", contagens, depois)
	}
	if n := alvosNoEscopoDoUsuario(t, pool, u); n != 2 {
		t.Errorf("depois do down InUserTx vê %d alvos; queria os 2 do dono", n)
	}
}
