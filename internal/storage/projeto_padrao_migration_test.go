package storage_test

import (
	"bytes"
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration do backfill do Projeto padrão, achada pelo nome.
const sufixoProjetoPadrao = "projeto_padrao"

// tabelasDeRecurso são as que ganharam project_id na 00040.
var tabelasDeRecurso = []string{"targets", "heartbeats", "notif_channels", "status_pages"}

// legado guarda o que o backfill não pode mexer: os segredos e o slug que
// já estão em uso fora do banco (URL de ping, link de verificação, página).
type legado struct {
	dono       uuid.UUID
	heartbeat  uuid.UUID
	pingHash   []byte
	canal      uuid.UUID
	verifyHash []byte
	pagina     uuid.UUID
	slug       string
}

// semearLegado grava, com o banco parado na 00038, um usuário com alvo e
// check, heartbeat, canal de e-mail pendente e página: só com o SQL das
// colunas daquela versão, como o binário em produção grava.
func semearLegado(t *testing.T, pool *pgxpool.Pool, nome string) legado {
	t.Helper()
	ctx := context.Background()
	l := legado{
		dono:       novoUsuario(t, pool),
		heartbeat:  novoID(),
		pingHash:   hashDe("ping-" + nome),
		canal:      novoID(),
		verifyHash: hashDe("verifica-" + nome),
		pagina:     novoID(),
		slug:       "pagina-" + nome,
	}
	alvo := novoAlvoDoBinarioAnterior(t, pool, l.dono, agora)
	novoCheck(t, pool, alvo, agora)
	novoCheck(t, pool, alvo, agora.Add(time.Minute))
	expira := agora.Add(time.Hour)
	err := emEscopo(t, pool, "app.user_id", l.dono.String(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, antigaInsertHeartbeat, l.heartbeat, l.dono, "hb", l.pingHash, int32(3600),
			(*string)(nil), (*string)(nil), int32(300), true, agora, agora.Add(time.Hour), "", false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, antigaInsertChannel, l.canal, l.dono, "email", []byte{1},
			(*time.Time)(nil), l.verifyHash, &expira, agora); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, antigaInsertStatusPage, l.pagina, l.dono, l.slug, "Status", true, agora)
		return err
	})
	if err != nil {
		t.Fatalf("semear o legado de %s: %v", nome, err)
	}
	return l
}

// bancoNaVersao cria um banco vazio, sobe até versao e devolve o pool da
// aplicação, o pool de superusuário do MESMO banco (para ler por fora da
// RLS) e o DSN.
func bancoNaVersao(t *testing.T, versao int64) (*pgxpool.Pool, *pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir até a %d: %v", versao, err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatalf("criar as partições de checks: %v", err)
	}

	app, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adm, err := url.Parse(testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	adm.Path = app.Path
	admin, err := pgxpool.New(ctx, adm.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	return pool, admin, dsn
}

func contar(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// contagensDeRecursos lê, por fora da RLS, quantas linhas cada tabela tem.
func contagensDeRecursos(t *testing.T, admin *pgxpool.Pool) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, tabela := range append([]string{"checks"}, tabelasDeRecurso...) {
		// tabela é sempre um literal do próprio teste.
		got[tabela] = contar(t, admin, `SELECT count(*) FROM `+tabela)
	}
	return got
}

// conferirProjetoPadrao confere, no escopo do dono, que ele tem um projeto
// só, o padrão, e que todo recurso dele está nesse projeto.
func conferirProjetoPadrao(t *testing.T, pool *pgxpool.Pool, l legado) {
	t.Helper()
	ctx := context.Background()
	err := emEscopo(t, pool, "app.user_id", l.dono.String(), func(tx pgx.Tx) error {
		var (
			n                                int
			padrao, titular, nomeOK, versao7 bool
		)
		if err := tx.QueryRow(ctx, `
			SELECT count(*), bool_and(is_default), bool_and(titular_id = $1),
			       bool_and(name = 'Projeto padrão'), bool_and(uuid_extract_version(id) = 7)
			  FROM projects`, l.dono).Scan(&n, &padrao, &titular, &nomeOK, &versao7); err != nil {
			return err
		}
		if n != 1 || !padrao || !titular || !nomeOK || !versao7 {
			t.Errorf("projetos de %s: n=%d padrão=%v titular=%v nome=%v v7=%v; queria 1 e tudo true",
				l.dono, n, padrao, titular, nomeOK, versao7)
			return nil
		}
		var projeto uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM projects`).Scan(&projeto); err != nil {
			return err
		}
		var papel string
		if err := tx.QueryRow(ctx, `SELECT role FROM project_members WHERE user_id = $1`, l.dono).Scan(&papel); err != nil {
			return err
		}
		if papel != "owner" {
			t.Errorf("papel de %s no padrão = %q, queria owner", l.dono, papel)
		}
		for _, tabela := range tabelasDeRecurso {
			// tabela é sempre um literal do próprio teste.
			if fora := contar(t, tx, `SELECT count(*) FROM `+tabela+` WHERE project_id IS DISTINCT FROM $1`, projeto); fora != 0 {
				t.Errorf("%s de %s: %d linha(s) fora do padrão, queria 0", tabela, l.dono, fora)
			}
			if dentro := contar(t, tx, `SELECT count(*) FROM `+tabela+` WHERE project_id = $1`, projeto); dentro != 1 {
				t.Errorf("%s de %s: %d linha(s) no padrão, queria 1", tabela, l.dono, dentro)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("conferir o padrão de %s: %v", l.dono, err)
	}
}

// O backfill põe cada usuário legado no próprio Projeto padrão, com tudo o
// que ele tem, e marca só o dono atual (o usuário mais antigo) como operador
// de e-mail verificado. Não toca nos tokens de ping, nos links de
// verificação nem no slug.
func TestDefaultProjectBackfillOnLegacyData(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoProjetoPadrao)
	pool, admin, dsn := bancoNaVersao(t, versaoDaMigration(t, "totp_bloqueio_progressivo"))
	usuarios := []legado{semearLegado(t, pool, "a"), semearLegado(t, pool, "b")}
	// b fica mais antigo que a, embora tenha o id maior: o operador sai do
	// created_at, e o id só desempata.
	if _, err := admin.Exec(ctx, `UPDATE users SET created_at = $2 WHERE id = $1`, usuarios[1].dono, agora.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	operadorEsperado := usuarios[1].dono
	antes := contagensDeRecursos(t, admin)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir até a %d: %v", versao, err)
	}

	for _, l := range usuarios {
		conferirProjetoPadrao(t, pool, l)

		var ping, verifica []byte
		var slug string
		if err := admin.QueryRow(ctx, `SELECT ping_token_hash FROM heartbeats WHERE id = $1`, l.heartbeat).Scan(&ping); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, `SELECT verify_token_hash FROM notif_channels WHERE id = $1`, l.canal).Scan(&verifica); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, `SELECT slug FROM status_pages WHERE id = $1`, l.pagina).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ping, l.pingHash) || !bytes.Equal(verifica, l.verifyHash) || slug != l.slug {
			t.Errorf("%s: ping=%x verifica=%x slug=%q; queria os de antes (%x, %x, %q)",
				l.dono, ping, verifica, slug, l.pingHash, l.verifyHash, l.slug)
		}

		var operador, verificado bool
		if err := admin.QueryRow(ctx,
			`SELECT is_operator, email_verified_at IS NOT NULL FROM users WHERE id = $1`, l.dono).Scan(&operador, &verificado); err != nil {
			t.Fatal(err)
		}
		if querOperador := l.dono == operadorEsperado; operador != querOperador || verificado != querOperador {
			t.Errorf("%s: is_operator=%v e-mail verificado=%v; queria os dois %v (só o mais antigo vira operador)",
				l.dono, operador, verificado, querOperador)
		}
	}
	if depois := contagensDeRecursos(t, admin); !mapasIguais(antes, depois) {
		t.Errorf("contagens mudaram no backfill: antes %v, depois %v", antes, depois)
	}
}

func mapasIguais(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func rodarBackfill(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `SELECT backfill_projetos_padrao()`); err != nil {
		t.Fatalf("rodar o backfill: %v", err)
	}
}

// A 00042 e o contract rodam a função de novo: rerodar não duplica projeto
// nem vínculo.
func TestBackfillIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	admin := testdb.Admin(t)
	// O alvo órfão (sem projeto) é dado legado: depois da troca das políticas
	// por pertença ao projeto, o escopo de usuário já não grava alvo, então ele
	// entra pelo superusuário, como estaria no banco.
	for range 2 {
		dono := novoUsuario(t, pool)
		novoAlvoDoBinarioAnterior(t, admin, dono, agora)
	}

	rodarBackfill(t, pool)
	projetos := contar(t, admin, `SELECT count(*) FROM projects`)
	vinculos := contar(t, admin, `SELECT count(*) FROM project_members`)
	if projetos != 2 || vinculos != 2 {
		t.Fatalf("primeira rodada: %d projeto(s) e %d vínculo(s), queria 2 e 2", projetos, vinculos)
	}
	rodarBackfill(t, pool)
	if p, v := contar(t, admin, `SELECT count(*) FROM projects`), contar(t, admin, `SELECT count(*) FROM project_members`); p != projetos || v != vinculos {
		t.Errorf("segunda rodada: %d projeto(s) e %d vínculo(s), queria %d e %d", p, v, projetos, vinculos)
	}
}

// Quem se cadastra depois da 00041 ganha o Projeto padrão quando a função
// roda de novo, mas não vira operador nem ganha e-mail verificado.
func TestBackfillRerunDoesNotPromoteOperator(t *testing.T) {
	pool := testdb.New(t)
	admin := testdb.Admin(t)
	x := novoUsuario(t, pool)

	rodarBackfill(t, pool)

	if n := contar(t, admin, `SELECT count(*) FROM projects WHERE titular_id = $1 AND is_default`, x); n != 1 {
		t.Errorf("projeto padrão de X: %d, queria 1", n)
	}
	var operador, verificado bool
	if err := admin.QueryRow(context.Background(),
		`SELECT is_operator, email_verified_at IS NOT NULL FROM users WHERE id = $1`, x).Scan(&operador, &verificado); err != nil {
		t.Fatal(err)
	}
	if operador || verificado {
		t.Errorf("X: is_operator=%v e-mail verificado=%v; queria os dois false", operador, verificado)
	}
}

// Reverter o expand até a 00038 não pode apagar recurso: projeto apagado
// levaria alvos, heartbeats, canais e páginas em cascata. O binário anterior
// volta a gravar, e subir de novo refaz o Projeto padrão.
func TestProjectMigrationsDownKeepResources(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoProjetoPadrao)
	anterior := versaoDaMigration(t, "totp_bloqueio_progressivo")
	pool, admin, dsn := bancoNaVersao(t, anterior)
	usuarios := []legado{semearLegado(t, pool, "a"), semearLegado(t, pool, "b")}
	antes := contagensDeRecursos(t, admin)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir até a %d: %v", versao, err)
	}
	if err := storage.MigrateDownTo(ctx, dsn, anterior); err != nil {
		t.Fatalf("descer até a %d: %v", anterior, err)
	}

	if depois := contagensDeRecursos(t, admin); !mapasIguais(antes, depois) {
		t.Errorf("contagens mudaram no down: antes %v, depois %v", antes, depois)
	}
	if n := contar(t, admin, `SELECT count(*) FROM pg_class WHERE relname = 'projects'`); n != 0 {
		t.Errorf("depois do down: %d relação(ões) projects, queria 0", n)
	}
	inserirComSQLAntigo(t, pool, insercoesAntigas[0], usuarios[0].dono)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo até a %d: %v", versao, err)
	}
	if n := contar(t, admin, `SELECT count(*) FROM projects WHERE is_default`); n != len(usuarios) {
		t.Errorf("depois de subir de novo: %d projeto(s) padrão, queria %d", n, len(usuarios))
	}
	if n := contar(t, admin, `SELECT count(*) FROM targets WHERE project_id IS NULL`); n != 0 {
		t.Errorf("depois de subir de novo: %d alvo(s) sem projeto, queria 0", n)
	}
}

// A 00042 chama a função dentro de um escopo: ao voltar, o app.user_id de
// quem chamou tem de estar como estava.
func TestBackfillKeepsCallerScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	quemChama := novoUsuario(t, pool)
	novoUsuario(t, pool)

	err := emEscopo(t, pool, "app.user_id", quemChama.String(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT backfill_projetos_padrao()`); err != nil {
			return err
		}
		var escopo string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.user_id', true)`).Scan(&escopo); err != nil {
			return err
		}
		if escopo != quemChama.String() {
			t.Errorf("app.user_id depois do backfill = %q, queria %q", escopo, quemChama)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// As funções da 00041 não ficam abertas a PUBLIC: só o dono (o papel da
// aplicação) as executa.
func TestProjectBackfillFunctionsNotExecutableByPublic(t *testing.T) {
	pool := testdb.New(t)
	n := contar(t, pool, `
		SELECT count(*)
		  FROM pg_proc p, aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a
		 WHERE p.proname IN ('backfill_projetos_padrao', 'gerar_uuid_v7')
		   AND a.grantee = 0 AND a.privilege_type = 'EXECUTE'`)
	if n != 0 {
		t.Errorf("%d função(ões) da 00041 executáveis por PUBLIC, queria 0", n)
	}
}
