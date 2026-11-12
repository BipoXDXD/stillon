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

// A conexão atravessa a RLS de sessions (00029): aqui o WHERE é a única
// barreira. O teste segue o mesmo espelho dos outros recursos.

// novaSessaoViva grava a linha viva de uma família do dono, com SQL cru.
func novaSessaoViva(t *testing.T, conn *pgx.Conn, dono uuid.UUID) (sessao, familia uuid.UUID) {
	t.Helper()
	sessao, familia = novoID(), novoID()
	if _, err := conn.Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		sessao, dono, familia, hashDe(sessao.String()), agora.Add(time.Hour)); err != nil {
		t.Fatalf("criar sessão: %v", err)
	}
	return sessao, familia
}

func TestRevokeFamilyOfUserFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	sessao, familia := novaSessaoViva(t, conn, a)
	revogar := func(dono uuid.UUID) int64 {
		t.Helper()
		n, err := q.RevokeFamilyOfUser(ctx, db.RevokeFamilyOfUserParams{Now: agora, UserID: dono, FamilyID: familia})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	revogadaEm := func() *time.Time {
		t.Helper()
		var em *time.Time
		if err := conn.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id = $1`, sessao).Scan(&em); err != nil {
			t.Fatal(err)
		}
		return em
	}

	if n := revogar(b); n != 0 {
		t.Errorf("B revogou %d sessões da família de A, queria 0", n)
	}
	if em := revogadaEm(); em != nil {
		t.Errorf("depois da revogação de B, a sessão de A ficou revogada em %s", em)
	}
	if n := revogar(a); n != 1 {
		t.Errorf("A revogou %d sessões da própria família, queria 1", n)
	}
	if em := revogadaEm(); em == nil || !em.Equal(agora) {
		t.Errorf("A revogou a própria família e revoked_at ficou %v, queria %s", em, agora)
	}
}

func TestFamilyIsLiveFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	_, familia := novaSessaoViva(t, conn, a)
	viva := func(dono uuid.UUID) bool {
		t.Helper()
		ok, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: dono, FamilyID: familia, Now: agora})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if viva(b) {
		t.Error("B viu viva a família de A, queria false")
	}
	if !viva(a) {
		t.Error("A não viu viva a própria família, queria true")
	}
}
