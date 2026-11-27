package storage_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// tabelasDoProjeto são todas as tabelas com RLS cujas linhas pertencem a um
// projeto: as quatro com project_id e as que delegam a elas (posse indireta).
// No escopo de projeto, um SELECT sem WHERE em cada uma só pode ver o projeto
// do escopo.
var tabelasDoProjeto = []string{
	"targets", "checks", "target_health", "check_rollup_1h", "incidents",
	"target_tags", "target_tls",
	"heartbeats", "heartbeat_events", "heartbeat_tags",
	"notif_channels", "notifications", "channel_health",
	"status_pages", "status_page_items",
}

// tabelasForaDoProjeto são as tabelas com RLS que não pertencem a projeto, cada
// uma com o motivo. As partições de checks ficam fora pelo nome da mãe (ver
// TestProjectScopeSeesNothingOfOtherProjectOfSameUser).
var tabelasForaDoProjeto = map[string]string{
	"sessions":              "a sessão é da pessoa, vale em todos os projetos dela",
	"session_family_proofs": "prova de família de sessão: da pessoa, como sessions",
	"totp_recovery_codes":   "segundo fator da conta, não de projeto",
	"ip_rate_limits":        "limite por IP, anterior a qualquer usuário",
	"account_tokens":        "link de confirmação de e-mail e de redefinição de senha: da conta, não de projeto",
	"account_email_daily":   "teto diário de e-mails de conta, sem dono, dividido entre réplicas",
	"projects":              "é o próprio projeto; a pertença sai de project_members",
	"project_members":       "vínculo pessoa–projeto; a pessoa vê os próprios vínculos",
}

// emEscopoDeProjeto roda fn com app.user_id e app.project_id ligados por SQL
// cru, como storage.InProjectTx faz depois de conferir a pertença: é a
// consulta que "esqueceu o filtro", e só a política a contém.
func emEscopoDeProjeto(t *testing.T, pool *pgxpool.Pool, usuario, projeto uuid.UUID, fn func(pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true), set_config('app.project_id', $2, true)`,
		usuario.String(), projeto.String()); err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// recursosDeProjeto são as linhas que semearProjeto grava num projeto.
type recursosDeProjeto struct {
	alvo, heartbeat, canal uuid.UUID
	incAlvo, incHeartbeat  uuid.UUID
	pingHash               []byte
}

// semearProjeto grava, por fora da RLS, uma linha por tabela do projeto, todas
// do mesmo dono. Com slug, grava também a página (status_pages.user_id é
// UNIQUE: um dono tem uma página só).
func semearProjeto(t *testing.T, dono, projeto uuid.UUID, nome, slug string) recursosDeProjeto {
	t.Helper()
	ctx := context.Background()
	admin := testdb.Admin(t)
	r := recursosDeProjeto{alvo: novoID(), heartbeat: novoID(), canal: novoID(),
		incAlvo: novoID(), incHeartbeat: novoID(), pingHash: hashDe("ping-" + nome)}
	passos := []struct {
		sql  string
		args []any
	}{
		{`SELECT ensure_checks_partition($1::date)`, []any{agora}},
		{`INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                       expected_status, enabled, next_run_at, created_at)
		  VALUES ($1, $2, $3, $4, 'https://exemplo.com', 'GET', 60, 5000, 200, true, $5, $5)`,
			[]any{r.alvo, dono, projeto, nome, agora}},
		{`INSERT INTO checks (id, target_id, checked_at, status, latency_ms, status_code)
		  VALUES ($1, $2, $3, 'up', 12, 200)`, []any{novoID(), r.alvo, agora}},
		{`INSERT INTO target_health (target_id) VALUES ($1)`, []any{r.alvo}},
		{`INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		  VALUES ($1, $2, 1, 1, 12, 12, 12, 12)`, []any{r.alvo, agora}},
		{`INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
			[]any{r.incAlvo, r.alvo, agora}},
		{`INSERT INTO target_tags (target_id, tag) VALUES ($1, 'prod')`, []any{r.alvo}},
		{`INSERT INTO target_tls (target_id, not_after, observed_at) VALUES ($1, $2, $2)`, []any{r.alvo, agora}},
		{`INSERT INTO heartbeats (id, user_id, project_id, name, ping_token_hash, period_sec, grace_sec,
		                          enabled, created_at, deadline_at, description, ping_resumes)
		  VALUES ($1, $2, $3, $4, $5, 3600, 300, true, $6, $6, '', false)`,
			[]any{r.heartbeat, dono, projeto, nome, r.pingHash, agora}},
		{`INSERT INTO heartbeat_events (id, heartbeat_id, at, kind) VALUES ($1, $2, $3, 'success')`,
			[]any{novoID(), r.heartbeat, agora}},
		{`INSERT INTO heartbeat_tags (heartbeat_id, tag) VALUES ($1, 'cron')`, []any{r.heartbeat}},
		{`INSERT INTO incidents (id, heartbeat_id, started_at, cause) VALUES ($1, $2, $3, 'heartbeat_late')`,
			[]any{r.incHeartbeat, r.heartbeat, agora}},
		{`INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, created_at)
		  VALUES ($1, $2, $3, 'discord', '\x01', $4, $4)`, []any{r.canal, dono, projeto, agora}},
		{`INSERT INTO notifications (id, incident_id, channel_id, event, next_attempt_at, created_at)
		  VALUES ($1, $2, $3, 'opened', $4, $4)`, []any{novoID(), r.incAlvo, r.canal, agora}},
		{`INSERT INTO channel_health (channel_id) VALUES ($1)`, []any{r.canal}},
	}
	if slug != "" {
		pagina := novoID()
		passos = append(passos,
			struct {
				sql  string
				args []any
			}{`INSERT INTO status_pages (id, user_id, project_id, slug, title, published, created_at)
			   VALUES ($1, $2, $3, $4, 'Status', true, $5)`, []any{pagina, dono, projeto, slug, agora}},
			struct {
				sql  string
				args []any
			}{`INSERT INTO status_page_items (status_page_id, target_id, display_name, position)
			   VALUES ($1, $2, 'Serviço', 0)`, []any{pagina, r.alvo}},
		)
	}
	for _, p := range passos {
		if _, err := admin.Exec(ctx, p.sql, p.args...); err != nil {
			t.Fatalf("semear %s: %s: %v", nome, p.sql, err)
		}
	}
	return r
}

// doisProjetos cria U, titular de A (o padrão) e de B, com uma linha por
// tabela em cada; a página fica só em A.
func doisProjetos(t *testing.T, pool *pgxpool.Pool) (u, a, b uuid.UUID, ra, rb recursosDeProjeto) {
	t.Helper()
	u = novoUsuario(t, pool)
	a = testdb.ProjetoPadrao(t, u)
	b = testdb.NovoProjeto(t, u, "Projeto B")
	ra = semearProjeto(t, u, a, "a", "pagina-a")
	rb = semearProjeto(t, u, b, "b", "")
	return u, a, b, ra, rb
}

// O mesmo usuário é titular de dois projetos. As políticas por user_id
// deixariam um aparecer no outro; a restritiva por app.project_id os separa,
// inclusive nas tabelas de posse indireta, que herdam pelo EXISTS no pai.
func TestProjectScopeSeesNothingOfOtherProjectOfSameUser(t *testing.T) {
	pool := testdb.New(t)
	u, a, b, _, _ := doisProjetos(t, pool)

	// A lista à mão só protege a tabela de que alguém lembrou: o catálogo
	// confere que toda tabela com RLS está numa das duas listas, e que as
	// listas não citam nome morto.
	comRLS := map[string]bool{}
	for _, tabela := range lerTabelasDaAplicacao(t, pool) {
		if !tabela.ligada || tabela.particao {
			continue // sem RLS não é deste teste; partição de checks: a mãe cobre
		}
		comRLS[tabela.nome] = true
		_, fora := tabelasForaDoProjeto[tabela.nome]
		if !fora && !slices.Contains(tabelasDoProjeto, tabela.nome) {
			t.Errorf("tabela %s com RLS fora de tabelasDoProjeto e de tabelasForaDoProjeto: ponha-a numa das duas (com o motivo, se fora)", tabela.nome)
		}
	}
	for _, nome := range tabelasDoProjeto {
		if !comRLS[nome] {
			t.Errorf("tabelasDoProjeto cita %s, que não é tabela com RLS em public", nome)
		}
	}
	for nome := range tabelasForaDoProjeto {
		if !comRLS[nome] {
			t.Errorf("tabelasForaDoProjeto cita %s, que não é tabela com RLS em public", nome)
		}
	}

	ctx := context.Background()
	for _, tabela := range tabelasDoProjeto {
		querA, querB := 1, 1
		switch tabela {
		case "incidents":
			querA, querB = 2, 2 // um do alvo e um do heartbeat
		case "status_pages", "status_page_items":
			querB = 0
		}
		for _, caso := range []struct {
			nome    string
			projeto uuid.UUID
			quer    int
		}{{"A", a, querA}, {"B", b, querB}} {
			var n int
			err := emEscopoDeProjeto(t, pool, u, caso.projeto, func(tx pgx.Tx) error {
				// tabela é sempre um literal da lista acima.
				return tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n)
			})
			if err != nil {
				t.Fatalf("contar %s no escopo de %s: %v", tabela, caso.nome, err)
			}
			if n != caso.quer {
				t.Errorf("%s no escopo do projeto %s: %d linhas; queria %d", tabela, caso.nome, n, caso.quer)
			}
		}
	}
}

// A página de status fica no projeto dela: o member a vê no escopo do
// projeto, e ela não aparece no outro projeto do titular, no escopo de
// usuário nem no escopo público de outro slug. Os três últimos não ligam
// app.project_id, então só status_pages_do_projeto os separa (a restritiva
// vale true neles).
func TestStatusPageStaysInsideItsProject(t *testing.T) {
	pool := testdb.New(t)
	u, a, b, _, _ := doisProjetos(t, pool)
	m := novoUsuario(t, pool)
	testdb.Membro(t, a, m, "member")
	w := novoUsuario(t, pool)
	c := testdb.ProjetoPadrao(t, w)
	semearProjeto(t, w, c, "c", "pagina-c")

	for _, tabela := range []string{"status_pages", "status_page_items"} {
		for _, caso := range []struct {
			nome string
			n    int
			quer int
		}{
			{"member no escopo de A", contarNoEscopoDoProjeto(t, pool, m, a, tabela), 1},
			{"titular no escopo de B", contarNoEscopoDoProjeto(t, pool, u, b, tabela), 0},
			{"outro titular no escopo do projeto dele", contarNoEscopoDoProjeto(t, pool, w, c, tabela), 1},
			{"titular no escopo de usuário", contarComEscopo(t, pool, u, tabela), 0},
			{"member no escopo de usuário", contarComEscopo(t, pool, m, tabela), 0},
			{"escopo público de pagina-c", contarPublico(t, pool, "pagina-c", tabela), 1},
		} {
			if caso.n != caso.quer {
				t.Errorf("%s, %s: %d linhas; queria %d", tabela, caso.nome, caso.n, caso.quer)
			}
		}
	}
}

// Dentro do escopo de A, nada se grava em B: nem nomeando B, nem omitindo o
// project_id (o gatilho não preenche no escopo de projeto), nem alterando uma
// linha que já é de B.
func TestProjectScopeRefusesWritingIntoAnotherProject(t *testing.T) {
	pool := testdb.New(t)
	u, a, b, _, rb := doisProjetos(t, pool)
	ctx := context.Background()

	inserir := func(projeto *uuid.UUID) error {
		return emEscopoDeProjeto(t, pool, u, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
				                     expected_status, enabled, next_run_at, created_at)
				VALUES ($1, $2, $3, 'intruso', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $4, $4)`,
				novoID(), u, projeto, agora)
			return err
		})
	}
	for nome, projeto := range map[string]*uuid.UUID{"com project_id de B": &b, "sem project_id": nil} {
		t.Run(nome, func(t *testing.T) {
			err := inserir(projeto)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Errorf("INSERT de alvo no escopo de A, %s: %v; queria 42501", nome, err)
			}
		})
	}

	t.Run("UPDATE de alvo de B", func(t *testing.T) {
		var afetadas int64
		err := emEscopoDeProjeto(t, pool, u, a, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE targets SET name = 'x' WHERE id = $1`, rb.alvo)
			afetadas = tag.RowsAffected()
			return err
		})
		if err != nil {
			t.Fatalf("UPDATE no escopo de A: %v", err)
		}
		if afetadas != 0 {
			t.Errorf("UPDATE do alvo de B no escopo de A afetou %d linhas; queria 0", afetadas)
		}
	})
}

// Sem app.project_id a restritiva vale true: o sondador, o ping e a página
// pública continuam vendo o que viam. O escopo de usuário, não: sem
// app.project_id, nenhuma permissiva <t>_do_projeto concede, e ele não vê os
// alvos de nenhum dos dois projetos de que é titular.
func TestRestrictivePolicyLeavesServiceScopesUntouched(t *testing.T) {
	pool := testdb.New(t)
	u, _, _, ra, rb := doisProjetos(t, pool)
	ctx := context.Background()

	t.Run("InUserTx", func(t *testing.T) {
		if n := alvosNoEscopoDoUsuario(t, pool, u); n != 0 {
			t.Errorf("InUserTx vê %d alvos; queria 0", n)
		}
	})

	t.Run("InProberTx", func(t *testing.T) {
		var reivindicados []uuid.UUID
		err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			linhas, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
			for _, l := range linhas {
				reivindicados = append(reivindicados, l.ID)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(reivindicados, func(x, y uuid.UUID) int { return slices.Compare(x[:], y[:]) })
		quer := []uuid.UUID{ra.alvo, rb.alvo}
		slices.SortFunc(quer, func(x, y uuid.UUID) int { return slices.Compare(x[:], y[:]) })
		if !slices.Equal(reivindicados, quer) {
			t.Errorf("o sondador reivindicou %v; queria %v", reivindicados, quer)
		}
	})

	t.Run("InPingTx", func(t *testing.T) {
		err := storage.InPingTx(ctx, pool, rb.pingHash, func(q *db.Queries) error {
			h, err := q.LockHeartbeatForPing(ctx, rb.pingHash)
			if err == nil && h.ID != rb.heartbeat {
				t.Errorf("o ping achou o heartbeat %s; queria %s", h.ID, rb.heartbeat)
			}
			return err
		})
		if err != nil {
			t.Errorf("o ping do heartbeat de B: %v; queria achar a linha", err)
		}
	})

	t.Run("InPublicTx", func(t *testing.T) {
		err := storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
			_, err := q.GetPublicPage(ctx, "pagina-a")
			return err
		})
		if err != nil {
			t.Errorf("a página pública: %v; queria achá-la", err)
		}
	})
}

// O binário anterior grava sem project_id entre a 00041 e esta migration
// (usuário criado por binário anterior ao PR 1, sem Projeto padrão para o
// gatilho achar). O segundo passe do backfill põe a linha no padrão criado.
func TestRestrictiveMigrationBackfillsOrphanRows(t *testing.T) {
	ctx := context.Background()
	pool, admin, dsn := bancoNaVersao(t, versaoDaMigration(t, sufixoProjetoPadrao))
	dono := novoUsuario(t, pool)
	alvo := novoAlvoDoBinarioAnterior(t, pool, dono, agora)
	if n := contar(t, admin, `SELECT count(*) FROM targets WHERE id = $1 AND project_id IS NULL`, alvo); n != 1 {
		t.Fatalf("o alvo do binário anterior já nasceu com projeto (%d órfãos); o teste não prova o backfill", n)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versaoDaMigration(t, "rls_restrita_por_projeto")); err != nil {
		t.Fatalf("subir a migration restritiva: %v", err)
	}

	var projetoDoAlvo, padrao *uuid.UUID
	if err := admin.QueryRow(ctx, `
		SELECT (SELECT project_id FROM targets WHERE id = $1),
		       (SELECT id FROM projects WHERE titular_id = $2 AND is_default)`, alvo, dono).
		Scan(&projetoDoAlvo, &padrao); err != nil {
		t.Fatal(err)
	}
	if padrao == nil {
		t.Fatalf("o backfill não criou o Projeto padrão de %s", dono)
	}
	if projetoDoAlvo == nil || *projetoDoAlvo != *padrao {
		t.Errorf("project_id do alvo órfão = %v; queria o Projeto padrão %s", projetoDoAlvo, *padrao)
	}
}
