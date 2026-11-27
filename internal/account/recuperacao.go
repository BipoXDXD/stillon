package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Recuperacao é o "esqueci a senha": manda um link de uso único (1 h) para o
// e-mail da conta, e o link troca a senha, confirma o e-mail e encerra todas
// as sessões.
type Recuperacao struct {
	pool   *pgxpool.Pool
	clk    clock.Clock
	mailer AccountMailer
	links  Links
	// O Argon2id fica fora das transações e trocável nos testes (SetHash).
	hashPassword func(context.Context, string) (string, error)
}

// NewRecuperacao monta o serviço com a derivação de produção (Argon2id).
func NewRecuperacao(pool *pgxpool.Pool, clk clock.Clock, mailer AccountMailer, links Links) *Recuperacao {
	return &Recuperacao{pool: pool, clk: clk, mailer: mailer, links: links, hashPassword: auth.HashPassword}
}

// PedirRedefinicao manda o link de redefinição se o e-mail tiver conta, e
// devolve nil com conta ou sem conta: a resposta não diz se o e-mail está
// cadastrado. O novo link substitui os anteriores quando o transporte o aceita.
//
// O link vai para o endereço gravado, e não para o digitado: o citext casa
// "Dono@" com "dono@", mas um servidor de e-mail pode tratar a parte local
// com distinção de caixa e entregar a outra caixa.
//
// O e-mail só é enfileirado (AccountMailer): a latência da rede não aparece
// no ramo "tem conta". Um erro devolvido é só do banco: a falha do mailer vai
// para o log e não muda a resposta.
func (r *Recuperacao) PedirRedefinicao(ctx context.Context, email string) error {
	userID, err := db.New(r.pool).GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("buscar usuário: %w", err)
	}

	token, hash, err := novoTokenDeConta()
	if err != nil {
		return err
	}
	var destino string
	err = storage.InUserTx(ctx, r.pool, userID, func(q *db.Queries) error {
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar o usuário: %w", err)
		}
		conta, err := q.GetAccount(ctx, userID)
		if err != nil {
			// A conta pode ter sido apagada desde a busca de userID.
			return fmt.Errorf("ler conta: %w", err)
		}
		destino = conta.Email
		return gravarTokenDeConta(ctx, q, userID, hash, propositoRedefinicao, r.clk.Now(), ValidadeDaRedefinicao)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	assunto, corpo := emailDeRedefinicao(r.links.RedefinirSenha(token))
	enviarEmailDeConta(ctx, r.mailer, destino, assunto, corpo, entregaDoTokenDeConta(r.pool, r.clk, userID, hash, propositoRedefinicao))
	return nil
}

// Redefinir troca a senha pelo link, na ordem:
//
//  1. a política da senha nova (não gasta o token por erro de digitação);
//  2. o token espiado, sem consumir: token lixo, usado ou vencido para aqui,
//     antes do Argon2id;
//  3. o hash novo, fora da transação;
//  4. a transação: a trava de sessões do dono espiado, o consumo atômico do
//     token (quem perde a corrida recebe ErrTokenInvalido), o hash trocado, TODAS as
//     sessões encerradas, o e-mail confirmado (o link prova a posse da
//     caixa), os links dos dois propósitos apagados e a auditoria.
//
// O 2FA confirmado continua ligado: o link prova a caixa, não o fator.
//
// Devolve ErrTokenInvalido, PolicyError, auth.ErrArgon2Busy ou erro de
// infraestrutura.
func (r *Recuperacao) Redefinir(ctx context.Context, token, novaSenha string) error {
	if motivo := auth.ValidateNewPassword(novaSenha); motivo != "" {
		return PolicyError{Reason: motivo}
	}
	tokenHash := crypto.HashToken(token)

	var dono uuid.UUID
	err := storage.InAccountTokenTx(ctx, r.pool, tokenHash, func(q *db.Queries) error {
		var err error
		dono, err = donoDoToken(ctx, q, tokenHash, propositoRedefinicao, r.clk.Now())
		return err
	})
	if err != nil {
		return err
	}

	hash, err := r.hashPassword(ctx, novaSenha)
	if err != nil {
		return err
	}

	return storage.InAccountTokenTx(ctx, r.pool, tokenHash, func(q *db.Queries) error {
		if err := q.LockUserSessions(ctx, dono); err != nil {
			return fmt.Errorf("travar o usuário: %w", err)
		}
		now := r.clk.Now()
		userID, err := consumirTokenDeConta(ctx, q, tokenHash, propositoRedefinicao, now)
		if err != nil {
			return err
		}
		return trocarSenhaPeloLink(ctx, q, userID, hash, now)
	})
}

// trocarSenhaPeloLink é o resto do passo 4 de Redefinir, na sequência do
// set-password do CLI (ResetPassword), com a trava do usuário já adquirida.
func trocarSenhaPeloLink(ctx context.Context, q *db.Queries, userID uuid.UUID, hash string, now time.Time) error {
	// O dono só aparece ao consumir o token: daqui em diante a transação é
	// dele, para a RLS de sessions, de account_tokens e da auditoria.
	if err := q.ScopeToUser(ctx, userID); err != nil {
		return fmt.Errorf("escopo do usuário: %w", err)
	}
	if _, err := q.ReplaceUserHash(ctx, db.ReplaceUserHashParams{ID: userID, PasswordHash: hash}); err != nil {
		return fmt.Errorf("gravar senha: %w", err)
	}
	if _, err := q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now}); err != nil {
		return fmt.Errorf("revogar sessões: %w", err)
	}
	// Quem pré-cadastrou o e-mail da vítima sem confirmar perde a conta
	// aqui: a dona da caixa a confirma junto com a senha nova.
	if err := q.MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{Now: now, ID: userID}); err != nil {
		return fmt.Errorf("confirmar e-mail: %w", err)
	}
	if err := q.DeleteAllAccountLinks(ctx, userID); err != nil {
		return fmt.Errorf("revogar links de conta: %w", err)
	}
	return audit(ctx, q, userID, "user.password_reset_by_email", nil, nil, nil, now)
}
