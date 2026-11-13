package storage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Uma linha de A em cada tabela de credencial (migration 00029), gravada no
// escopo de A como a aplicação grava.
type credenciaisDeA struct {
	dono      uuid.UUID
	tokenHash []byte
}

func gravarCredenciaisDeA(t *testing.T, pool *pgxpool.Pool) credenciaisDeA {
	t.Helper()
	a := novoUsuario(t, pool)
	hash := sha256.Sum256([]byte("refresh de A " + a.String()))
	familia := novoID()
	comEscopo(t, pool, "app.user_id", a.String(), func(tx pgx.Tx) {
		ctx := context.Background()
		if _, err := tx.Exec(ctx,
			`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
			novoID(), a, familia, hash[:], agora.Add(24*time.Hour)); err != nil {
			t.Fatalf("gravar sessão de A: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
			 VALUES ($1, $2, $3, 2, 1, $4)`, familia, a, novoID(), agora); err != nil {
			t.Fatalf("gravar contador de A: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, '\x0a')`, novoID(), a); err != nil {
			t.Fatalf("gravar código de A: %v", err)
		}
	}, true)
	return credenciaisDeA{dono: a, tokenHash: hash[:]}
}

// As consultas cruas, SEM o WHERE de dono que as queries da aplicação levam:
// só a política pode barrar. A coluna alterada por cada UPDATE é a própria,
// para que a contagem de linhas afetadas seja a única coisa observada.
var credenciais = []struct {
	tabela, update, delete, insertDeOutro string
}{
	{
		tabela: "sessions",
		update: `UPDATE sessions SET revoked_at = revoked_at`,
		delete: `DELETE FROM sessions`,
		insertDeOutro: `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
		                VALUES (gen_random_uuid(), $1, gen_random_uuid(), '\xff', now())`,
	},
	{
		tabela: "session_family_proofs",
		update: `UPDATE session_family_proofs SET errors = errors`,
		delete: `DELETE FROM session_family_proofs`,
		insertDeOutro: `INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		                VALUES (gen_random_uuid(), $1, gen_random_uuid(), 0, 0, now())`,
	},
	{
		tabela:        "totp_recovery_codes",
		update:        `UPDATE totp_recovery_codes SET code_hash = code_hash`,
		delete:        `DELETE FROM totp_recovery_codes`,
		insertDeOutro: `INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES (gen_random_uuid(), $1, '\x0b')`,
	},
}

// Com o papel da aplicação, B não lê, não altera, não apaga e não grava em
// nome de A nas três tabelas, mesmo sem WHERE; sem escopo nenhum, ninguém vê
// nada. A primeira verificação é a de A ver a própria linha: sem ela, uma
// tabela vazia passaria em todas as outras.
func TestCredenciaisDeOutroUsuarioFicamForaDaRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := gravarCredenciaisDeA(t, pool)
	b := novoUsuario(t, pool)

	for _, c := range credenciais {
		t.Run(c.tabela, func(t *testing.T) {
			if n := contarComEscopo(t, pool, a.dono, c.tabela); n != 1 {
				t.Fatalf("A vê %d linhas próprias em %s; queria 1", n, c.tabela)
			}
			if n := contarComEscopo(t, pool, b, c.tabela); n != 0 {
				t.Errorf("B, sem WHERE, vê %d linhas de A em %s", n, c.tabela)
			}
			if n := contarSemEscopo(t, pool, c.tabela); n != 0 {
				t.Errorf("sem escopo, %d linhas visíveis em %s", n, c.tabela)
			}
			comEscopo(t, pool, "app.user_id", b.String(), func(tx pgx.Tx) {
				if n := afetadasNaTx(t, tx, c.update); n != 0 {
					t.Errorf("B, sem WHERE, alterou %d linhas de A em %s", n, c.tabela)
				}
				if n := afetadasNaTx(t, tx, c.delete); n != 0 {
					t.Errorf("B, sem WHERE, apagou %d linhas de A em %s", n, c.tabela)
				}
			}, true)
			comEscopo(t, pool, "app.user_id", b.String(), func(tx pgx.Tx) {
				_, err := tx.Exec(ctx, c.insertDeOutro, a.dono)
				if codigoPG(err) != "42501" {
					t.Errorf("B gravar linha em nome de A em %s deu %v; queria 42501", c.tabela, err)
				}
			}, false)
			if n := contarComEscopo(t, pool, a.dono, c.tabela); n != 1 {
				t.Errorf("depois das tentativas de B, A tem %d linhas em %s; queria 1", n, c.tabela)
			}
		})
	}
}

// O escopo do refresh e do logout só LÊ a linha de sessions do hash
// apresentado: outro hash não vê nada, nenhuma escrita passa, e as outras
// duas tabelas ficam fechadas.
func TestEscopoDoTokenSoLeASessaoDoHash(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := gravarCredenciaisDeA(t, pool)

	comEscopo(t, pool, "app.session_token_hash", hex.EncodeToString(a.tokenHash), func(tx pgx.Tx) {
		var dono uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM sessions`).Scan(&dono); err != nil || dono != a.dono {
			t.Fatalf("pelo hash, dono = %v (%v); queria %v", dono, err, a.dono)
		}
		if n := afetadasNaTx(t, tx, `UPDATE sessions SET revoked_at = now()`); n != 0 {
			t.Errorf("o escopo do token alterou %d sessões", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM sessions`); n != 0 {
			t.Errorf("o escopo do token apagou %d sessões", n)
		}
		for _, tabela := range []string{"session_family_proofs", "totp_recovery_codes"} {
			var n int
			// tabela é um literal do próprio teste.
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil || n != 0 {
				t.Errorf("o escopo do token vê %d linhas em %s (%v)", n, tabela, err)
			}
		}
	}, true)

	outro := sha256.Sum256([]byte("outro refresh"))
	comEscopo(t, pool, "app.session_token_hash", hex.EncodeToString(outro[:]), func(tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
			t.Errorf("outro hash vê %d sessões (%v)", n, err)
		}
	}, true)
}

// O sondador (retenção e métrica) lê e apaga sessions e session_family_proofs
// de todos, mas não as altera nem grava, e não vê os códigos de recuperação.
func TestEscopoDoSondadorSoLeEApagaSessoesEContadores(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := gravarCredenciaisDeA(t, pool)

	comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
		for _, c := range credenciais {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+c.tabela).Scan(&n); err != nil {
				t.Fatal(err)
			}
			queria := 1
			if c.tabela == "totp_recovery_codes" {
				queria = 0
			}
			if n != queria {
				t.Errorf("o sondador vê %d linhas em %s; queria %d", n, c.tabela, queria)
			}
			if n := afetadasNaTx(t, tx, c.update); n != 0 {
				t.Errorf("o sondador alterou %d linhas em %s", n, c.tabela)
			}
		}
	}, true)
	comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
		_, err := tx.Exec(ctx, credenciais[0].insertDeOutro, a.dono)
		if codigoPG(err) != "42501" {
			t.Errorf("o sondador gravar sessão deu %v; queria 42501", err)
		}
	}, false)
	comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
		if n := afetadasNaTx(t, tx, `DELETE FROM session_family_proofs`); n != 1 {
			t.Errorf("o sondador apagou %d contadores; queria 1", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM sessions`); n != 1 {
			t.Errorf("o sondador apagou %d sessões; queria 1", n)
		}
		if n := afetadasNaTx(t, tx, `DELETE FROM totp_recovery_codes`); n != 0 {
			t.Errorf("o sondador apagou %d códigos de recuperação; queria 0", n)
		}
	}, true)
}

// comEscopo roda fn numa transação com a GUC definida como SET LOCAL, igual aos
// escopos de storage. commit=false termina em rollback: é para o fn que provoca
// um erro e deixa a transação abortada.
func comEscopo(t *testing.T, pool *pgxpool.Pool, guc, valor string, fn func(pgx.Tx), commit bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit
	if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, guc, valor); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit no escopo %s: %v", guc, err)
		}
	}
}

func afetadasNaTx(t *testing.T, tx pgx.Tx, sql string) int64 {
	t.Helper()
	tag, err := tx.Exec(context.Background(), sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return tag.RowsAffected()
}
