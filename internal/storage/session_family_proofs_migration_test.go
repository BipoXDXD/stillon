package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration do contador de provas por família, achada pelo nome: o número é o
// próximo livre na hora do merge.
const sufixoContadorDeProvas = "session_family_proofs"

func violaFK(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// inserirContadorDeProvas grava no escopo do usuário, como a aplicação: a RLS
// da 00029 deixa passar e o que sobra para recusar são o CHECK e a FK.
func inserirContadorDeProvas(ctx context.Context, pool *pgxpool.Pool, usuario uuid.UUID, reservadas, erros int) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, usuario.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), usuario, uuid.New(), reservadas, erros, agora)
		return err
	})
}

// O banco recusa o contador torto: mais erros que reservas (cada erro é uma
// reserva), contagem negativa, ou um dono que não existe.
func TestSessionFamilyProofsChecksAndForeignKey(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)

	for _, tc := range []struct {
		nome               string
		reservadas, erros  int
		usuario            uuid.UUID
		recusa             bool
		codigoDeViolacaoOK func(error) bool
	}{
		{"zero_e_zero", 0, 0, dono, false, nil},
		{"erros_iguais_as_reservadas", 5, 5, dono, false, nil},
		{"mais_erros_que_reservadas", 4, 5, dono, true, violaCheck},
		{"reservadas_negativas", -1, 0, dono, true, violaCheck},
		{"erros_negativos", 1, -1, dono, true, violaCheck},
		{"dono_inexistente", 1, 0, uuid.New(), true, violaFK},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			err := inserirContadorDeProvas(ctx, pool, tc.usuario, tc.reservadas, tc.erros)
			switch {
			case tc.recusa && !tc.codigoDeViolacaoOK(err):
				t.Errorf("contador inválido: erro = %v, queria a violação esperada", err)
			case !tc.recusa && err != nil:
				t.Errorf("contador válido recusado: %v", err)
			}
		})
	}
}

// Reverter o deploy desce a migration só da tabela nova: usuários e sessões
// ficam como estavam, a revogação de sessão (a do binário anterior) segue
// funcionando, e subir de novo cria a tabela vazia.
func TestSessionFamilyProofsDownDropsOnlyTheNewTable(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoContadorDeProvas)
	dsn := testdb.EmptyDatabase(t)
	// Só até a 00028: a RLS da 00029 é de outro deploy e tem o próprio down.
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dono := novoUsuario(t, pool)
	familia := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), dono, familia, []byte("hash"), agora.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := inserirContadorDeProvas(ctx, pool, dono, 2, 1); err != nil {
		t.Fatal(err)
	}

	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}

	var tabelas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_class WHERE relname = 'session_family_proofs'`).Scan(&tabelas); err != nil {
		t.Fatal(err)
	}
	if tabelas != 0 {
		t.Errorf("depois do down: %d relações session_family_proofs, queria 0", tabelas)
	}
	tag, err := pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`, familia, agora)
	if err != nil || tag.RowsAffected() != 1 {
		t.Errorf("revogar a sessão sem a tabela nova: %d linhas, erro %v; queria 1 e nil", tag.RowsAffected(), err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
	var linhas int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session_family_proofs`).Scan(&linhas); err != nil || linhas != 0 {
		t.Errorf("depois de subir de novo: %d linhas (erro %v), queria a tabela vazia", linhas, err)
	}
}
