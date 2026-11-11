package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Limpar o log da tarefa alheia passa por três camadas: o exists() do
// serviço, o filtro h.user_id da query e a RLS FORCE. Os testes de HTTP só
// quebram derrubando as três; aqui cada camada do banco é provada sozinha.

// eventosDe conta os eventos de um heartbeat no escopo de um usuário.
func eventosDe(t *testing.T, pool *pgxpool.Pool, userID, heartbeat uuid.UUID) int {
	t.Helper()
	var n int
	err := emEscopo(t, pool, "app.user_id", userID.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM heartbeat_events WHERE heartbeat_id = $1`, heartbeat).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// desligarRLSDoDono tira o FORCE: o papel da aplicação é dono das tabelas e
// passa a ignorar as políticas. O snapshot do testdb desfaz no fim do teste.
func desligarRLSDoDono(t *testing.T, pool *pgxpool.Pool, tabelas ...string) {
	t.Helper()
	for _, tabela := range tabelas {
		// tabela é sempre um literal do próprio teste.
		if _, err := pool.Exec(context.Background(), `ALTER TABLE `+tabela+` NO FORCE ROW LEVEL SECURITY`); err != nil {
			t.Fatal(err)
		}
	}
}

// Sem a RLS, só o filtro h.user_id separa B do log de A.
func TestDeleteHeartbeatEventsFiltersByOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hbA := novoHeartbeat(t, pool, a, "a")
	novoEvento(t, pool, hbA)
	desligarRLSDoDono(t, pool, "heartbeats", "heartbeat_events")
	if n := eventosDe(t, pool, b, hbA); n != 1 {
		t.Fatalf("sem RLS, B devia enxergar o evento de A e viu %d: o teste não provaria o filtro", n)
	}

	var apagadas int64
	err := storage.InUserTx(ctx, pool, b, func(q *db.Queries) error {
		var err error
		apagadas, err = q.DeleteHeartbeatEvents(ctx, db.DeleteHeartbeatEventsParams{UserID: b, HeartbeatID: hbA})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if apagadas != 0 {
		t.Errorf("B apagou %d eventos de A, queria 0", apagadas)
	}
	if n := eventosDe(t, pool, a, hbA); n != 1 {
		t.Errorf("A ficou com %d eventos, queria 1", n)
	}
}

// Sem o filtro (um DELETE cru), só a RLS separa B do log de A.
func TestRawDeleteOfAnotherUsersHeartbeatEventsAffectsNothing(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hbA := novoHeartbeat(t, pool, a, "a")
	novoEvento(t, pool, hbA)

	apagar := func(quem uuid.UUID) int64 {
		t.Helper()
		var apagadas int64
		err := emEscopo(t, pool, "app.user_id", quem.String(), func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `DELETE FROM heartbeat_events WHERE heartbeat_id = $1`, hbA)
			apagadas = tag.RowsAffected()
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return apagadas
	}
	if n := apagar(b); n != 0 {
		t.Errorf("o DELETE cru de B apagou %d eventos de A, queria 0", n)
	}
	if n := eventosDe(t, pool, a, hbA); n != 1 {
		t.Errorf("A ficou com %d eventos, queria 1", n)
	}
	// O mesmo DELETE no escopo do dono apaga: o 0 acima é da política, não de
	// uma consulta que não casa com nada.
	if n := apagar(a); n != 1 {
		t.Errorf("o DELETE cru de A apagou %d eventos, queria 1", n)
	}
}
