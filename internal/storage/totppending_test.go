package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A relação é do dado persistido, não apenas do serviço de troca de 2FA.
// Retirar o CHECK permite INSERT/UPDATE incompletos e deixa o teste vermelho.
func TestTOTPPendingSecretAndFamilyMustStayTogether(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	familia := uuid.New()
	segredo := []byte{1, 2, 3} // sentinela pública; aqui se testa presença, não criptografia

	for _, tc := range []struct {
		nome    string
		segredo []byte
		familia any
		recusa  bool
	}{
		{"sem_troca", nil, nil, false},
		{"troca_completa", segredo, familia, false},
		{"segredo_sem_familia", segredo, nil, true},
		{"familia_sem_segredo", nil, familia, true},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			id := uuid.New()
			_, err := pool.Exec(ctx, `INSERT INTO users
				(id, email, password_hash, totp_pending_secret_enc, totp_pending_family_id)
				VALUES ($1, $2, 'hash-do-teste', $3, $4)`,
				id, id.String()+"@stillon.example", tc.segredo, tc.familia)
			if tc.recusa {
				exigirPendenteIncompletoRecusado(t, err)
			} else if err != nil {
				t.Fatalf("estado consistente recusado: %v", err)
			}
		})
	}

	dono := novoUsuario(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_pending_secret_enc = $2,
		totp_pending_family_id = $3 WHERE id = $1`, dono, segredo, familia); err != nil {
		t.Fatal(err)
	}
	for _, coluna := range []string{"totp_pending_secret_enc", "totp_pending_family_id"} {
		t.Run("limpeza_parcial_"+coluna, func(t *testing.T) {
			// Os dois identificadores vêm desta lista constante, sem entrada externa.
			_, err := pool.Exec(ctx, "UPDATE users SET "+coluna+" = NULL WHERE id = $1", dono)
			exigirPendenteIncompletoRecusado(t, err)
		})
	}
	var completo bool
	if err := pool.QueryRow(ctx, `SELECT totp_pending_secret_enc IS NOT NULL AND
		totp_pending_family_id = $2 FROM users WHERE id = $1`, dono, familia).Scan(&completo); err != nil {
		t.Fatal(err)
	}
	if !completo {
		t.Fatal("UPDATE recusado mudou o pendente original")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_pending_secret_enc = NULL,
		totp_pending_family_id = NULL WHERE id = $1`, dono); err != nil {
		t.Fatalf("limpeza conjunta recusada: %v", err)
	}
}

func exigirPendenteIncompletoRecusado(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "users_totp_pending_pair" {
		t.Errorf("pendente incompleto deveria violar users_totp_pending_pair (23514), recebeu %v", err)
	}
}
