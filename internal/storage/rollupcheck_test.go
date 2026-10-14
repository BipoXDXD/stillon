package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O banco amarra as latências do rollup como a consolidação as calcula: as
// quatro ficam NULL juntas (hora sem sucesso) ou presentes juntas, nunca
// negativas, e em ordem p50 <= p95 <= p99 <= max (percentis da mesma amostra).
// Uma escrita fora da consulta gerada não consegue gravar um rollup que o
// painel mostraria como uma curva impossível.
func TestRollupLatenciesMustBeCoherent(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	for i, tc := range []struct {
		nome                string
		ok                  int32
		p50, p95, p99, maxi *int32
		recusa              bool
	}{
		{"latencias_em_ordem", 2, ptr(10), ptr(20), ptr(30), ptr(40), false},
		{"latencias_iguais", 1, ptr(0), ptr(0), ptr(0), ptr(0), false},
		{"hora_sem_sucesso", 0, nil, nil, nil, nil, false},
		{"p95_abaixo_do_p50", 2, ptr(20), ptr(10), ptr(30), ptr(40), true},
		{"p99_abaixo_do_p95", 2, ptr(10), ptr(30), ptr(20), ptr(40), true},
		{"max_abaixo_do_p99", 2, ptr(10), ptr(20), ptr(40), ptr(30), true},
		{"p50_negativo", 2, ptr(-1), ptr(0), ptr(0), ptr(0), true},
		{"p95_ausente_com_sucesso", 2, ptr(10), nil, ptr(30), ptr(40), true},
		{"p99_ausente_com_sucesso", 2, ptr(10), ptr(20), nil, ptr(40), true},
		{"max_ausente_com_sucesso", 2, ptr(10), ptr(20), ptr(30), nil, true},
		{"max_presente_sem_sucesso", 0, nil, nil, nil, ptr(40), true},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			bucket := agora.Add(time.Duration(i) * time.Hour)
			err := inserirRollup(t, pool, dono, alvo, bucket, tc.ok, tc.p50, tc.p95, tc.p99, tc.maxi)
			switch {
			case tc.recusa && !violaCheck(err):
				t.Errorf("rollup incoerente: erro = %v, queria check_violation (23514)", err)
			case !tc.recusa && err != nil:
				t.Errorf("rollup coerente recusado: %v", err)
			}
		})
	}
}

// A 00013 roda no boot sobre os rollups que produção já tem. Uma linha antiga
// fora da regra não pode derrubar o boot: os CHECKs entram NOT VALID e só
// valem para as escritas novas. O down tira os dois CHECKs.
func TestRollupLatencyMigrationKeepsOldRowsAndGuardsNewOnes(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateDownTo(ctx, dsn, 12); err != nil {
		t.Fatalf("down da 00013 com a tabela vazia: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := inserirRollup(t, pool, dono, alvo, agora, 2, ptr(20), ptr(10), nil, ptr(5)); err != nil {
		t.Fatalf("linha antiga, gravada antes da 00013: %v", err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, 13); err != nil {
		t.Fatalf("a 00013 não sobe sobre uma linha antiga incoerente: %v", err)
	}
	if err := inserirRollup(t, pool, dono, alvo, agora.Add(time.Hour), 2, ptr(20), ptr(10), nil, ptr(5)); !violaCheck(err) {
		t.Errorf("rollup novo incoerente depois da 00013: erro = %v, queria check_violation (23514)", err)
	}

	if err := storage.MigrateDownTo(ctx, dsn, 12); err != nil {
		t.Fatalf("down da 00013 com a linha antiga: %v", err)
	}
	if err := inserirRollup(t, pool, dono, alvo, agora.Add(2*time.Hour), 2, ptr(20), ptr(10), nil, ptr(5)); err != nil {
		t.Errorf("depois do down o CHECK ainda recusa: %v", err)
	}
}

// A 00014 valida os dois CHECKs: o Postgres passa a garanti-los também nas
// linhas que já existiam. Produção só tem linhas gravadas depois da 00013,
// então o VALIDATE não acha nada fora da regra. O down volta ao NOT VALID da
// 00013, que continua barrando a escrita nova.
func TestRollupLatencyChecksAreValidated(t *testing.T) {
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
	if got := checksValidados(t, pool); got != 2 {
		t.Fatalf("CHECKs de latência validados depois da 00014 = %d, queria 2", got)
	}

	if err := storage.MigrateDownTo(ctx, dsn, 13); err != nil {
		t.Fatalf("down da 00014: %v", err)
	}
	if got := checksValidados(t, pool); got != 0 {
		t.Errorf("CHECKs de latência validados depois do down da 00014 = %d, queria 0 (NOT VALID)", got)
	}
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := inserirRollup(t, pool, dono, alvo, agora, 2, ptr(20), ptr(10), nil, ptr(5)); !violaCheck(err) {
		t.Errorf("rollup incoerente depois do down da 00014: erro = %v, queria check_violation (23514)", err)
	}
}

func checksValidados(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_constraint
		 WHERE conrelid = 'check_rollup_1h'::regclass
		   AND conname IN ('check_rollup_1h_latencias_juntas', 'check_rollup_1h_latencias_em_ordem')
		   AND convalidated`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ptr(n int32) *int32 { return &n }

func violaCheck(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// inserirRollup grava um rollup cru no escopo do dono (a RLS do rollup passa
// pelo alvo), por fora da consolidação.
func inserirRollup(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID, bucket time.Time,
	ok int32, p50, p95, p99, maxi *int32) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, $3, $4, $5, $6, $7)`,
		alvo, bucket, ok, p50, p95, p99, maxi); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
