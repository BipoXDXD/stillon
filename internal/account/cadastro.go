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
// tem conta; esqueceu a senha?". Nos dois casos devolve nil: o erro é só de
// validação (antes de qualquer trabalho) ou de infraestrutura, inclusive
// auth.ErrArgon2Busy.
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
	err = emTx(ctx, c.pool, func(q *db.Queries) error {
		_, err := q.InsertSignupUser(ctx, db.InsertSignupUserParams{
			ID: id, Email: e, PasswordHash: hash, CreatedAt: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING: o e-mail já tem conta, e nada muda nela.
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
		if err := gravarTokenDeConfirmacao(ctx, q, id, tokenHash, now); err != nil {
			return err
		}
		return audit(ctx, q, id, "user.signed_up", nil, nil, nil, now)
	})
	if err != nil {
		return err
	}

	var assunto, corpo string
	if existente {
		assunto, corpo = emailJaTemConta(c.links.EsqueciASenha())
	} else {
		assunto, corpo = emailDeConfirmacao(c.links.ConfirmarEmail(token))
	}
	return c.enviar(ctx, e, assunto, corpo)
}

// ConfirmarEmail consome o token do link e marca o e-mail como confirmado, na
// mesma transação. Token inexistente, já usado ou vencido é ErrTokenInvalido,
// sem distinção. O consumo é um UPDATE só (ConsumeAccountToken): de dois
// cliques simultâneos, exatamente um vence.
func (c *Cadastro) ConfirmarEmail(ctx context.Context, token string) error {
	now := c.clk.Now()
	tokenHash := crypto.HashToken(token)
	return storage.InAccountTokenTx(ctx, c.pool, tokenHash, func(q *db.Queries) error {
		userID, err := q.ConsumeAccountToken(ctx, db.ConsumeAccountTokenParams{
			Now: now, TokenHash: tokenHash, Purpose: propositoConfirmacao,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalido
		}
		if err != nil {
			return fmt.Errorf("consumir token de confirmação: %w", err)
		}
		if err := q.MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{Now: now, ID: userID}); err != nil {
			return fmt.Errorf("marcar e-mail confirmado: %w", err)
		}
		return audit(ctx, q, userID, "user.email_verified", nil, nil, nil, now)
	})
}

// ReenviarConfirmacao manda um link de confirmação novo para o usuário da
// sessão; o token novo apaga os anteriores, então só o último e-mail vale.
// Com o e-mail já confirmado, devolve nil sem mandar nada. Usuário que não
// existe mais é ErrUserNotFound.
func (c *Cadastro) ReenviarConfirmacao(ctx context.Context, userID uuid.UUID) error {
	conta, err := db.New(c.pool).GetAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("ler conta: %w", err)
	}
	if conta.EmailVerifiedAt != nil {
		return nil
	}
	token, tokenHash, err := novoTokenDeConta()
	if err != nil {
		return err
	}
	now := c.clk.Now()
	err = emTx(ctx, c.pool, func(q *db.Queries) error {
		if err := q.ScopeToUser(ctx, userID); err != nil {
			return fmt.Errorf("escopo do usuário: %w", err)
		}
		return gravarTokenDeConfirmacao(ctx, q, userID, tokenHash, now)
	})
	if err != nil {
		return err
	}
	assunto, corpo := emailDeConfirmacao(c.links.ConfirmarEmail(token))
	return c.enviar(ctx, conta.Email, assunto, corpo)
}

// gravarTokenDeConfirmacao apaga os tokens de confirmação anteriores do
// usuário e grava o novo, com a validade de 24 h. Supõe o escopo do usuário
// ligado na transação de q.
func gravarTokenDeConfirmacao(ctx context.Context, q *db.Queries, userID uuid.UUID, tokenHash []byte, now time.Time) error {
	if err := q.DeleteAccountTokens(ctx, db.DeleteAccountTokensParams{
		UserID: userID, Purpose: propositoConfirmacao,
	}); err != nil {
		return fmt.Errorf("apagar tokens de confirmação anteriores: %w", err)
	}
	if err := q.InsertAccountToken(ctx, db.InsertAccountTokenParams{
		TokenHash: tokenHash, UserID: userID, Purpose: propositoConfirmacao,
		CreatedAt: now, ExpiresAt: now.Add(ValidadeDaConfirmacao),
	}); err != nil {
		return fmt.Errorf("gravar token de confirmação: %w", err)
	}
	return nil
}

func (c *Cadastro) enviar(ctx context.Context, to, assunto, corpo string) error {
	if err := c.mailer.SendAccountEmail(ctx, to, assunto, corpo); err != nil {
		return fmt.Errorf("enfileirar e-mail de conta: %w", err)
	}
	return nil
}
