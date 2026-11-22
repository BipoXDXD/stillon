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

// Espelho de filtro_dono_test.go para o histórico de incidentes de alvos:
// superusuário, sem RLS, e só o project_id do WHERE separa A de B. A e B são
// dois projetos do MESMO usuário: o filtro por dono os deixaria passar.

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
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	incidente := novoIncidente(t, pool, alvoA)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	reconhecer := func(projeto uuid.UUID) (db.AckIncidentRow, error) {
		return q.AckIncident(ctx, db.AckIncidentParams{Now: agora, ProjectID: projeto, ID: incidente})
	}

	_, err := reconhecer(pb)
	exigirNenhumaLinha(t, err, "AckIncident")
	if ack := ackDoIncidente(t, conn, incidente); ack != nil {
		t.Errorf("depois do ack de B sem RLS, o incidente de A ficou com ack_at %s, queria nulo", ack)
	}
	ack, err := reconhecer(pa)
	if err != nil {
		t.Fatalf("A reconhecendo o próprio incidente: %v", err)
	}
	if ack.ID != incidente || ack.TargetID != alvoA || ack.AckAt == nil || !ack.AckAt.Equal(agora) {
		t.Errorf("A reconheceu e recebeu %+v, queria o incidente %s do alvo %s com ack em %s", ack, incidente, alvoA, agora)
	}
}

// O filtro target_id de ListIncidents com um alvo de outro projeto: o
// comentário da query promete lista vazia, e sem RLS só o project_id do WHERE
// cumpre. O segundo alvo de A, com incidente próprio, prova que o filtro por
// alvo também age: sem ele, A leria os dois incidentes.
func TestListIncidentsByTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	incidente := novoIncidente(t, pool, alvoA)
	novoIncidente(t, pool, novoAlvo(t, pool, a, agora))
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.ListIncidentsRow {
		t.Helper()
		inc, err := q.ListIncidents(ctx, db.ListIncidentsParams{ProjectID: projeto, TargetID: &alvoA, MaxRows: 10})
		if err != nil {
			t.Fatal(err)
		}
		return inc
	}

	if inc := listar(pb); len(inc) != 0 {
		t.Errorf("B leu %d incidentes do alvo de A sem RLS, queria 0", len(inc))
	}
	if inc := listar(pa); len(inc) != 1 || inc[0].ID != incidente || inc[0].TargetID != alvoA {
		t.Errorf("A leu %+v, queria só o incidente %s do alvo %s", inc, incidente, alvoA)
	}
}
