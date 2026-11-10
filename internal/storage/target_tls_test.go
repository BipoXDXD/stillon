package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration de target_tls, achada pelo nome: o número é o próximo livre na
// hora do merge.
const sufixoTargetTLS = "target_tls"

var validadeTLS = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

// novaValidadeTLS grava a linha de target_tls do alvo no escopo do sondador,
// o único que a escreve em produção.
func novaValidadeTLS(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID) {
	t.Helper()
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO target_tls (target_id, not_after, observed_at) VALUES ($1, $2, $3)`,
			alvo, validadeTLS, agora)
		return err
	})
	if err != nil {
		t.Fatalf("gravar target_tls: %v", err)
	}
}

// linhasAfetadas roda um comando com um GUC ligado e devolve quantas linhas
// ele tocou.
func linhasAfetadas(t *testing.T, pool *pgxpool.Pool, guc, valor, comando string, args ...any) int64 {
	t.Helper()
	var n int64
	err := emEscopo(t, pool, guc, valor, func(tx pgx.Tx) error {
		tag, err := tx.Exec(context.Background(), comando, args...)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		t.Fatalf("%s: %v", comando, err)
	}
	return n
}

// target_tls segue o dono do alvo ou o sondador, nomeados na política.
func TestTargetTLSFollowsOwnerOrProber(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	novaValidadeTLS(t, pool, alvoA)
	novaValidadeTLS(t, pool, alvoB)

	if n := contarComEscopo(t, pool, a, "target_tls"); n != 1 {
		t.Errorf("o dono A viu %d linhas de target_tls, queria a do seu alvo", n)
	}
	if n := contarEmEscopo(t, pool, "app.user_id", b.String(),
		"target_tls WHERE target_id = '"+alvoA.String()+"'"); n != 0 {
		t.Errorf("o dono B viu %d linhas do alvo de A", n)
	}
	maisTarde := validadeTLS.Add(24 * time.Hour)
	if n := linhasAfetadas(t, pool, "app.user_id", b.String(),
		`UPDATE target_tls SET not_after = $1 WHERE target_id = $2`, maisTarde, alvoA); n != 0 {
		t.Errorf("o UPDATE de B tocou %d linhas do alvo de A", n)
	}
	var guardada time.Time
	err := emEscopo(t, pool, "app.user_id", a.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT not_after FROM target_tls WHERE target_id = $1`, alvoA).Scan(&guardada)
	})
	if err != nil || !guardada.Equal(validadeTLS) {
		t.Errorf("a validade do alvo de A ficou %v (erro %v), queria %v", guardada, err, validadeTLS)
	}

	// B não grava a linha de um alvo de A: o WITH CHECK recusa com 42501.
	alvoSemLinha := novoAlvo(t, pool, a, agora)
	err = emEscopo(t, pool, "app.user_id", b.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO target_tls (target_id, not_after, observed_at) VALUES ($1, $2, $3)`,
			alvoSemLinha, validadeTLS, agora)
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("B gravou a linha de um alvo de A: erro %v, queria 42501", err)
	}

	if n := contarEmEscopo(t, pool, "app.scope", "prober", "target_tls"); n != 2 {
		t.Errorf("o sondador viu %d linhas, queria as 2", n)
	}
	if n := linhasAfetadas(t, pool, "app.scope", "prober",
		`UPDATE target_tls SET not_after = $1 WHERE target_id = $2`, maisTarde, alvoB); n != 1 {
		t.Errorf("o UPDATE do sondador tocou %d linhas, queria 1", n)
	}
	novaValidadeTLS(t, pool, alvoSemLinha)

	if n := contarSemEscopo(t, pool, "target_tls"); n != 0 {
		t.Errorf("sem escopo nenhum, target_tls mostrou %d linhas", n)
	}
}

// O dono lê e apaga a validade do próprio alvo, mas não a escreve: só o
// sondador grava not_after. Senão um bug numa rota de usuário deixaria o dono
// escolher quando o aviso de vencimento dispara.
func TestOwnerCannotWriteTheirOwnTargetTLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo, alvoSemLinha := novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, dono, agora)
	novaValidadeTLS(t, pool, alvo)
	forjada := validadeTLS.Add(365 * 24 * time.Hour)

	// O upsert do sondador, no escopo do dono, sobre a linha existente.
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.SaveTargetTLS(ctx, db.SaveTargetTLSParams{TargetID: alvo, NotAfter: forjada, ObservedAt: agora})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o dono regravou a validade do próprio alvo: erro %v, queria 42501", err)
	}
	// O mesmo upsert num alvo do dono ainda sem linha.
	err = storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.SaveTargetTLS(ctx, db.SaveTargetTLSParams{TargetID: alvoSemLinha, NotAfter: forjada, ObservedAt: agora})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o dono inseriu a validade do próprio alvo: erro %v, queria 42501", err)
	}
	// UPDATE direto: sem política de UPDATE para o dono, a linha fica invisível
	// ao comando e nada muda.
	if n := linhasAfetadas(t, pool, "app.user_id", dono.String(),
		`UPDATE target_tls SET not_after = $1 WHERE target_id = $2`, forjada, alvo); n != 0 {
		t.Errorf("o UPDATE do dono tocou %d linhas do próprio alvo", n)
	}

	// Relidas pelo superusuário, por fora da RLS.
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var guardada time.Time
	if err := admin.QueryRow(ctx, `SELECT not_after FROM target_tls WHERE target_id = $1`, alvo).Scan(&guardada); err != nil {
		t.Fatal(err)
	}
	if !guardada.Equal(validadeTLS) {
		t.Errorf("a validade do alvo ficou %v, queria a do sondador, %v", guardada, validadeTLS)
	}
	var linhas int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM target_tls WHERE target_id = $1`, alvoSemLinha).Scan(&linhas); err != nil {
		t.Fatal(err)
	}
	if linhas != 0 {
		t.Errorf("o alvo sem leitura ganhou %d linhas no escopo do dono", linhas)
	}
}

// As consultas do dono (targets.sql) filtram por user_id além da RLS, e cada
// barreira vale sozinha, como nas tags. Aqui, o filtro sozinho: sob o escopo
// de A, a política deixa A ver a própria validade, e só o WHERE barra o pedido
// feito em nome de B.
func TestTargetTLSOwnerQueriesFilterByTheOwner(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	novaValidadeTLS(t, pool, alvoA)

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		if _, err := q.GetTargetTLSOfOwner(ctx, db.GetTargetTLSOfOwnerParams{UserID: b, TargetID: alvoA}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("ler a validade de A em nome de B deu %v, queria ErrNoRows", err)
		}
		deB, err := q.ListTargetTLSOfOwner(ctx, b)
		if err != nil {
			return err
		}
		if len(deB) != 0 {
			t.Errorf("listar as validades em nome de B devolveu %d linhas de A", len(deB))
		}
		if err := q.DeleteTargetTLSOfOwner(ctx, db.DeleteTargetTLSOfOwnerParams{UserID: b, TargetID: alvoA}); err != nil {
			return err
		}
		// Controle: em nome do dono, a mesma linha aparece.
		doDono, err := q.ListTargetTLSOfOwner(ctx, a)
		if err != nil {
			return err
		}
		if len(doDono) != 1 || doDono[0].TargetID != alvoA {
			t.Errorf("em nome de A, as validades foram %v; queria a do alvo de A", doDono)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// I8: o escopo público enxerga o alvo da página publicada, mas não a validade
// do certificado dele. Delegar a política só ao alvo abriria a tabela à status
// page.
func TestPublicScopeCannotReadTargetTLS(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	novaPagina(t, pool, dono, "pagina-tls", true, alvo)
	novaValidadeTLS(t, pool, alvo)

	if n := contarPublico(t, pool, "pagina-tls", "targets"); n != 1 {
		t.Fatalf("o escopo público viu %d alvos, queria o da página (controle)", n)
	}
	if n := contarPublico(t, pool, "pagina-tls", "target_tls"); n != 0 {
		t.Errorf("o escopo público viu %d linhas de target_tls", n)
	}
}

// Apagar o alvo leva a linha dele; apagar o usuário leva a dos alvos dele; a
// do vizinho fica.
func TestTargetTLSGoesWithTheTargetAndTheUser(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono, outro, vizinho := novoUsuario(t, pool), novoUsuario(t, pool), novoUsuario(t, pool)
	alvoApagado, alvoDoOutro, alvoDoVizinho := novoAlvo(t, pool, dono, agora), novoAlvo(t, pool, outro, agora), novoAlvo(t, pool, vizinho, agora)
	for _, alvo := range []uuid.UUID{alvoApagado, alvoDoOutro, alvoDoVizinho} {
		novaValidadeTLS(t, pool, alvo)
	}

	if n := linhasAfetadas(t, pool, "app.user_id", dono.String(), `DELETE FROM targets WHERE id = $1`, alvoApagado); n != 1 {
		t.Fatalf("o DELETE do alvo tocou %d linhas", n)
	}
	if n := contarEmEscopo(t, pool, "app.scope", "prober", "target_tls"); n != 2 {
		t.Errorf("depois de apagar o alvo, target_tls ficou com %d linhas, queria 2", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, outro); err != nil {
		t.Fatal(err)
	}
	var restante uuid.UUID
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT target_id FROM target_tls`).Scan(&restante)
	})
	if err != nil || restante != alvoDoVizinho {
		t.Errorf("depois de apagar o usuário, sobrou %s (erro %v), queria só a do vizinho", restante, err)
	}
}

// A descida tira a tabela e targets.tls_warn; a subida devolve tls_warn com o
// padrão true nos alvos que já existiam.
func TestTargetTLSDownAndUp(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	novaValidadeTLS(t, pool, alvo)
	if n := linhasAfetadas(t, pool, "app.user_id", dono.String(),
		`UPDATE targets SET tls_warn = false WHERE id = $1`, alvo); n != 1 {
		t.Fatalf("desligar tls_warn tocou %d linhas", n)
	}

	if err := storage.MigrateDownTo(ctx, dsn, versaoDaMigration(t, sufixoTargetTLS)-1); err != nil {
		t.Fatalf("down da target_tls: %v", err)
	}
	var tabela *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.target_tls')::text`).Scan(&tabela); err != nil {
		t.Fatal(err)
	}
	if tabela != nil {
		t.Errorf("target_tls continuou existindo depois do down")
	}
	if n := colunasTLSWarn(t, pool); n != 0 {
		t.Errorf("targets.tls_warn continuou existindo depois do down")
	}

	if err := storage.MigrateUpTo(ctx, dsn, versaoDaMigration(t, sufixoTargetTLS)); err != nil {
		t.Fatalf("subir de novo depois do down: %v", err)
	}
	var avisar bool
	err = emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT tls_warn FROM targets WHERE id = $1`, alvo).Scan(&avisar)
	})
	if err != nil || !avisar {
		t.Errorf("tls_warn do alvo existente voltou %v (erro %v), queria true", avisar, err)
	}
}

// Piso de rollback (spec §12): a imagem anterior à target_tls sobe no schema
// novo sem aplicar nada.
func TestFloorBinaryBootsOnTargetTLS(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var versao int64
	if err := conn.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version`).Scan(&versao); err != nil {
		t.Fatal(err)
	}
	if versao < versaoDaMigration(t, sufixoTargetTLS) {
		t.Fatalf("o schema novo parou na %d, antes da target_tls", versao)
	}
	anterior, err := goose.NewProvider(goose.DialectPostgres, conn, migrationsAte(t, arquivoDaMigration(t, sufixoTargetTLS)))
	if err != nil {
		t.Fatal(err)
	}
	aplicadas, err := anterior.Up(ctx)
	if err != nil {
		t.Fatalf("boot do binário anterior à target_tls: %v", err)
	}
	if len(aplicadas) != 0 {
		t.Errorf("o binário anterior aplicou %d migrations", len(aplicadas))
	}
}

func colunasTLSWarn(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'targets' AND column_name = 'tls_warn'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// contarSemEscopo conta as linhas numa transação sem GUC nenhum.
func contarSemEscopo(t *testing.T, pool *pgxpool.Pool, tabela string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var n int
	// tabela é sempre um literal do próprio teste.
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
