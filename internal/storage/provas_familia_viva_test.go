package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O sondador só apaga o contador de provas de família sem sessão viva
// (migration 00032): a política de DELETE repete o NOT EXISTS de
// DeleteDeadFamilyProofs, então nem um DELETE direto, sem o filtro da query,
// zera o limite de provas de uma família ativa. Os prazos saem do now() do
// banco, porque a política compara com ele. Sabotagem: sem a 00032, o DELETE
// direto apaga o contador da viva; sem o expires_at > now() na política, ele
// não apaga o da família vencida.
func TestSondadorNaoApagaContadorDeProvasDeFamiliaViva(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	viva, revogada, vencida, usada := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, s := range []struct {
		familia       uuid.UUID
		prazo         string
		revogar, usar bool
	}{
		{viva, "1 day", false, false},
		{revogada, "1 day", true, false},
		{vencida, "-1 second", false, false},
		{usada, "1 day", false, true},
	} {
		if _, err := testdb.Admin(t).Exec(ctx,
			`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, revoked_at, used_at)
			 VALUES ($1, $2, $3, $4, now() + $5::interval,
			         CASE WHEN $6 THEN now() END, CASE WHEN $7 THEN now() END)`,
			uuid.New(), dono, s.familia, []byte(s.familia.String()), s.prazo, s.revogar, s.usar); err != nil {
			t.Fatal(err)
		}
	}
	for _, familia := range []uuid.UUID{viva, revogada, vencida, usada} {
		if _, err := testdb.Admin(t).Exec(ctx,
			`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
			 VALUES ($1, $2, $3, 4, 4, $4)`, familia, dono, uuid.New(), agora.Add(-10*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
		if n := afetadasNaTx(t, tx, `DELETE FROM session_family_proofs WHERE family_id = '`+viva.String()+`'`); n != 0 {
			t.Errorf("o sondador apagou %d contadores da família viva; queria 0", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM session_family_proofs WHERE family_id = '`+revogada.String()+`'`); n != 1 {
			t.Errorf("o sondador apagou %d contadores da família revogada; queria 1", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM session_family_proofs WHERE family_id = '`+vencida.String()+`'`); n != 1 {
			t.Errorf("o sondador apagou %d contadores da família vencida; queria 1", n)
		}
	}, true)

	// A query da retenção continua apagando a família morta que sobrou. A
	// sessão já usada a mata em qualquer relógio, inclusive no agora fixo.
	r, err := storage.ApplyRetention(ctx, pool, agora)
	if err != nil {
		t.Fatal(err)
	}
	if r.DeletedFamilyProofs != 1 {
		t.Errorf("a retenção apagou %d contadores; queria 1, o da família de sessão usada", r.DeletedFamilyProofs)
	}

	rows, err := testdb.Admin(t).Query(ctx, `SELECT family_id FROM session_family_proofs`)
	if err != nil {
		t.Fatal(err)
	}
	restam, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	if len(restam) != 1 || restam[0] != viva {
		t.Errorf("restaram os contadores %v; queria só o da família viva %v", restam, viva)
	}
}
