package storage_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// guardaTx é um storage.Beginner que guarda a transação aberta: o teste roda
// SQL cru dentro de InUserTx e InProberTx, que só entregam as queries geradas.
type guardaTx struct {
	storage.Beginner
	tx pgx.Tx
}

func (g *guardaTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := g.Beginner.Begin(ctx)
	g.tx = tx
	return tx, err
}

// escopoDepoisDaCota lê, na transação, o app.project_id e os dois GUCs da
// cota. Os da cota só podem estar vazios fora das funções (o NULL de um GUC
// nunca ligado vira a string vazia).
func escopoDepoisDaCota(t *testing.T, tx pgx.Tx) (projeto, titular, projetos string) {
	t.Helper()
	if err := tx.QueryRow(context.Background(), `
		SELECT coalesce(current_setting('app.project_id', true), ''),
		       coalesce(current_setting('app.cota_titular_id', true), ''),
		       coalesce(current_setting('app.cota_projetos', true), '')`).Scan(&projeto, &titular, &projetos); err != nil {
		t.Fatalf("ler os GUCs depois da contagem de cota: %v", err)
	}
	return projeto, titular, projetos
}

// comRegra põe uma regra de conteúdo nos alvos dados, por fora da RLS.
func comRegra(t *testing.T, alvos ...uuid.UUID) {
	t.Helper()
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`UPDATE targets SET content_mode = 'contains', content_value = 'ok' WHERE id = ANY($1)`, alvos); err != nil {
		t.Fatalf("pôr regra de conteúdo: %v", err)
	}
}

// U é titular de A (dois de cada recurso) e de B (um de cada). No escopo de A,
// as funções somam os dois projetos; o SELECT seguinte, na mesma transação,
// volta a ver só A, com os GUCs da cota já restaurados.
func TestQuotaFunctionsCountEveryProjectOfTitular(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	b := testdb.NovoProjeto(t, u, "Projeto B")
	ra := semearProjeto(t, u, a, "a1", "")
	semearProjeto(t, u, a, "a2", "")
	rb := semearProjeto(t, u, b, "b1", "")
	comRegra(t, ra.alvo, rb.alvo)

	err := noEscopoDoProjeto(t, pool, u, a, func(tx pgx.Tx) error {
		var alvos, heartbeats, canais int64
		if err := tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1), cota_heartbeats_do_titular($1),
		                                   cota_canais_do_titular($1)`, a).Scan(&alvos, &heartbeats, &canais); err != nil {
			return err
		}
		for nome, n := range map[string]int64{"alvos": alvos, "heartbeats": heartbeats, "canais": canais} {
			if n != 3 {
				t.Errorf("a cota de %s no escopo de A deu %d, queria 3 (2 de A e 1 de B)", nome, n)
			}
		}

		linhas, err := tx.Query(ctx, `SELECT r.id FROM cota_urls_com_regra_do_titular($1) AS r`, a)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(linhas, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		quer := []uuid.UUID{ra.alvo, rb.alvo}
		slices.SortFunc(ids, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
		slices.SortFunc(quer, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
		if !slices.Equal(ids, quer) {
			t.Errorf("as URLs com regra do titular trouxeram %v, queria os alvos com regra de A e de B %v", ids, quer)
		}

		var soA int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&soA); err != nil {
			return err
		}
		if soA != 2 {
			t.Errorf("depois da contagem, SELECT count(*) FROM targets deu %d, queria só os 2 de A", soA)
		}
		projeto, titular, projetos := escopoDepoisDaCota(t, tx)
		if projeto != a.String() || titular != "" || projetos != "" {
			t.Errorf("depois da contagem: app.project_id %q, app.cota_titular_id %q, app.cota_projetos %q; "+
				"queria %q e os dois da cota vazios", projeto, titular, projetos, a)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A segunda barreira: o filtro por projetos do titular dentro das funções
// vale sem a RLS. O superusuário ignora as políticas (inclusive a leitura do
// titular e a recusa 42501, que dependem dela), então só o WHERE project_id =
// ANY (projetos) separa U (A com dois de cada recurso, B com um) de V (um de
// cada).
func TestQuotaFunctionsFilterTitularWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	admin := testdb.Admin(t)
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	b := testdb.NovoProjeto(t, u, "Projeto B")
	ra := semearProjeto(t, u, a, "a1", "")
	semearProjeto(t, u, a, "a2", "")
	rb := semearProjeto(t, u, b, "b1", "")
	v := novoUsuario(t, pool)
	pv := testdb.ProjetoPadrao(t, v)
	rv := semearProjeto(t, v, pv, "v1", "")
	comRegra(t, ra.alvo, rb.alvo, rv.alvo)

	for _, c := range []struct {
		nome    string
		projeto uuid.UUID
		n       int64
		urls    []uuid.UUID
	}{
		{"U", a, 3, []uuid.UUID{ra.alvo, rb.alvo}},
		{"V", pv, 1, []uuid.UUID{rv.alvo}},
	} {
		var alvos, heartbeats, canais int64
		if err := admin.QueryRow(ctx, `SELECT cota_alvos_do_titular($1), cota_heartbeats_do_titular($1),
		                                      cota_canais_do_titular($1)`, c.projeto).Scan(&alvos, &heartbeats, &canais); err != nil {
			t.Fatal(err)
		}
		for nome, n := range map[string]int64{"alvos": alvos, "heartbeats": heartbeats, "canais": canais} {
			if n != c.n {
				t.Errorf("sem RLS, a cota de %s de %s deu %d, queria %d", nome, c.nome, n, c.n)
			}
		}
		linhas, err := admin.Query(ctx, `SELECT r.id FROM cota_urls_com_regra_do_titular($1) AS r`, c.projeto)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := pgx.CollectRows(linhas, pgx.RowTo[uuid.UUID])
		if err != nil {
			t.Fatal(err)
		}
		porTexto := func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) }
		slices.SortFunc(ids, porTexto)
		slices.SortFunc(c.urls, porTexto)
		if !slices.Equal(ids, c.urls) {
			t.Errorf("sem RLS, as URLs com regra de %s trouxeram %v, queria %v", c.nome, ids, c.urls)
		}
	}
}

// Os alvos de outro titular (V) não entram na cota de U.
func TestQuotaFunctionDoesNotCountOtherTitulars(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	semearProjeto(t, u, a, "a", "")
	v := novoUsuario(t, pool)
	pv := testdb.ProjetoPadrao(t, v)
	semearProjeto(t, v, pv, "v1", "")
	semearProjeto(t, v, pv, "v2", "")

	var n int64
	err := noEscopoDoProjeto(t, pool, u, a, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1)`, a).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("a cota de U deu %d alvos, queria 1: os de V não podem entrar", n)
	}
}

// A função não conta um projeto que o escopo não enxerga: nem o de outro
// titular, nem nenhum no escopo do sondador (sem app.user_id).
func TestQuotaFunctionRefusesProjectOutsideScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	semearProjeto(t, u, a, "a", "")
	v := novoUsuario(t, pool)
	pv := testdb.ProjetoPadrao(t, v)

	contar := func(tx pgx.Tx) error {
		var n int64
		return tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1)`, a).Scan(&n)
	}
	if err := noEscopoDoProjeto(t, pool, v, pv, contar); codigoPG(err) != "42501" {
		t.Errorf("no escopo de V, a cota do projeto de U deu %v; queria 42501", err)
	}
	g := &guardaTx{Beginner: pool}
	err := storage.InProberTx(ctx, g, func(*db.Queries) error { return contar(g.tx) })
	if codigoPG(err) != "42501" {
		t.Errorf("no escopo do sondador, a cota do projeto de U deu %v; queria 42501", err)
	}
}

// A recusa (42501) não deixa o escopo da transação para trás: as funções não
// restauram nada no erro, quem desfaz os set_config é o abort do savepoint.
// Depois do ROLLBACK TO, ou do bloco EXCEPTION do plpgsql que engole o erro, o
// projeto continua A e os GUCs da cota, vazios.
func TestQuotaFunctionRestoresScopeAfterError(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u := novoUsuario(t, pool)
	a := testdb.ProjetoPadrao(t, u)
	v := novoUsuario(t, pool)
	pv := testdb.ProjetoPadrao(t, v)

	err := noEscopoDoProjeto(t, pool, u, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
			return err
		}
		var n int64
		if err := tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1)`, pv).Scan(&n); codigoPG(err) != "42501" {
			t.Errorf("a cota do projeto de V no escopo de U deu %d, %v; queria 42501", n, err)
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`); err != nil {
			return err
		}
		projeto, titular, projetos := escopoDepoisDaCota(t, tx)
		if projeto != a.String() || titular != "" || projetos != "" {
			t.Errorf("depois da recusa: app.project_id %q, app.cota_titular_id %q, app.cota_projetos %q; "+
				"queria %q e os dois da cota vazios", projeto, titular, projetos, a)
		}

		// pv é um uuid formatado pelo próprio teste: DO não aceita parâmetro.
		if _, err := tx.Exec(ctx, `DO $$ BEGIN PERFORM cota_alvos_do_titular('`+pv.String()+`');
			EXCEPTION WHEN insufficient_privilege THEN NULL; END $$`); err != nil {
			return err
		}
		projeto, titular, projetos = escopoDepoisDaCota(t, tx)
		if projeto != a.String() || titular != "" || projetos != "" {
			t.Errorf("depois da recusa engolida pelo EXCEPTION: app.project_id %q, app.cota_titular_id %q, "+
				"app.cota_projetos %q; queria %q e os dois da cota vazios", projeto, titular, projetos, a)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// O escopo de usuário pode contar a cota (o titular enxerga o projeto), mas a
// contagem não lhe abre linha nenhuma: na mesma transação, depois dela, ele
// continua sem ver recurso de projeto.
func TestInUserTxSeesNoResourcesEvenAfterQuotaCount(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, a, _, _, _ := doisProjetos(t, pool)

	g := &guardaTx{Beginner: pool}
	err := storage.InUserTx(ctx, g, u, func(*db.Queries) error {
		var n int64
		if err := g.tx.QueryRow(ctx, `SELECT cota_alvos_do_titular($1)`, a).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("a cota de U no escopo de usuário deu %d alvos, queria 2 (A e B)", n)
		}
		for _, tabela := range []string{"targets", "heartbeats", "notif_channels"} {
			var vistos int64
			// tabela é sempre um literal do próprio teste.
			if err := g.tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&vistos); err != nil {
				return err
			}
			if vistos != 0 {
				t.Errorf("depois da contagem, o escopo de usuário vê %d linhas de %s; queria 0", vistos, tabela)
			}
		}
		_, titular, projetos := escopoDepoisDaCota(t, g.tx)
		if titular != "" || projetos != "" {
			t.Errorf("depois da contagem: app.cota_titular_id %q, app.cota_projetos %q; queria vazios", titular, projetos)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// As políticas e funções que leem app.cota_* são só as da 00045: políticas de
// SELECT, e funções que só fixam search_path na cláusula SET e restauram os
// três GUCs no corpo (app.project_id ao valor guardado, os da cota vazios).
func TestQuotaPoliciesAndFunctionsAreContained(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	linhas, err := pool.Query(ctx, `
		SELECT pol.polname, pol.polcmd::text
		  FROM pg_policy pol
		 WHERE strpos(coalesce(pg_get_expr(pol.polqual, pol.polrelid), ''), 'app.cota_') > 0
		    OR strpos(coalesce(pg_get_expr(pol.polwithcheck, pol.polrelid), ''), 'app.cota_') > 0
		 ORDER BY pol.polname`)
	if err != nil {
		t.Fatal(err)
	}
	type politica struct{ nome, cmd string }
	politicas, err := pgx.CollectRows(linhas, func(r pgx.CollectableRow) (politica, error) {
		var p politica
		return p, r.Scan(&p.nome, &p.cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	var nomes []string
	for _, p := range politicas {
		nomes = append(nomes, p.nome)
		if p.cmd != "r" {
			t.Errorf("a política %s cita app.cota_ com polcmd %q; queria só SELECT ('r')", p.nome, p.cmd)
		}
	}
	quer := []string{"heartbeats_cota_select", "notif_channels_cota_select", "projects_cota_select", "targets_cota_select"}
	if !slices.Equal(nomes, quer) {
		t.Errorf("políticas que citam app.cota_: %v; queria exatamente %v", nomes, quer)
	}

	linhas, err = pool.Query(ctx, `
		SELECT p.proname, coalesce(p.proconfig, '{}'), p.prosrc
		  FROM pg_proc p
		 WHERE strpos(p.prosrc, 'app.cota_') > 0
		 ORDER BY p.proname`)
	if err != nil {
		t.Fatal(err)
	}
	type funcao struct {
		nome, corpo string
		config      []string
	}
	funcoes, err := pgx.CollectRows(linhas, func(r pgx.CollectableRow) (funcao, error) {
		var f funcao
		return f, r.Scan(&f.nome, &f.config, &f.corpo)
	})
	if err != nil {
		t.Fatal(err)
	}
	nomes = nil
	for _, f := range funcoes {
		nomes = append(nomes, f.nome)
		if len(f.config) != 1 || !strings.HasPrefix(f.config[0], "search_path=") {
			t.Errorf("o proconfig de %s é %v; queria só search_path", f.nome, f.config)
		}
		for _, r := range restauracoesDaCota {
			if !r.MatchString(f.corpo) {
				t.Errorf("o corpo de %s não tem %s: o GUC sairia ligado da função", f.nome, r)
			}
		}
	}
	quer = []string{"cota_alvos_do_titular", "cota_canais_do_titular", "cota_heartbeats_do_titular",
		"cota_urls_com_regra_do_titular"}
	if !slices.Equal(nomes, quer) {
		t.Errorf("funções que citam app.cota_: %v; queria exatamente %v", nomes, quer)
	}
}

// restauracoesDaCota são as restaurações que toda função de cota faz antes
// de sair: zera os dois GUCs da cota e devolve app.project_id ao valor que
// guardou em escopo.
var restauracoesDaCota = []*regexp.Regexp{
	regexp.MustCompile(`set_config\('app\.cota_projetos', '', true\)`),
	regexp.MustCompile(`set_config\('app\.cota_titular_id', '', true\)`),
	regexp.MustCompile(`escopo := COALESCE\(current_setting\('app\.project_id', true\), ''\)`),
	regexp.MustCompile(`set_config\('app\.project_id', escopo, true\)`),
}
