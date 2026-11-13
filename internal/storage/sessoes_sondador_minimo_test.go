package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O escopo do sondador fica no mínimo que a retenção usa (migration 00030):
// apaga só sessão já vencida, e lê só a vencida e a da família que tem
// contador de provas (o NOT EXISTS de DeleteDeadFamilyProofs). A sessão viva
// de um usuário sem contador fica invisível e intocável. Os prazos saem do
// now() do banco, porque a política compara com ele. Sabotagem: sem a 00030,
// o sondador vê e apaga a sessão viva.
func TestEscopoDoSondadorNaoLeNemApagaSessaoViva(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	viva, vivaComContador, vencida := uuid.New(), uuid.New(), uuid.New()
	revogadaComContador, usadaComContador := uuid.New(), uuid.New()
	familiaComContador := uuid.New()
	for _, s := range []struct {
		id, familia uuid.UUID
		prazo       string
	}{
		{viva, uuid.New(), "1 day"},
		{vivaComContador, familiaComContador, "1 day"},
		{vencida, uuid.New(), "-1 day"},
		{revogadaComContador, familiaComContador, "1 day"},
		{usadaComContador, familiaComContador, "1 day"},
	} {
		if _, err := testdb.Admin(t).Exec(ctx,
			`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			 VALUES ($1, $2, $3, $4, now() + $5::interval)`,
			s.id, dono, s.familia, []byte(s.id.String()), s.prazo); err != nil {
			t.Fatal(err)
		}
	}
	// O NOT EXISTS de DeleteDeadFamilyProofs só considera sessão não usada e não
	// revogada; a política acompanha, e a usada ou revogada fica invisível.
	if _, err := testdb.Admin(t).Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1`, revogadaComContador); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx,
		`UPDATE sessions SET used_at = now() WHERE id = $1`, usadaComContador); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		 VALUES ($1, $2, $3, 1, 1, now())`, familiaComContador, dono, uuid.New()); err != nil {
		t.Fatal(err)
	}

	comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, viva).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("o sondador lê a sessão viva de um usuário (%d linha)", n)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, vivaComContador).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("o sondador vê %d sessões da família com contador; queria 1, a retenção dos contadores precisa dela", n)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, revogadaComContador).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("o sondador lê a sessão revogada da família com contador (%d linha)", n)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, usadaComContador).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("o sondador lê a sessão usada da família com contador (%d linha)", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM sessions WHERE id IN ('`+viva.String()+`', '`+vivaComContador.String()+`')`); n != 0 {
			t.Errorf("o sondador apagou %d sessões vivas", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM sessions WHERE id = '`+vencida.String()+`'`); n != 1 {
			t.Errorf("o sondador apagou %d sessões vencidas; queria 1", n)
		}
	}, true)

	var restam int
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE id IN ($1, $2)`, viva, vivaComContador).Scan(&restam); err != nil {
		t.Fatal(err)
	}
	if restam != 2 {
		t.Errorf("restaram %d das 2 sessões vivas", restam)
	}
}
