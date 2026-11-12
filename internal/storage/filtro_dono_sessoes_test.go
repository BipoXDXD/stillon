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

// sessions não tem RLS: aqui o WHERE é a única barreira, com
// ou sem superusuário. O teste segue o mesmo espelho dos outros recursos.

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

// revogacaoDaSessao lê revoked_at com SQL cru, para não depender das queries testadas.
func revogacaoDaSessao(t *testing.T, conn *pgx.Conn, sessao uuid.UUID) *time.Time {
	t.Helper()
	var em *time.Time
	if err := conn.QueryRow(context.Background(), `SELECT revoked_at FROM sessions WHERE id = $1`, sessao).Scan(&em); err != nil {
		t.Fatal(err)
	}
	return em
}

func TestRevokeAllFamiliesFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	sessao, _ := novaSessaoViva(t, conn, a)
	revogar := func(dono uuid.UUID) int64 {
		t.Helper()
		n, err := q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{Now: agora, UserID: dono})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := revogar(b); n != 0 {
		t.Errorf("B revogou %d sessões de A, queria 0", n)
	}
	if em := revogacaoDaSessao(t, conn, sessao); em != nil {
		t.Errorf("depois da revogação de B, a sessão de A ficou revogada em %s", em)
	}
	if n := revogar(a); n != 1 {
		t.Errorf("A revogou %d sessões próprias, queria 1", n)
	}
	if em := revogacaoDaSessao(t, conn, sessao); em == nil || !em.Equal(agora) {
		t.Errorf("A revogou as próprias sessões e revoked_at ficou %v, queria %s", em, agora)
	}
}

// A troca de senha de B derruba as outras famílias DE B. A família mantida é
// uma qualquer, para que só o user_id do WHERE proteja a sessão de A.
func TestRevokeOtherFamiliesFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	sessao, _ := novaSessaoViva(t, conn, a)
	revogar := func(dono uuid.UUID) int64 {
		t.Helper()
		n, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{Now: agora, UserID: dono, KeepFamilyID: novoID()})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := revogar(b); n != 0 {
		t.Errorf("B revogou %d sessões de A, queria 0", n)
	}
	if em := revogacaoDaSessao(t, conn, sessao); em != nil {
		t.Errorf("depois da troca de senha de B, a sessão de A ficou revogada em %s", em)
	}
	if n := revogar(a); n != 1 {
		t.Errorf("A revogou %d das próprias outras famílias, queria 1", n)
	}
	if em := revogacaoDaSessao(t, conn, sessao); em == nil || !em.Equal(agora) {
		t.Errorf("A revogou as outras famílias e revoked_at ficou %v, queria %s", em, agora)
	}
}

func TestListLiveFamiliesFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	_, familia := novaSessaoViva(t, conn, a)
	listar := func(dono uuid.UUID) []db.ListLiveFamiliesRow {
		t.Helper()
		f, err := q.ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: dono, Now: agora})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	if deB := listar(b); len(deB) != 0 {
		t.Errorf("B listou %d famílias de A, queria 0", len(deB))
	}
	if deA := listar(a); len(deA) != 1 || deA[0].FamilyID != familia {
		t.Errorf("A listou %v, queria só a família %s", deA, familia)
	}
}

// session_family_proofs não tem RLS, e a reserva só nasce para uma família
// viva DO usuário pedido: B reservando a família de A não cria a linha.
func TestReserveFamilyProofFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	_, familia := novaSessaoViva(t, conn, a)
	geracao := novoID()
	reservar := func(dono uuid.UUID) (db.ReserveFamilyProofRow, error) {
		return q.ReserveFamilyProof(ctx, db.ReserveFamilyProofParams{
			FamilyID: familia, UserID: dono, Generation: geracao, Now: agora,
		})
	}
	linhas := func() int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM session_family_proofs WHERE family_id = $1`, familia).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	_, err := reservar(b)
	exigirNenhumaLinha(t, err, "ReserveFamilyProof")
	if n := linhas(); n != 0 {
		t.Errorf("a reserva de B criou %d linhas para a família de A, queria 0", n)
	}
	r, err := reservar(a)
	if err != nil {
		t.Fatalf("A reservando a própria família: %v", err)
	}
	if r.Generation != geracao || r.Reserved != 1 {
		t.Errorf("A reservou e recebeu %+v, queria a geração %s com 1 reserva", r, geracao)
	}
}
