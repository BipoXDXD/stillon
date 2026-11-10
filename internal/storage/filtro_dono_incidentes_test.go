package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para o histórico de incidentes de alvos.

// ackDoIncidente lê ack_at com SQL cru, para não depender das queries testadas.
func ackDoIncidente(t *testing.T, conn *pgx.Conn, incidente uuid.UUID) *time.Time {
	t.Helper()
	var ack *time.Time
	if err := conn.QueryRow(context.Background(), `SELECT ack_at FROM incidents WHERE id = $1`, incidente).Scan(&ack); err != nil {
		t.Fatal(err)
	}
	return ack
}

func TestAckIncidentFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	incidente := novoIncidente(t, pool, alvoA)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	reconhecer := func(dono uuid.UUID) (db.AckIncidentRow, error) {
		return q.AckIncident(ctx, db.AckIncidentParams{Now: agora, UserID: dono, ID: incidente})
	}

	_, err := reconhecer(b)
	exigirNenhumaLinha(t, err, "AckIncident")
	if ack := ackDoIncidente(t, conn, incidente); ack != nil {
		t.Errorf("depois do ack de B sem RLS, o incidente de A ficou com ack_at %s, queria nulo", ack)
	}
	ack, err := reconhecer(a)
	if err != nil {
		t.Fatalf("A reconhecendo o próprio incidente: %v", err)
	}
	if ack.ID != incidente || ack.TargetID != alvoA || ack.AckAt == nil || !ack.AckAt.Equal(agora) {
		t.Errorf("A reconheceu e recebeu %+v, queria o incidente %s do alvo %s com ack em %s", ack, incidente, alvoA, agora)
	}
}

// O filtro target_id de ListIncidents com um alvo alheio: o comentário da
// query promete lista vazia, e sem RLS só o user_id do WHERE cumpre.
func TestListIncidentsByTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	incidente := novoIncidente(t, pool, alvoA)
	q := queriesSemRLS(t)
	listar := func(dono uuid.UUID) []db.ListIncidentsRow {
		t.Helper()
		inc, err := q.ListIncidents(ctx, db.ListIncidentsParams{UserID: dono, TargetID: &alvoA, MaxRows: 10})
		if err != nil {
			t.Fatal(err)
		}
		return inc
	}

	if inc := listar(b); len(inc) != 0 {
		t.Errorf("B leu %d incidentes do alvo de A sem RLS, queria 0", len(inc))
	}
	if inc := listar(a); len(inc) != 1 || inc[0].ID != incidente || inc[0].TargetID != alvoA {
		t.Errorf("A leu %+v, queria só o incidente %s do alvo %s", inc, incidente, alvoA)
	}
}
