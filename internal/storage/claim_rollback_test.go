package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Uma versão futura grava avisos de certificado sem incidente (SP1, §12). O
// binário anterior não sabe montar esse aviso: se a reivindicação o contasse,
// a linha sumiria no JOIN com incidents, o lease venceria e cada volta
// somaria uma tentativa até o aviso fechar como failed sem nunca ter saído.
func TestClaimSkipsNotificationWithoutIncident(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE notifications ALTER COLUMN incident_id DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}
	dono := novoUsuario(t, pool)
	canal := novoCanal(t, pool, dono, nil, time.Time{})
	comIncidente := novaNotificacao(t, pool, novoIncidente(t, pool, novoAlvo(t, pool, dono, agora)), canal)
	semIncidente := novaNotificacaoSemIncidente(t, pool, canal)

	var lote []db.ClaimDueNotificationsRow
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			// Um lugar só: o aviso sem incidente vence antes (11:59), então
			// sem o filtro ele ocuparia a vaga e o lote voltaria vazio.
			Now: agora, LeaseUntil: agora.Add(time.Minute), MaxRows: 1,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lote) != 1 || lote[0].ID != comIncidente {
		t.Fatalf("reivindicou %d avisos (%v); queria só o com incidente %v", len(lote), lote, comIncidente)
	}

	var status string
	var tentativas int
	var proxima time.Time
	noEscopoDoSondador(t, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status, attempts, next_attempt_at FROM notifications WHERE id = $1`, semIncidente,
		).Scan(&status, &tentativas, &proxima)
	})
	if status != "pending" || tentativas != 0 || !proxima.Equal(time.Date(2026, 9, 22, 11, 59, 0, 0, time.UTC)) {
		t.Errorf("aviso sem incidente ficou status=%s attempts=%d next_attempt_at=%s; queria pending, 0, 2026-09-22 11:59:00Z",
			status, tentativas, proxima.UTC())
	}
}

// novaNotificacaoSemIncidente grava o aviso como a versão futura o gravaria,
// com next_attempt_at literal no passado.
func novaNotificacaoSemIncidente(t *testing.T, pool *pgxpool.Pool, canal uuid.UUID) uuid.UUID {
	t.Helper()
	id := novoID()
	noEscopoDoSondador(t, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO notifications (id, incident_id, channel_id, event, next_attempt_at, created_at)
			 VALUES ($1, NULL, $2, 'opened', '2026-09-22 11:59:00+00', '2026-09-22 11:59:00+00')`,
			id, canal)
		return err
	})
	return id
}

// noEscopoDoSondador roda SQL cru que nenhuma consulta do sqlc cobre. A
// notifications tem RLS forçada: o papel da aplicação só a enxerga nesse escopo.
func noEscopoDoSondador(t *testing.T, pool *pgxpool.Pool, fn func(pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
