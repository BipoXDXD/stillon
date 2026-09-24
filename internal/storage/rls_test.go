package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// A consulta que esqueceu o filtro, e a que filtrou pelo usuário errado:
// só a política separa os dois donos.
func TestRLSIsolatesTargetsEvenWithTheWrongFilter(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora)
	alvoB := novoAlvo(t, pool, b, agora)

	if n := contarComEscopo(t, pool, a, "targets"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d alvos, queria 1", n)
	}

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		daB, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: b, MaxRows: 10})
		if err != nil {
			return err
		}
		if len(daB) != 0 {
			t.Errorf("sob o escopo de A, listar os alvos de B devolveu %d", len(daB))
		}
		_, err = q.GetTarget(ctx, db.GetTargetParams{UserID: b, ID: alvoB})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("sob o escopo de A, buscar o alvo de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithoutScopeNothingIsVisible(t *testing.T) {
	pool := testdb.New(t)
	novoAlvo(t, pool, novoUsuario(t, pool), agora)

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM targets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sem escopo, a aplicação viu %d alvos: a RLS não está forçada para o dono da tabela", n)
	}
}

// Review Focus 1. Depois do primeiro SET LOCAL da sessão, o GUC volta como
// string vazia, e não NULL. Um pool reusa a conexão; a próxima consulta nela
// precisa funcionar. (Sem as aspas simples duplas aqui: o gofmt as troca por
// aspas tipográficas em comentário de documentação.)
func TestScopeSurvivesConnectionReuse(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	novoAlvo(t, pool, dono, agora.Add(-time.Minute))

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	err = storage.InUserTx(ctx, conn, dono, func(q *db.Queries) error {
		_, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: dono, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A premissa do teste. Se o Postgres um dia devolver NULL aqui, este
	// teste deixa de provar alguma coisa, e precisa dizer isso.
	var vazio bool
	if err := conn.QueryRow(ctx, `SELECT current_setting('app.user_id', true) = ''`).Scan(&vazio); err != nil {
		t.Fatal(err)
	}
	if !vazio {
		t.Fatal("premissa quebrada: o GUC não ficou '' depois do SET LOCAL; revise este teste")
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM targets`).Scan(&n); err != nil {
		t.Fatalf("consulta sem escopo numa conexão reusada falhou: %v", err)
	}
	if n != 0 {
		t.Errorf("sem escopo viu %d alvos", n)
	}

	err = storage.InProberTx(ctx, conn, func(q *db.Queries) error {
		_, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatalf("o sondador falhou numa conexão reusada: %v", err)
	}
}

func TestProberCanRescheduleButNotCreate(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoAlvo(t, pool, a, agora.Add(-time.Minute))
	novoAlvo(t, pool, b, agora.Add(-time.Minute))

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		got, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Errorf("o sondador reivindicou %d alvos, queria os 2, de dois donos", len(got))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: uuid.Must(uuid.NewV7()), UserID: a, Name: "x", URL: "https://x",
			Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o sondador criou um alvo (erro = %v); queria violação de RLS 42501", err)
	}
}

// Duas tentativas, cada uma na sua transação: um erro aborta a transação, e a
// segunda instrução falharia por isso, não pela RLS.
//
// Com WHERE, o UPDATE precisa ler colunas, e o Postgres passa a exigir que a
// linha NOVA satisfaça também o USING das políticas de SELECT — uma segunda
// barreira, independente do WITH CHECK de targets_owner. Por isso a primeira
// tentativa continua verde com WITH CHECK (true). Só a tentativa sem WHERE
// depende do WITH CHECK de dono, e é ela que o prende.
func TestOwnerCannotHandTargetToAnotherUser(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)

	tentar := func(sql string, args ...any) error {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.String()); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, sql, args...)
		return err
	}

	if err := tentar(`UPDATE targets SET user_id = $1 WHERE id = $2`, b, alvo); codigoPG(err) != "42501" {
		t.Errorf("com WHERE, A entregou o alvo a B (erro = %v); queria violação de RLS 42501", err)
	}
	if err := tentar(`UPDATE targets SET user_id = $1`, b); codigoPG(err) != "42501" {
		t.Errorf("sem WHERE, A entregou o alvo a B (erro = %v); queria violação de RLS 42501: "+
			"o WITH CHECK de targets_owner não está barrando", err)
	}
}

// uuid.Nil nunca é usuário; a transação nem começa.
func TestInUserTxRefusesNilUser(t *testing.T) {
	pool := testdb.New(t)
	chamou := false
	err := storage.InUserTx(context.Background(), pool, uuid.Nil, func(*db.Queries) error {
		chamou = true
		return nil
	})
	if !errors.Is(err, storage.ErrNoUser) {
		t.Errorf("InUserTx com uuid.Nil deu %v, queria ErrNoUser", err)
	}
	if chamou {
		t.Error("InUserTx com uuid.Nil chamou fn")
	}
}

func TestChecksAreVisibleOnlyThroughTheirTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	novoCheck(t, pool, alvoA, agora)
	novoCheck(t, pool, alvoB, agora)

	if n := contarComEscopo(t, pool, a, "checks"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d checks, queria 1", n)
	}
	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		rows, err := q.ListChecks(ctx, db.ListChecksParams{
			UserID: b, TargetID: alvoB, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Errorf("sob o escopo de A, os checks de B vieram (%d)", len(rows))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// O sondador depende deste código para reconhecer "o alvo sumiu no meio da
// sonda" (Task 7). O natural seria esperar a FK (23503); a RLS chega antes.
func TestCheckForVanishedTargetFailsWithRLSCode(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: uuid.Must(uuid.NewV7()),
			CheckedAt: agora, Status: "up", LatencyMS: 1,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("check de alvo inexistente deu %v; queria 42501", err)
	}
}

// SKIP LOCKED, e não só FOR UPDATE: sem ele, o segundo sondador ESPERA o
// primeiro soltar as linhas — não duplica, mas trava. O prazo curto
// transforma a espera em falha.
func TestSkipLockedDoesNotWaitForAnotherProber(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	for range 10 {
		novoAlvo(t, pool, dono, agora.Add(-time.Minute))
	}

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	if _, err := tx1.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	primeiros, err := db.New(tx1).ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 5})
	if err != nil {
		t.Fatal(err)
	}

	ctx2, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var segundos []db.ClaimDueTargetsRow
	err = storage.InProberTx(ctx2, pool, func(q *db.Queries) error {
		var err error
		segundos, err = q.ClaimDueTargets(ctx2, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		return err
	})
	if err != nil {
		t.Fatalf("o segundo sondador esperou o primeiro: %v", err)
	}

	vistos := map[uuid.UUID]bool{}
	for _, r := range primeiros {
		vistos[r.ID] = true
	}
	for _, r := range segundos {
		if vistos[r.ID] {
			t.Errorf("alvo %s reivindicado pelos dois sondadores", r.ID)
		}
	}
	if len(primeiros)+len(segundos) != 10 {
		t.Errorf("reivindicados %d+%d, queria 10 no total", len(primeiros), len(segundos))
	}
}

func codigoPG(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
