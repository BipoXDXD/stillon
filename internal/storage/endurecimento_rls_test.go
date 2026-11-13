package storage_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Endurecimento da RLS depois das políticas por comando (migration
// rls_endurecimento): fora do escopo do sondador, um incidente só muda em
// ack_at e a histerese só volta a zero; target_health e check_rollup_1h só
// ganham e perdem linha pelo sondador; protect_checks_partition só aceita
// partição de checks.

// linhaComoJSON lê a linha como o admin a vê, para conferir que uma escrita
// recusada não deixou rastro. tabela é sempre um literal do próprio teste.
func linhaComoJSON(t *testing.T, tabela, coluna string, id uuid.UUID) string {
	t.Helper()
	var linha string
	err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text)::text, '[]') FROM `+tabela+` x WHERE `+coluna+` = $1`,
		id).Scan(&linha)
	if err != nil {
		t.Fatalf("reler %s: %v", tabela, err)
	}
	return linha
}

// Uma linha por coluna de incidents, alterada no escopo do dono: todas são
// recusadas com 42501 e a linha relida fica igual. O reconhecimento continua
// passando. Sabotagem: sem o gatilho, a política de UPDATE do dono deixa
// fechar, reabrir, trocar a causa ou desmarcar o aviso do próprio incidente.
func TestDonoSoAlteraOAckDoIncidente(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo, outroAlvo := novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, dono, agora)
	hb := novoHeartbeat(t, pool, dono, "hb")
	incidente := incidenteDoSondador(t, pool, alvo)

	casos := map[string]struct {
		sql  string
		args []any
	}{
		"id":                {`UPDATE incidents SET id = $2 WHERE id = $1`, []any{novoID()}},
		"target_id":         {`UPDATE incidents SET target_id = $2 WHERE id = $1`, []any{outroAlvo}},
		"started_at":        {`UPDATE incidents SET started_at = started_at - interval '1 hour' WHERE id = $1`, nil},
		"resolved_at":       {`UPDATE incidents SET resolved_at = now() WHERE id = $1`, nil},
		"cause":             {`UPDATE incidents SET cause = 'dns' WHERE id = $1`, nil},
		"open_notified_at":  {`UPDATE incidents SET open_notified_at = now() WHERE id = $1`, nil},
		"close_notified_at": {`UPDATE incidents SET close_notified_at = now() WHERE id = $1`, nil},
		"heartbeat_id":      {`UPDATE incidents SET heartbeat_id = $2 WHERE id = $1`, []any{hb}},
		"closed_by_resume":  {`UPDATE incidents SET closed_by_resume = NOT closed_by_resume WHERE id = $1`, nil},
	}
	exigirUmCasoPorColuna(t, pool, "incidents", casos, "ack_at")

	antes := linhaComoJSON(t, "incidents", "id", incidente)
	for coluna, c := range casos {
		t.Run(coluna, func(t *testing.T) {
			comEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, c.sql, append([]any{incidente}, c.args...)...)
				if codigoPG(err) != "42501" {
					t.Errorf("o dono alterando %s deu %d linha(s) e erro %v; queria 42501", coluna, tag.RowsAffected(), err)
				}
			}, false)
			if depois := linhaComoJSON(t, "incidents", "id", incidente); depois != antes {
				t.Errorf("o incidente mudou depois da recusa:\nantes  %s\ndepois %s", antes, depois)
			}
		})
	}

	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		_, err := q.AckIncident(ctx, db.AckIncidentParams{Now: agora, UserID: dono, ID: incidente})
		return err
	})
	if err != nil {
		t.Fatalf("o dono reconhecendo o incidente: %v", err)
	}
}

// exigirUmCasoPorColuna falha se a tabela tiver coluna sem caso (fora as
// liberadas): uma coluna nova obriga a decidir se o dono pode alterá-la.
func exigirUmCasoPorColuna[T any](t *testing.T, pool *pgxpool.Pool, tabela string, casos map[string]T, liberadas ...string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT attname FROM pg_attribute
		  WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped ORDER BY attname`, tabela)
	if err != nil {
		t.Fatal(err)
	}
	colunas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	esperadas := append([]string(nil), liberadas...)
	for coluna := range casos {
		esperadas = append(esperadas, coluna)
	}
	sort.Strings(esperadas)
	if strings.Join(colunas, ",") != strings.Join(esperadas, ",") {
		t.Fatalf("colunas de %s: %v; o teste cobre %v", tabela, colunas, esperadas)
	}
}

// target_health e check_rollup_1h: o dono não insere, não apaga e não altera
// fora do zerar da histerese, e a tabela relida fica igual. Sabotagem: com a
// FOR ALL antiga, o dono grava, altera e apaga as duas.
func TestDonoNaoEscreveHistereseNemRollup(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo, semLinhas := novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, dono, agora)
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO target_health (target_id, consecutive_failures, streak_since, last_checked_at)
		 VALUES ($1, 3, $2, $2)`, alvo, agora); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		 VALUES ($1, $2, 10, 10, 1, 2, 3, 4)`, alvo, agora.Truncate(time.Hour)); err != nil {
		t.Fatal(err)
	}

	recusas := map[string]string{
		"inserir histerese":               `INSERT INTO target_health (target_id) VALUES ('` + semLinhas.String() + `')`,
		"alterar a contagem da histerese": `UPDATE target_health SET consecutive_failures = 9 WHERE target_id = '` + alvo.String() + `'`,
		"zerar e mexer em last_checked_at": `UPDATE target_health
			SET consecutive_failures = 0, consecutive_successes = 0, streak_since = NULL, last_checked_at = now()
			WHERE target_id = '` + alvo.String() + `'`,
		"inserir rollup": `INSERT INTO check_rollup_1h (target_id, bucket, total, ok)
			VALUES ('` + semLinhas.String() + `', '2026-09-22 10:00:00+00', 1, 0)`,
		"alterar rollup": `UPDATE check_rollup_1h SET total = total + 1 WHERE target_id = '` + alvo.String() + `'`,
	}
	apagar := map[string]string{
		"apagar histerese": `DELETE FROM target_health WHERE target_id = '` + alvo.String() + `'`,
		"apagar rollup":    `DELETE FROM check_rollup_1h WHERE target_id = '` + alvo.String() + `'`,
	}

	saude, rollup := linhaComoJSON(t, "target_health", "target_id", alvo), linhaComoJSON(t, "check_rollup_1h", "target_id", alvo)
	conferirIntactas := func(t *testing.T) {
		t.Helper()
		if d := linhaComoJSON(t, "target_health", "target_id", alvo); d != saude {
			t.Errorf("target_health mudou:\nantes  %s\ndepois %s", saude, d)
		}
		if d := linhaComoJSON(t, "check_rollup_1h", "target_id", alvo); d != rollup {
			t.Errorf("check_rollup_1h mudou:\nantes  %s\ndepois %s", rollup, d)
		}
		for _, tabela := range []string{"target_health", "check_rollup_1h"} {
			if d := linhaComoJSON(t, tabela, "target_id", semLinhas); d != "[]" {
				t.Errorf("%s ganhou linha do dono: %s", tabela, d)
			}
		}
	}
	for nome, sql := range recusas {
		t.Run(nome, func(t *testing.T) {
			comEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, sql)
				if codigoPG(err) != "42501" && (err != nil || tag.RowsAffected() != 0) {
					t.Errorf("o dono: %d linha(s) e erro %v; queria 42501 ou 0 linhas", tag.RowsAffected(), err)
				}
			}, false)
			conferirIntactas(t)
		})
	}
	for nome, sql := range apagar {
		t.Run(nome, func(t *testing.T) {
			comEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) {
				if n := afetadasNaTx(t, tx, sql); n != 0 {
					t.Errorf("o dono apagou %d linha(s); queria 0", n)
				}
			}, true)
			conferirIntactas(t)
		})
	}

	// O zerar da edição de alvo (ResetTargetHealth) continua passando.
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{UserID: dono, TargetID: alvo})
	})
	if err != nil {
		t.Fatalf("o dono zerando a histerese: %v", err)
	}
	var falhas int
	if err := testdb.Admin(t).QueryRow(ctx, `SELECT consecutive_failures FROM target_health WHERE target_id = $1`,
		alvo).Scan(&falhas); err != nil || falhas != 0 {
		t.Errorf("depois do zerar: %d falhas (erro %v); queria 0", falhas, err)
	}
}

// O sondador continua gravando histerese e rollup pelos caminhos do código:
// EnsureTargetHealth, LockTargetHealth, SaveTargetHealth, ConsolidateRollups e
// DeleteOldRollups.
func TestSondadorSegueGravandoHistereseERollup(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	checkDoSondador(t, pool, alvo)

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.EnsureTargetHealth(ctx, alvo); err != nil {
			return err
		}
		if _, err := q.LockTargetHealth(ctx, alvo); err != nil {
			return err
		}
		return q.SaveTargetHealth(ctx, db.SaveTargetHealthParams{TargetID: alvo, ConsecutiveFailures: 2, LastCheckedAt: agora})
	})
	if err != nil {
		t.Fatalf("sondador gravando histerese: %v", err)
	}
	for range 2 { // a segunda rodada passa pelo ON CONFLICT DO UPDATE
		if n, err := storage.ConsolidateRollups(ctx, pool, agora.Add(2*time.Hour)); err != nil || n != 1 {
			t.Fatalf("consolidar rollups: %d linhas, erro %v", n, err)
		}
	}
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		n, err := q.DeleteOldRollups(ctx, agora.Add(24*time.Hour))
		if err == nil && n != 1 {
			t.Errorf("retenção apagou %d rollups; queria 1", n)
		}
		return err
	})
	if err != nil {
		t.Fatalf("sondador apagando rollup velho: %v", err)
	}
}

// Ninguém altera um check: nem o dono nem o sondador, pela mãe ou pela
// partição. Sabotagem: uma política de UPDATE em checks deixa os dois passar.
func TestNinguemAlteraCheck(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	check := checkDoSondador(t, pool, alvo)
	particao := particaoDoCheck(t, pool, check)
	antes := linhaComoJSON(t, "checks", "id", check)

	for _, escopo := range []struct{ guc, valor string }{
		{"app.user_id", dono.String()},
		{"app.scope", "prober"},
	} {
		for _, tabela := range []string{"checks", pgx.Identifier{particao}.Sanitize()} {
			t.Run(escopo.guc+" em "+tabela, func(t *testing.T) {
				comEscopo(t, pool, escopo.guc, escopo.valor, func(tx pgx.Tx) {
					if n := afetadasNaTx(t, tx, `UPDATE `+tabela+` SET status = 'down', error_kind = 'dns'
						WHERE id = '`+check.String()+`'`); n != 0 {
						t.Errorf("alterou %d check(s); queria 0", n)
					}
				}, true)
			})
		}
	}
	if depois := linhaComoJSON(t, "checks", "id", check); depois != antes {
		t.Errorf("o check mudou:\nantes  %s\ndepois %s", antes, depois)
	}
}

// protect_checks_partition recusa o que não é partição de checks, sem tocar a
// tabela. Sabotagem: sem a conferência, ela liga RLS e cria as políticas de
// checks em targets.
func TestProtegerParticaoRecusaOutraTabela(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE avulsa (id int, target_id uuid)`); err != nil {
		t.Fatal(err)
	}
	for _, tabela := range []string{"targets", "avulsa"} {
		t.Run(tabela, func(t *testing.T) {
			antes := politicasDe(t, pool, tabela)
			if _, err := pool.Exec(ctx, `SELECT protect_checks_partition($1::regclass)`, tabela); err == nil {
				t.Errorf("protect_checks_partition(%s) passou; queria erro", tabela)
			}
			if depois := politicasDe(t, pool, tabela); depois != antes {
				t.Errorf("políticas de %s mudaram:\nantes  %s\ndepois %s", tabela, antes, depois)
			}
		})
	}
	var ligada bool
	if err := pool.QueryRow(ctx, `SELECT relrowsecurity FROM pg_class WHERE relname = 'avulsa'`).Scan(&ligada); err != nil || ligada {
		t.Errorf("avulsa com RLS=%v (erro %v); queria sem RLS", ligada, err)
	}
}

// Uma partição com políticas de mesmo nome e comando, mas expressão afrouxada,
// não passa pela comparação com a mãe. Sabotagem: comparando só nome e
// comando, as duas saem iguais.
func TestParidadeDasParticoesComparaAsExpressoes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE TABLE checks_2030_01 PARTITION OF checks
		     FOR VALUES FROM ('2030-01-01 00:00:00+00') TO ('2030-02-01 00:00:00+00')`,
		`ALTER TABLE checks_2030_01 ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE checks_2030_01 FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY checks_select_via_target ON checks_2030_01 FOR SELECT USING (true)`,
		`CREATE POLICY checks_prober_insert ON checks_2030_01 FOR INSERT
		     WITH CHECK (EXISTS (SELECT 1 FROM targets t WHERE t.id = checks_2030_01.target_id))`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if afrouxada, mae := politicasDe(t, pool, "checks_2030_01"), politicasDe(t, pool, "checks"); afrouxada == mae {
		t.Errorf("a partição afrouxada saiu igual à mãe:\n%s", mae)
	}

	// A partição que a função cria sai igual, com o nome normalizado.
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatal(err)
	}
	if criada, mae := politicasDe(t, pool, particaoDoMesDeAgora), politicasDe(t, pool, "checks"); criada != mae {
		t.Errorf("partição criada pela função difere da mãe:\npartição %s\nmãe      %s", criada, mae)
	}
}

// Só existem as políticas de UPDATE esperadas nas tabelas com dono imutável
// (00031): as três do dono (FOR ALL) e as quatro dos escopos de serviço. Um
// escopo novo com UPDATE faz o teste falhar e obriga a revisar o gatilho.
func TestPoliticasDeUpdateDasTabelasComDono(t *testing.T) {
	pool := testdb.New(t)
	rows, err := pool.Query(context.Background(),
		`SELECT c.relname || '.' || p.polname || ':' || p.polcmd::text
		   FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		  WHERE c.relname IN ('targets', 'heartbeats', 'notif_channels') AND p.polcmd IN ('w', '*')
		  ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	politicas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	esperadas := []string{
		"heartbeats.heartbeats_owner:*",
		"heartbeats.heartbeats_ping_update:w",
		"heartbeats.heartbeats_prober_update:w",
		"notif_channels.notif_channels_owner:*",
		"notif_channels.notif_channels_verify_update:w",
		"targets.targets_owner:*",
		"targets.targets_prober_update:w",
	}
	if strings.Join(politicas, "\n") != strings.Join(esperadas, "\n") {
		t.Errorf("políticas de UPDATE:\n%s\nqueria:\n%s", strings.Join(politicas, "\n"), strings.Join(esperadas, "\n"))
	}
}

// Os gatilhos de dono imutável (00031) e os desta migration rodam BEFORE, por
// linha, no UPDATE: um AFTER também abortaria a transação, mas depois de a RLS
// e as constraints julgarem a linha nova, e o erro deixaria de ser o 42501
// do gatilho.
func TestGatilhosDeGuardaSaoBeforeUpdatePorLinha(t *testing.T) {
	pool := testdb.New(t)
	const (
		porLinha = 1 << 0
		antes    = 1 << 1
		update   = 1 << 4
	)
	for _, g := range []struct{ tabela, gatilho string }{
		{"targets", "targets_user_id_imutavel"},
		{"heartbeats", "heartbeats_user_id_imutavel"},
		{"notif_channels", "notif_channels_user_id_imutavel"},
		{"incidents", "incidents_so_ack_fora_do_sondador"},
		{"target_health", "target_health_so_zerar_fora_do_sondador"},
	} {
		t.Run(g.gatilho, func(t *testing.T) {
			var tipo int16
			err := pool.QueryRow(context.Background(),
				`SELECT tgtype FROM pg_trigger WHERE tgrelid = $1::regclass AND tgname = $2`,
				g.tabela, g.gatilho).Scan(&tipo)
			if err != nil {
				t.Fatalf("ler o gatilho: %v", err)
			}
			if tipo&porLinha == 0 || tipo&antes == 0 || tipo&update == 0 {
				t.Errorf("tgtype=%d; queria BEFORE UPDATE FOR EACH ROW", tipo)
			}
		})
	}
}

// O down devolve as políticas FOR ALL, tira os gatilhos e a conferência da
// função; o up de novo volta tudo.
func TestEndurecimentoRLSDesceESobe(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, "rls_endurecimento")
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	estado := func() string {
		t.Helper()
		var s string
		err := pool.QueryRow(ctx, `
			SELECT (SELECT string_agg(polname, ',' ORDER BY polname) FROM pg_policy
			         WHERE polrelid IN ('target_health'::regclass, 'check_rollup_1h'::regclass))
			       || ' / ' ||
			       (SELECT count(*) FROM pg_trigger WHERE tgname LIKE '%fora_do_sondador')::text`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	subida := estado()
	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if s, queria := estado(), "check_rollup_via_target,target_health_via_target / 0"; s != queria {
		t.Errorf("depois do down: %q; queria %q", s, queria)
	}
	if _, err := pool.Exec(ctx, `SELECT protect_checks_partition('targets'::regclass)`); err == nil ||
		!strings.Contains(err.Error(), "target_id") {
		t.Errorf("a função de antes deveria tentar criar a política em targets e cair na coluna; deu %v", err)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
	if s := estado(); s != subida {
		t.Errorf("depois de subir de novo: %q; queria %q", s, subida)
	}
}
