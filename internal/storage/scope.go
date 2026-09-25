package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Beginner é o mínimo que uma transação com escopo precisa. *pgxpool.Pool e
// *pgxpool.Conn satisfazem; aceitar a conexão avulsa é o que permite provar
// o comportamento numa conexão REUSADA (TestScopeSurvivesConnectionReuse).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var (
	ErrNoUser  = errors.New("transação de usuário sem usuário")
	ErrNoToken = errors.New("transação de verificação sem hash de token")
	ErrNoSlug  = errors.New("transação pública sem slug")
)

// InUserTx roda fn numa transação em que a RLS só enxerga as linhas de userID.
//
// set_config(..., true) é o SET LOCAL: o valor morre no fim da transação e
// não vaza para o próximo uso da conexão no pool. Ver a migration 00004 para
// o motivo do NULLIF nas políticas.
func InUserTx(ctx context.Context, b Beginner, userID uuid.UUID, fn func(*db.Queries) error) error {
	if userID == uuid.Nil {
		// uuid.Nil nunca é usuário. Aceitá-lo produziria uma transação que
		// não enxerga nada, e o erro apareceria longe daqui, como "não
		// encontrado".
		return ErrNoUser
	}
	return inTx(ctx, b, `SELECT set_config('app.user_id', $1, true)`, userID.String(), fn)
}

// InProberTx roda fn com o escopo do sondador: lê e reagenda alvos de todos
// os usuários e grava checks, mas não cria nem apaga alvos (políticas
// targets_prober_*). É o ÚNICO lugar do código que liga esse escopo.
func InProberTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "prober", fn)
}

// InVerifyTx roda fn no escopo de verificação de canal (GET/POST
// /verify/{token}): a RLS enxerga só a linha de notif_channels cujo
// verify_token_hash é tokenHash, e nada de outra tabela. É o único lugar do
// código que liga esse escopo.
//
// O hash vai em hexadecimal porque GUC é texto; a política o decodifica.
func InVerifyTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error {
	if len(tokenHash) != sha256.Size {
		// Um hash vazio casaria com nada, e o erro apareceria longe daqui
		// como "link inválido". Um tamanho errado é defeito de quem chama.
		return ErrNoToken
	}
	return inTx(ctx, b, `SELECT set_config('app.verify_token_hash', $1, true)`, hex.EncodeToString(tokenHash), fn)
}

// InPublicTx roda fn no escopo público da status page (GET /p/{slug}): a
// RLS enxerga a página publicada daquele slug, os itens dela e, por
// delegação, os alvos, checks, rollups e incidentes desses itens. Nada mais.
//
// A transação é READ ONLY. As políticas que delegam a targets são FOR ALL
// (incidents, checks, target_health), e sem isto o escopo público seria
// também porta de escrita nelas. O Postgres recusa qualquer escrita com
// 25006, seja qual for a política.
func InPublicTx(ctx context.Context, b Beginner, slug string, fn func(*db.Queries) error) error {
	if slug == "" {
		return ErrNoSlug
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	// Tem de ser o primeiro comando da transação.
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		return fmt.Errorf("transação somente leitura: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_slug', $1, true)`, slug); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func inTx(ctx context.Context, b Beginner, setSQL, valor string, fn func(*db.Queries) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, err := tx.Exec(ctx, setSQL, valor); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
