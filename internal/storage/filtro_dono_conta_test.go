package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para as tabelas da conta. totp_recovery_codes
// não tem RLS: o user_id do WHERE é a única barreira.

func TestDeleteRecoveryCodesFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	if _, err := conn.Exec(ctx, `INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
		novoID(), a, hashDe("codigo-de-a")); err != nil {
		t.Fatal(err)
	}
	codigos := func(dono uuid.UUID) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, dono).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := q.DeleteRecoveryCodes(ctx, b); err != nil {
		t.Fatalf("DeleteRecoveryCodes de B: %v", err)
	}
	if n := codigos(a); n != 1 {
		t.Errorf("depois do DELETE de B, A ficou com %d códigos, queria 1", n)
	}
	if err := q.DeleteRecoveryCodes(ctx, a); err != nil {
		t.Fatalf("DeleteRecoveryCodes de A: %v", err)
	}
	if n := codigos(a); n != 0 {
		t.Errorf("A apagou os próprios códigos e ficou com %d, queria 0", n)
	}
}
