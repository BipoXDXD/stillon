package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Cadastro é o cadastro público: criar a conta, confirmar o e-mail pelo link
// e reenviar a confirmação. A resposta do cadastro não diz se o e-mail já
// tinha conta: os dois ramos derivam a senha uma vez e mandam um e-mail.
type Cadastro struct {
	pool   *pgxpool.Pool
	clk    clock.Clock
	mailer AccountMailer
	links  Links
	// hash é o auth.HashPassword em produção; os testes o trocam (SetHash).
	hash func(context.Context, string) (string, error)
}

// NewCadastro monta o serviço do cadastro público.
func NewCadastro(pool *pgxpool.Pool, clk clock.Clock, mailer AccountMailer, links Links) *Cadastro {
	return &Cadastro{pool: pool, clk: clk, mailer: mailer, links: links, hash: auth.HashPassword}
}

// ValidarCadastro confere a entrada do cadastro sem tocar no banco e devolve o
// e-mail normalizado. Erros: ErrInvalidEmail ou PolicyError. Como só olha a
// entrada, validar primeiro não conta nada sobre o e-mail.
func ValidarCadastro(email, senha string) (string, error) {
	e, err := ValidateEmail(email)
	if err != nil {
		return "", err
	}
	if motivo := auth.ValidateNewPassword(senha); motivo != "" {
		return "", PolicyError{Reason: motivo}
	}
	return e, nil
}

// Cadastrar cria a conta não confirmada, com o Projeto padrão e o token de
// confirmação, e manda o link por e-mail. Com o e-mail já cadastrado
// (confirmado ou não, em qualquer caixa), não grava nada e manda o "você já
// tem conta; esqueceu a senha?" para o endereço gravado. Nos dois casos
// devolve nil. Os erros possíveis são de validação ou de infraestrutura,
// inclusive auth.ErrArgon2Busy. A falha do mailer vai para o log e a
// resposta continua igual.
//
// Contra a enumeração, os dois ramos fazem o mesmo trabalho caro: o Argon2id
// roda SEMPRE, antes de qualquer consulta, e sai exatamente um e-mail, depois
// do commit (o AccountMailer só enfileira). Sobram alguns ms de banco no ramo
// novo (projeto, token, auditoria), risco aceito no plano.
func (c *Cadastro) Cadastrar(ctx context.Context, email, senha string) error {
	e, err := ValidarCadastro(email, senha)
	if err != nil {
		return err
	}
	hash, err := c.hash(ctx, senha)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id do usuário: %w", err)
	}
	token, tokenHash, err := novoTokenDeConta()
	if err != nil {
		return err
	}
	now := c.clk.Now()

	existente := false
	destino := e
	err = emTx(ctx, c.pool, func(q *db.Queries) error {
		_, err := q.InsertSignupUser(ctx, db.InsertSignupUserParams{
			ID: id, Email: e, PasswordHash: hash, CreatedAt: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// Depois do conflito, a leitura em READ COMMITTED enxerga a conta
			// mesmo quando outro cadastro acabou de confirmar a transação.
			userID, err := q.GetUserIDByEmail(ctx, e)
			if err != nil {
				return fmt.Errorf("buscar usuário do cadastro existente: %w", err)
			}
			conta, err := q.GetAccount(ctx, userID)
			if err != nil {
				return fmt.Errorf("ler conta do cadastro existente: %w", err)
			}
			// O citext ignora a caixa, mas o servidor de e-mail pode distingui-la.
			destino = conta.Email
			existente = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("inserir usuário do cadastro: %w", err)
		}
		if err := q.ScopeToUser(ctx, id); err != nil {
			return fmt.Errorf("escopo do usuário: %w", err)
		}
		if _, err := storage.CriarProjetoPadraoNaTx(ctx, q, id, now); err != nil {
			return err
		}
		if err := q.LockUserSessions(ctx, id); err != nil {
			return fmt.Errorf("travar o usuário: %w", err)
		}
		if err := gravarTokenDeConta(ctx, q, id, tokenHash, propositoConfirmacao, c.clk.Now(), ValidadeDaConfirmacao); err != nil {
			return err
		}
		return audit(ctx, q, id, "user.signed_up", nil, nil, nil, now)
	})
	if err != nil {
		return err
	}

	if existente {
		assunto, corpo := emailJaTemConta(c.links.EsqueciASenha())
		enviarEmailDeConta(ctx, c.mailer, destino, assunto, corpo, EntregaDeEmail{})
	} else {
		assunto, corpo := emailDeConfirmacao(c.links.ConfirmarEmail(token))
		enviarEmailDeConta(ctx, c.mailer, destino, assunto, corpo, entregaDoTokenDeConta(c.pool, c.clk, id, tokenHash, propositoConfirmacao))
	}
	return nil
}

// ConfirmarEmail consome o link, confirma o e-mail e revoga os outros links
// do propósito na mesma transação. Token inexistente, usado ou vencido é
// ErrTokenInvalido. De dois cliques simultâneos, exatamente um vence.
func (c *Cadastro) ConfirmarEmail(ctx context.Context, token string) error {
	tokenHash := crypto.HashToken(token)
	return storage.InAccountTokenTx(ctx, c.pool, tokenHash, func(q *db.Queries) error {
		dono, err := donoDoToken(ctx, q, tokenHash, propositoConfirmacao, c.clk.Now())
		if err != nil {
			return err
		}
		if err := q.LockUserSessions(ctx, dono); err != nil {
			return fmt.Errorf("travar o usuário: %w", err)
		}
		now := c.clk.Now()
		userID, err := consumirTokenDeConta(ctx, q, tokenHash, propositoConfirmacao, now)
		if err != nil {
			return err
		}
		if err := q.ScopeToUser(ctx, userID); err != nil {
			return fmt.Errorf("escopo do usuário: %w", err)
		}
		if err := q.MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{Now: now, ID: userID}); err != nil {
			return fmt.Errorf("marcar e-mail confirmado: %w", err)
		}
		if err := q.DeleteAccountTokens(ctx, db.DeleteAccountTokensParams{
			UserID: userID, Purpose: propositoConfirmacao,
		}); err != nil {
			return fmt.Errorf("revogar links de confirmação: %w", err)
		}
		return audit(ctx, q, userID, "user.email_verified", nil, nil, nil, now)
	})
}

// ReenviarConfirmacao enfileira um link para o usuário da sessão. O novo
// substitui os anteriores quando o transporte o aceita. Se o usuário já
// confirmou o e-mail, nada é enviado. Usuário ausente é ErrUserNotFound.
func (c *Cadastro) ReenviarConfirmacao(ctx context.Context, userID uuid.UUID) error {
	token, tokenHash, err := novoTokenDeConta()
	if err != nil {
		return err
	}
	var destino string
	err = storage.InUserTx(ctx, c.pool, userID, func(q *db.Queries) error {
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar o usuário: %w", err)
		}
		conta, err := q.GetAccount(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("ler conta: %w", err)
		}
		if conta.EmailVerifiedAt != nil {
			return nil
		}
		destino = conta.Email
		return gravarTokenDeConta(ctx, q, userID, tokenHash, propositoConfirmacao, c.clk.Now(), ValidadeDaConfirmacao)
	})
	if err != nil || destino == "" {
		return err
	}
	assunto, corpo := emailDeConfirmacao(c.links.ConfirmarEmail(token))
	enviarEmailDeConta(ctx, c.mailer, destino, assunto, corpo, entregaDoTokenDeConta(c.pool, c.clk, userID, tokenHash, propositoConfirmacao))
	return nil
}
