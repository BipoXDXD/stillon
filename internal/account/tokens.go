package account

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// Validades dos tokens de conta (spec): os dois são de uso único.
const (
	ValidadeDaConfirmacao = 24 * time.Hour
	ValidadeDaRedefinicao = time.Hour
)

// Propósitos gravados em account_tokens.purpose.
const (
	propositoConfirmacao = "confirm_email"
	propositoRedefinicao = "reset_password"
)

// ErrTokenInvalido: token inexistente, já usado ou vencido. Um erro só, para
// não contar a quem chuta qual dos três aconteceu.
var ErrTokenInvalido = errors.New("token inválido")

// bytesDoTokenDeConta são os 256 bits de entropia do token.
const bytesDoTokenDeConta = 32

// novoTokenDeConta gera o token que vai no link e o hash que vai para o banco:
// 32 bytes de crypto/rand em base64url sem padding, e o SHA-256 dele
// (crypto.HashToken). O token em si nunca é gravado.
func novoTokenDeConta() (token string, hash []byte, err error) {
	cru := make([]byte, bytesDoTokenDeConta)
	if _, err := rand.Read(cru); err != nil {
		return "", nil, fmt.Errorf("gerar token de conta: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(cru)
	return token, crypto.HashToken(token), nil
}

// gravarTokenDeConta roda sob o escopo e a trava do usuário. A sequência
// do banco ordena as emissões do mesmo dono mesmo com relógios diferentes.
func gravarTokenDeConta(ctx context.Context, q *db.Queries, userID uuid.UUID, hash []byte, proposito string, now time.Time, validade time.Duration) error {
	if err := q.DeleteExpiredAccountTokens(ctx, db.DeleteExpiredAccountTokensParams{UserID: userID, Now: now}); err != nil {
		return fmt.Errorf("apagar links de conta vencidos: %w", err)
	}
	if err := q.InsertAccountToken(ctx, db.InsertAccountTokenParams{
		TokenHash: hash, UserID: userID, Purpose: proposito, CreatedAt: now, ExpiresAt: now.Add(validade),
	}); err != nil {
		return fmt.Errorf("gravar link de conta: %w", err)
	}
	return nil
}

// entregaDoTokenDeConta valida o link antes da rede e registra o resultado
// após a rede. A leitura inicial não trava linhas nem reserva a conta.
func entregaDoTokenDeConta(pool *pgxpool.Pool, clk clock.Clock, userID uuid.UUID, hash []byte, proposito string) EntregaDeEmail {
	return EntregaDeEmail{
		Preparar: func(ctx context.Context) (bool, error) {
			err := storage.InAccountTokenTx(ctx, pool, hash, func(q *db.Queries) error {
				_, err := donoDoToken(ctx, q, hash, proposito, clk.Now())
				return err
			})
			if errors.Is(err, ErrTokenInvalido) {
				return false, nil
			}
			return err == nil, err
		},
		Concluir: concluirTokenDeConta(pool, clk, userID, hash),
	}
}

// concluirTokenDeConta só retém o hash. O transporte roda fora da transação.
// Quando não houve resposta do provedor, os dois links podem ter sido
// entregues e ficam válidos até consumo ou vencimento.
func concluirTokenDeConta(pool *pgxpool.Pool, clk clock.Clock, userID uuid.UUID, hash []byte) ConclusaoDoEnvio {
	return func(ctx context.Context, resultado ResultadoDoEnvio) error {
		if resultado == EnvioIncerto {
			return nil
		}
		return storage.InUserTx(ctx, pool, userID, func(q *db.Queries) error {
			if err := q.LockUserSessions(ctx, userID); err != nil {
				return err
			}
			switch resultado {
			case AceitoPeloTransporte:
				return q.ReplaceOlderAccountTokens(ctx, db.ReplaceOlderAccountTokensParams{
					TokenHash: hash, UserID: userID, Now: clk.Now(),
				})
			case EnvioDescartado:
				return q.DeleteDiscardedAccountToken(ctx, db.DeleteDiscardedAccountTokenParams{
					UserID: userID, TokenHash: hash,
				})
			}
			return nil
		})
	}
}

// donoDoToken lê o candidato sem travar uma linha, pois quem consome precisa
// adquirir primeiro a trava do usuário que também ordena os envios.
func donoDoToken(ctx context.Context, q *db.Queries, hash []byte, proposito string, now time.Time) (uuid.UUID, error) {
	dono, err := q.PeekAccountToken(ctx, db.PeekAccountTokenParams{TokenHash: hash, Purpose: proposito, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrTokenInvalido
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("conferir link de conta: %w", err)
	}
	return dono, nil
}

func consumirTokenDeConta(ctx context.Context, q *db.Queries, hash []byte, proposito string, now time.Time) (uuid.UUID, error) {
	dono, err := q.ConsumeAccountToken(ctx, db.ConsumeAccountTokenParams{TokenHash: hash, Purpose: proposito, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrTokenInvalido
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("consumir link de conta: %w", err)
	}
	return dono, nil
}
