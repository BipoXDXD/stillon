package storage_test

import (
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

func contarTokenDeConta(t *testing.T, hash []byte) int {
	t.Helper()
	var n int
	err := testdb.Admin(t).QueryRow(t.Context(), `SELECT count(*) FROM account_tokens WHERE token_hash = $1`, hash).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTokenDeContaInsercaoExigeEscopoDoDono(t *testing.T) {
	pool := testdb.New(t)
	ctx := t.Context()
	dono, outro := novoUsuario(t, pool), novoUsuario(t, pool)
	hash := hashDe("fixture-insercao")
	parametros := db.InsertAccountTokenParams{
		TokenHash: hash, UserID: dono, Purpose: propositoConfirmar,
		CreatedAt: agora, ExpiresAt: agora.Add(24 * time.Hour),
	}

	inserir := func(q *db.Queries) error {
		return q.InsertAccountToken(ctx, parametros)
	}
	if err := storage.InUserTx(ctx, pool, outro, inserir); codigoPG(err) != "42501" {
		t.Errorf("inserção fora do escopo do dono: %v; queria SQLSTATE 42501", err)
	}
	if n := contarTokenDeConta(t, hash); n != 0 {
		t.Fatalf("%d tokens depois da inserção recusada; queria 0", n)
	}
	if err := storage.InUserTx(ctx, pool, dono, inserir); err != nil {
		t.Fatalf("inserção no escopo do dono: %v", err)
	}
	if n := contarTokenDeConta(t, hash); n != 1 {
		t.Errorf("%d tokens depois da inserção autorizada; queria 1", n)
	}
}

func TestTokenDeContaRemocaoExigeEscopoDoDono(t *testing.T) {
	pool := testdb.New(t)
	ctx := t.Context()
	dono := novoUsuario(t, pool)
	hash := hashDe("fixture-remocao")
	gravarTokenDeConta(t, pool, dono, hash, propositoRedefinir, time.Hour)
	parametros := db.DeleteAccountTokensParams{UserID: dono, Purpose: propositoRedefinir}

	// A leitura confirma que a linha está visível à transação de remoção.
	err := storage.InAccountTokenTx(ctx, pool, hash, func(q *db.Queries) error {
		if _, err := q.PeekAccountToken(ctx, db.PeekAccountTokenParams{
			TokenHash: hash, Purpose: propositoRedefinir, Now: agora,
		}); err != nil {
			return err
		}
		return q.DeleteAccountTokens(ctx, parametros)
	})
	if err != nil {
		t.Fatalf("leitura e remoção no escopo do hash: %v", err)
	}
	if n := contarTokenDeConta(t, hash); n != 1 {
		t.Errorf("%d tokens depois da remoção no escopo do hash; queria 1", n)
	}
}
