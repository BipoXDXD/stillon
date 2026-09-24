package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// novaPagina cria a página do dono com os alvos dados, na ordem.
func novaPagina(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, slug string, publicada bool, alvos ...uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: id, UserID: dono, Slug: slug, Title: "Status", Published: publicada, CreatedAt: agora,
		}); err != nil {
			return err
		}
		for i, alvo := range alvos {
			if err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
				StatusPageID: id, TargetID: alvo, DisplayName: "Serviço", Position: int32(i),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("criar página: %v", err)
	}
	return id
}

// contarPublico conta as linhas de uma tabela no escopo público do slug,
// com um SELECT sem WHERE: só a política separa o que ele vê.
func contarPublico(t *testing.T, pool *pgxpool.Pool, slug, tabela string) int {
	t.Helper()
	var n int
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', $1, true)`, slug); err != nil {
		t.Fatal(err)
	}
	// tabela é sempre um literal do próprio teste.
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// O escopo público vê a página publicada do slug e, por delegação, só os
// alvos dela: um alvo do mesmo dono fora da página, a página de outro dono e
// os canais ficam de fora.
func TestPublicScopeSeesOnlyThePublishedPageTargets(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	naPagina, foraDaPagina := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, a, agora)
	alvoB := novoAlvo(t, pool, b, agora)
	novaPagina(t, pool, a, "pagina-a", true, naPagina)
	novaPagina(t, pool, b, "pagina-b", true, alvoB)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	// Uma linha de cada tabela por alvo, dentro e fora da página: sem elas, o
	// 0 das tabelas que o público não pode ver seria verdade por acidente.
	canal := novoCanal(t, pool, a, nil, time.Time{})
	for _, alvo := range []uuid.UUID{naPagina, foraDaPagina, alvoB} {
		novoCheck(t, pool, alvo, agora)
		novaNotificacao(t, pool, novoIncidente(t, pool, alvo), canal)
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.EnsureTargetHealth(ctx, alvo) }); err != nil {
			t.Fatal(err)
		}
	}
	motivo := "rejected"
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: canal, Now: agora, LastError: &motivo})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ConsolidateRollups(ctx, pool, agora.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for tabela, quer := range map[string]int{"notifications": 3, "channel_health": 1, "check_rollup_1h": 3, "target_health": 3} {
		if n := contarComoSondador(t, pool, `SELECT count(*) FROM `+tabela); n != quer {
			t.Fatalf("a preparação gravou %d linhas em %s, queria %d", n, tabela, quer)
		}
	}

	for tabela, quer := range map[string]int{
		"status_pages": 1, "status_page_items": 1, "targets": 1, "checks": 1, "incidents": 1,
		"check_rollup_1h": 1, "target_health": 1,
		"notif_channels": 0, "notifications": 0, "channel_health": 0,
	} {
		if n := contarPublico(t, pool, "pagina-a", tabela); n != quer {
			t.Errorf("no escopo público de pagina-a, %s mostrou %d linhas, queria %d", tabela, n, quer)
		}
	}

	err = storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
		if _, err := q.GetPublicPage(ctx, "pagina-b"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("com o slug de A, a página de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDraftPageIsInvisibleToThePublic(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	novaPagina(t, pool, a, "rascunho", false, novoAlvo(t, pool, a, agora))

	for _, tabela := range []string{"status_pages", "status_page_items", "targets"} {
		if n := contarPublico(t, pool, "rascunho", tabela); n != 0 {
			t.Errorf("página não publicada: %s mostrou %d linhas ao público", tabela, n)
		}
	}
}

// As políticas que delegam a targets são FOR ALL: sem a transação READ ONLY,
// o escopo público poderia fechar o incidente de um alvo da página.
func TestPublicScopeIsReadOnly(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	inc := novoIncidente(t, pool, alvo)
	novaPagina(t, pool, a, "pagina-a", true, alvo)

	err := storage.InPublicTx(ctx, pool, "pagina-a", func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: inc, ResolvedAt: agora.Add(time.Minute)})
		return err
	})
	if codigoPG(err) != "25006" {
		t.Errorf("escrita no escopo público deu %v, queria 25006 (read-only transaction)", err)
	}
	if err := storage.InPublicTx(ctx, pool, "", func(*db.Queries) error { return nil }); !errors.Is(err, storage.ErrNoSlug) {
		t.Errorf("slug vazio deu %v, queria ErrNoSlug", err)
	}
}

// A FK ignora RLS: sem o WITH CHECK nos itens, A poria na própria página
// o alvo de B, e a página pública de A mostraria o estado do alvo de B.
func TestOwnerCannotPutAnotherUsersTargetOnThePage(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	pagina := novaPagina(t, pool, a, "pagina-a", true)
	alvoB := novoAlvo(t, pool, b, agora)

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		return q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: alvoB, DisplayName: "roubado", Position: 0,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A pondo o alvo de B na página deu %v, queria 42501", err)
	}
}

// Uma página por usuário (spec §2.1), slug único entre todos, e o formato
// do slug também no banco.
func TestStatusPageConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novaPagina(t, pool, a, "pagina-a", false)

	inserir := func(dono uuid.UUID, slug string) error {
		return storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
			return q.InsertStatusPage(ctx, db.InsertStatusPageParams{
				ID: novoID(), UserID: dono, Slug: slug, Title: "x", CreatedAt: agora,
			})
		})
	}
	if err := inserir(a, "outra-de-a"); codigoPG(err) != "23505" {
		t.Errorf("segunda página de A deu %v, queria 23505", err)
	}
	if err := inserir(b, "pagina-a"); codigoPG(err) != "23505" {
		t.Errorf("slug repetido por B deu %v, queria 23505", err)
	}
	for _, slug := range []string{"ab", "-abc", "abc-", "ABC", "a_b_c", "com espaço"} {
		if err := inserir(b, slug); codigoPG(err) != "23514" {
			t.Errorf("slug %q deu %v, queria 23514 (CHECK)", slug, err)
		}
	}
}

// A política pública não muda o que o dono vê. Prova a propriedade, e não a
// guarda "app.public_slug IS NOT NULL" da política: sem ela, a função
// devolveria os itens que o dono enxerga, que são só os das páginas dele, e
// o resultado seria o mesmo (achado da revisão, provado por sabotagem). A
// guarda é atalho: fora do escopo público, a função nem precisa rodar.
func TestPublicPolicyDoesNotWidenOwnerScope(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora)
	novaPagina(t, pool, b, "pagina-b", true, novoAlvo(t, pool, b, agora))

	if n := contarComEscopo(t, pool, a, "targets"); n != 1 {
		t.Errorf("A viu %d alvos com a página de B publicada, queria 1", n)
	}
}

// channel_health segue notifications: o dono do canal e o sondador; o
// escopo verify, que enxerga o canal, não.
func TestChannelHealthFollowsOwnerOrProber(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	h := hashDe("token-a")
	canal := novoCanal(t, pool, a, h, agora.Add(24*time.Hour))
	motivo := "rejected"
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: canal, Now: agora, LastError: &motivo})
	})
	if err != nil {
		t.Fatal(err)
	}

	if n := contarComEscopo(t, pool, a, "channel_health"); n != 1 {
		t.Errorf("o dono viu %d linhas de channel_health, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "channel_health"); n != 0 {
		t.Errorf("outro usuário viu %d linhas de channel_health", n)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.verify_token_hash', encode($1::bytea, 'hex'), true)`, h); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_health`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("o escopo verify viu %d linhas de channel_health", n)
	}
}

// A política pública de targets resolve os alvos da página UMA vez por
// consulta, e não uma vez por alvo da instância (achado da revisão: com uma
// função por linha, a página pública custava ~0,5 s com 5 mil alvos de
// outros donos). A contagem de chamadas vem do próprio Postgres
// (track_functions), sem cronômetro.
func TestPublicPolicyResolvesThePageTargetsOnce(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, outro := novoUsuario(t, pool), novoUsuario(t, pool)
	novaPagina(t, pool, a, "pagina-a", true, novoAlvo(t, pool, a, agora))
	for range 50 {
		novoAlvo(t, pool, outro, agora)
	}

	var papel string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&papel); err != nil {
		t.Fatal(err)
	}
	admin, err := storage.NewPool(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// track_functions só o superusuário liga; SET ROLE devolve a conexão ao
	// papel da aplicação, e a RLS volta a valer.
	for _, sql := range []string{`SET track_functions = 'pl'`, `SET ROLE ` + pgx.Identifier{papel}.Sanitize()} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.Exec(ctx, `RESET ROLE`)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', 'pagina-a', true)`); err != nil {
		t.Fatal(err)
	}
	var vistos int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&vistos); err != nil {
		t.Fatal(err)
	}
	var chamadas int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(pg_stat_get_xact_function_calls('public_page_target_ids'::regproc), 0)`).Scan(&chamadas); err != nil {
		t.Fatal(err)
	}
	if vistos != 1 || chamadas < 1 || chamadas > 2 {
		t.Errorf("o escopo público viu %d alvos com %d chamadas da função; queria 1 alvo e 1 ou 2 chamadas, e não uma por alvo (51)", vistos, chamadas)
	}
}
