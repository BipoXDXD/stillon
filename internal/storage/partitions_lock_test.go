package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As duas funções precisam da MESMA trava de transação. Conferir só o
// texto SQL não prova espera, nem que a trava sobrevive ao fim da query.
func TestPartitionCreationAndRetentionShareTheirTransactionLock(t *testing.T) {
	for _, caso := range []struct {
		nome       string
		segurar    string
		esperar    string
		criaDepois bool
	}{
		{
			nome:    "criação bloqueia retenção",
			segurar: `SELECT ensure_checks_partition('2030-01-01'::date)`,
			esperar: `SELECT drop_checks_partitions_before('1950-01-01'::timestamptz)`,
		},
		{
			nome:       "retenção bloqueia criação",
			segurar:    `SELECT drop_checks_partitions_before('1950-01-01'::timestamptz)`,
			esperar:    `SELECT ensure_checks_partition('2030-03-01'::date)`,
			criaDepois: true,
		},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := storage.EnsureChecksPartitions(ctx, pool,
				time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}

			dona, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer dona.Release()
			op, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}

			tx, err := dona.Begin(ctx)
			if err != nil {
				op.Release()
				t.Fatal(err)
			}
			var fim chan error
			terminou := false
			defer func() {
				cancel()
				limpeza, encerrar := context.WithTimeout(context.Background(), 3*time.Second)
				defer encerrar()
				_ = tx.Rollback(limpeza)
				if fim != nil && !terminou {
					select {
					case <-fim:
					case <-limpeza.Done():
						t.Error("a operação não encerrou após cancelamento e rollback da dona")
					}
				}
				op.Release()
			}()

			// O corte antigo não apaga nenhuma partição: se as chaves
			// divergirem, uma trava de DDL não pode mascarar a falta da advisory.
			if _, err := tx.Exec(ctx, caso.segurar); err != nil {
				t.Fatal(err)
			}
			donaPID := int32(dona.Conn().PgConn().PID())
			opPID := int32(op.Conn().PgConn().PID())
			fim = make(chan error, 1)
			go func() {
				_, err := op.Exec(ctx, caso.esperar)
				fim <- err
			}()

			espera, pararEspera := context.WithTimeout(ctx, 4*time.Second)
			defer pararEspera()
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			bloqueada := false
			for !bloqueada {
				select {
				case err := <-fim:
					terminou = true
					t.Fatalf("operação terminou com a transação dona aberta: %v; deveria esperar a mesma advisory lock", err)
				default:
				}
				if err := pool.QueryRow(espera, `SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity
					 WHERE pid = $1 AND wait_event_type = 'Lock' AND wait_event = 'advisory'
					   AND $2::int = ANY(pg_blocking_pids(pid)))`, opPID, donaPID).Scan(&bloqueada); err != nil {
					t.Fatalf("observar a conexão testada: %v", err)
				}
				if !bloqueada {
					select {
					case <-tick.C:
					case <-espera.Done():
						t.Fatal("a conexão testada não esperou a advisory lock da transação dona")
					}
				}
			}
			t.Logf("backend %d espera advisory lock do backend %d após a query da dona terminar", opPID, donaPID)

			if caso.criaDepois {
				var criada bool
				if err := pool.QueryRow(ctx, `SELECT to_regclass('checks_2030_03') IS NOT NULL`).Scan(&criada); err != nil {
					t.Fatal(err)
				}
				if criada {
					t.Fatal("a criação avançou antes do commit da retenção")
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-fim:
				terminou = true
				if err != nil {
					t.Fatalf("depois do commit da dona: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("a operação não terminou depois do commit da dona")
			}
			if caso.criaDepois {
				var criada bool
				if err := pool.QueryRow(ctx, `SELECT to_regclass('checks_2030_03') IS NOT NULL`).Scan(&criada); err != nil {
					t.Fatal(err)
				}
				if !criada {
					t.Fatal("a partição não foi criada depois do commit da retenção")
				}
			}
			var travas int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks
				WHERE locktype = 'advisory' AND pid IN ($1, $2)`, donaPID, opPID).Scan(&travas); err != nil {
				t.Fatal(err)
			}
			if travas != 0 {
				t.Fatalf("%d trava(s) advisory ficaram depois das transações", travas)
			}
		})
	}
}
