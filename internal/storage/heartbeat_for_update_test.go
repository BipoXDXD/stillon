package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// GetHeartbeatForUpdate serve o PATCH, a rotação do token e o sinalizar agora.
// Os testes de HTTP só quebram derrubando o filtro da query e a RLS FORCE
// juntos; aqui o filtro user_id é provado sozinho, como no
// TestDeleteHeartbeatEventsFiltersByOwnerWithoutRLS.

// heartbeatsVisiveis conta as linhas de heartbeats com o id no escopo de um
// usuário, sem filtro de dono na query.
func heartbeatsVisiveis(t *testing.T, pool *pgxpool.Pool, userID, id uuid.UUID) int {
	t.Helper()
	var n int
	err := emEscopo(t, pool, "app.user_id", userID.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM heartbeats WHERE id = $1`, id).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Sem a RLS, só o filtro user_id da query separa B da linha de A.
func TestGetHeartbeatForUpdateFiltersByOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hbA := novoHeartbeat(t, pool, a, "a")
	desligarRLSDoDono(t, pool, "heartbeats")
	if n := heartbeatsVisiveis(t, pool, b, hbA); n != 1 {
		t.Fatalf("sem RLS, B devia enxergar a linha de A e viu %d: o teste não provaria o filtro", n)
	}

	err := storage.InUserTx(ctx, pool, b, func(q *db.Queries) error {
		_, err := q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{UserID: b, ID: hbA})
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B leu a linha de A para atualizar: erro %v, queria pgx.ErrNoRows", err)
	}
}
