package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Os propósitos aceitos pelo CHECK de account_tokens (migration 00047).
const (
	propositoConfirmar = "confirm_email"
	propositoRedefinir = "reset_password"
)

// gravarTokenDeConta grava, no escopo do dono (como o cadastro faz), um token
// de propósito que vale de agora até agora+validade.
func gravarTokenDeConta(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, hash []byte, proposito string, validade time.Duration) {
	t.Helper()
	ctx := context.Background()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertAccountLink(ctx, db.InsertAccountLinkParams{
			TokenHash: hash, UserID: dono, Purpose: proposito,
			CreatedAt: agora, ExpiresAt: agora.Add(validade),
		})
	})
	if err != nil {
		t.Fatalf("gravar token de conta: %v", err)
	}
}

// espiarToken lê o dono do token no escopo do próprio hash.
func espiarToken(t *testing.T, pool *pgxpool.Pool, hash []byte, proposito string, em time.Time) (uuid.UUID, error) {
	t.Helper()
	ctx := context.Background()
	var dono uuid.UUID
	err := storage.InAccountTokenTx(ctx, pool, hash, func(q *db.Queries) error {
		var err error
		dono, err = q.PeekAccountLink(ctx, db.PeekAccountLinkParams{TokenHash: hash, Purpose: proposito, Now: em})
		return err
	})
	return dono, err
}

// consumirToken consome, no escopo de escopoHash, o token tokenHash.
func consumirToken(t *testing.T, pool *pgxpool.Pool, escopoHash, tokenHash []byte, proposito string, em time.Time) (uuid.UUID, error) {
	t.Helper()
	ctx := context.Background()
	var dono uuid.UUID
	err := storage.InAccountTokenTx(ctx, pool, escopoHash, func(q *db.Queries) error {
		var err error
		dono, err = q.ConsumeAccountLink(ctx, db.ConsumeAccountLinkParams{TokenHash: tokenHash, Purpose: proposito, Now: em})
		return err
	})
	return dono, err
}

func TestTokenDeContaSoApareceComOHashCerto(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hash := hashDe("token-certo")
	gravarTokenDeConta(t, pool, dono, hash, propositoConfirmar, 24*time.Hour)

	achado, err := espiarToken(t, pool, hash, propositoConfirmar, agora)
	if err != nil {
		t.Fatalf("espiar com o hash certo: %v", err)
	}
	if achado != dono {
		t.Errorf("dono do token = %v, queria %v", achado, dono)
	}

	// Com outro hash no escopo e no parâmetro, a linha não aparece.
	outro := hashDe("token-outro")
	if _, err := espiarToken(t, pool, outro, propositoConfirmar, agora); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("espiar com outro hash deu %v, queria pgx.ErrNoRows", err)
	}

	// Sem escopo nenhum, a RLS não deixa ver nada.
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM account_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sem escopo, account_tokens mostrou %d linhas, queria 0", n)
	}
}

func TestTokenDeContaConsumidoUmaVez(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hash := hashDe("uso-unico")
	gravarTokenDeConta(t, pool, dono, hash, propositoRedefinir, time.Hour)

	achado, err := consumirToken(t, pool, hash, hash, propositoRedefinir, agora)
	if err != nil {
		t.Fatalf("primeiro consumo: %v", err)
	}
	if achado != dono {
		t.Errorf("primeiro consumo devolveu %v, queria %v", achado, dono)
	}
	if _, err := consumirToken(t, pool, hash, hash, propositoRedefinir, agora); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("segundo consumo deu %v, queria pgx.ErrNoRows", err)
	}
}

func TestConsumoExigeOHashDoEscopo(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hash := hashDe("verdadeiro")
	gravarTokenDeConta(t, pool, dono, hash, propositoRedefinir, time.Hour)

	// O parâmetro nomeia o token verdadeiro, mas o escopo é de outro hash: só
	// a política de UPDATE separa os dois.
	if _, err := consumirToken(t, pool, hashDe("escopo-alheio"), hash, propositoRedefinir, agora); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("consumo com escopo de outro hash deu %v, queria pgx.ErrNoRows", err)
	}

	// No escopo do dono, a política de SELECT deixa ver o token (ramo do
	// dono), e só a de UPDATE impede consumi-lo sem o hash no escopo.
	ctx := context.Background()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		_, err := q.ConsumeAccountLink(ctx, db.ConsumeAccountLinkParams{TokenHash: hash, Purpose: propositoRedefinir, Now: agora})
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("consumo no escopo do dono deu %v, queria pgx.ErrNoRows", err)
	}

	var usado *time.Time
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT used_at FROM account_tokens WHERE token_hash = $1`, hash).Scan(&usado); err != nil {
		t.Fatal(err)
	}
	if usado != nil {
		t.Errorf("used_at = %v depois do consumo recusado, queria nulo", *usado)
	}
}

func TestTokenDeContaVencidoNaoConsome(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hash := hashDe("vencido")
	gravarTokenDeConta(t, pool, dono, hash, propositoConfirmar, 24*time.Hour)

	// No instante exato de expires_at, o token já não vale.
	if _, err := consumirToken(t, pool, hash, hash, propositoConfirmar, agora.Add(24*time.Hour)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("consumo em expires_at deu %v, queria pgx.ErrNoRows", err)
	}
}

func TestDonoApagaSoOsProprios(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	gravarTokenDeConta(t, pool, a, hashDe("de-a"), propositoConfirmar, 24*time.Hour)
	gravarTokenDeConta(t, pool, b, hashDe("de-b"), propositoConfirmar, 24*time.Hour)

	ctx := context.Background()
	// O WHERE nomeia B de propósito: só a política impede A de apagar o de B.
	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		if err := q.DeleteAccountLinks(ctx, db.DeleteAccountLinksParams{UserID: a, Purpose: propositoConfirmar}); err != nil {
			return err
		}
		return q.DeleteAccountLinks(ctx, db.DeleteAccountLinksParams{UserID: b, Purpose: propositoConfirmar})
	})
	if err != nil {
		t.Fatalf("apagar tokens no escopo de A: %v", err)
	}

	contar := func(dono uuid.UUID) int {
		var n int
		if err := testdb.Admin(t).QueryRow(ctx, `SELECT count(*) FROM account_tokens WHERE user_id = $1`, dono).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := contar(a); n != 0 {
		t.Errorf("tokens de A depois de apagar = %d, queria 0", n)
	}
	if n := contar(b); n != 1 {
		t.Errorf("tokens de B depois de A apagar = %d, queria 1", n)
	}
}

func TestInAccountTokenTxRecusaHashCurto(t *testing.T) {
	pool := testdb.New(t)
	rodou := false
	err := storage.InAccountTokenTx(context.Background(), pool, make([]byte, 31), func(*db.Queries) error {
		rodou = true
		return nil
	})
	if !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("hash de 31 bytes deu %v, queria ErrNoToken", err)
	}
	if rodou {
		t.Error("fn rodou com hash de 31 bytes")
	}
}

func TestInsertSignupUserNaoDuplica(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)

	primeiro := novoID()
	id, err := q.InsertSignupUser(ctx, db.InsertSignupUserParams{
		ID: primeiro, Email: "fulano@x.com", PasswordHash: "x", CreatedAt: agora,
	})
	if err != nil {
		t.Fatalf("primeiro cadastro: %v", err)
	}
	if id != primeiro {
		t.Errorf("primeiro cadastro devolveu %v, queria %v", id, primeiro)
	}

	// citext: a caixa diferente é o mesmo e-mail.
	if _, err := q.InsertSignupUser(ctx, db.InsertSignupUserParams{
		ID: novoID(), Email: "Fulano@X.com", PasswordHash: "y", CreatedAt: agora,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("segundo cadastro do mesmo e-mail deu %v, queria pgx.ErrNoRows", err)
	}
}
