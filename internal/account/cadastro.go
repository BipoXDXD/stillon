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
	users  auth.UserStore
	clk    clock.Clock
	mailer AccountMailer
	links  Links
	// hash é o auth.HashPassword em produção; os testes o trocam (SetHash).
	hash func(context.Context, string) (string, error)
}

// NewCadastro monta o serviço do cadastro público.
func NewCadastro(pool *pgxpool.Pool, clk clock.Clock, mailer AccountMailer, links Links) *Cadastro {
	return &Cadastro{pool: pool, users: auth.NewPGStore(pool), clk: clk, mailer: mailer, links: links, hash: auth.HashPassword}
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
		// A conta acabou de ser inserida nesta transação. Antes do commit,
		// o ID novo ainda não é visível e só esta transação emite para ele.
		if err := inserirTokenDeConta(ctx, q, id, tokenHash, propositoConfirmacao, c.clk.Now(), ValidadeDaConfirmacao); err != nil {
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

// ConfirmarEmail exige o link E a senha escolhida no cadastro: o link sozinho
// habilitaria a conta para quem só tem acesso ao e-mail (inclusive o dono do
// endereço que alguém cadastrou), sem provar que foi ele quem a criou.
//
// A ordem:
//
//  1. o dono do token, numa leitura sem trava. Token inexistente, usado ou
//     vencido é ErrTokenInvalido, antes de olhar a senha;
//  2. senha vazia é ErrInvalidCredentials, sem Argon2id e sem contar
//     tentativa: não adivinha nada;
//  3. a senha, fora de transação, pelo mecanismo do login
//     (auth.CheckPasswordOrLocked): a vaga do Argon2id, a reserva no bloqueio
//     da CONTA (o mesmo contador do login, então a confirmação não é um
//     oráculo de senha à parte) e o Argon2id. Errada é ErrInvalidCredentials,
//     e a reserva fica contada; a conta travada é auth.LockedError (que é
//     auth.ErrLocked), com o fim da trava. ipHash, o HMAC do IP de quem
//     tenta, vai para a auditoria se esta tentativa travar a conta. Certa, a
//     reserva volta ainda com a vaga: só a errada conta;
//  4. a transação de antes: trava do usuário, o token consumido, o hash
//     conferido de novo (a senha trocada depois da verificação recusa), o
//     e-mail confirmado, os outros links revogados, a auditoria.
//
// Só o passo 4 consome o link: nos erros de senha ele continua valendo. De
// dois cliques simultâneos, exatamente um vence. Com o teto do Argon2id
// cheio, auth.ErrArgon2Busy, sem contar tentativa.
func (c *Cadastro) ConfirmarEmail(ctx context.Context, token, senha string, ipHash []byte) error {
	tokenHash := crypto.HashToken(token)
	var dono uuid.UUID
	err := storage.InAccountTokenTx(ctx, c.pool, tokenHash, func(q *db.Queries) error {
		var err error
		dono, err = donoDoToken(ctx, q, tokenHash, propositoConfirmacao, c.clk.Now())
		return err
	})
	if err != nil {
		return err
	}
	if senha == "" {
		return ErrInvalidCredentials
	}
	verificado, err := c.conferirSenha(ctx, dono, senha, ipHash)
	if err != nil {
		return err
	}

	return storage.InAccountTokenTx(ctx, c.pool, tokenHash, func(q *db.Queries) error {
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
		// O token é o mesmo, então o dono também; o hash pode ter mudado
		// (redefinição, troca de senha) entre a verificação e a trava.
		atual, err := q.GetUserHash(ctx, userID)
		if err != nil {
			return fmt.Errorf("ler a senha do usuário: %w", err)
		}
		if userID != dono || atual != verificado {
			return ErrInvalidCredentials
		}
		if err := q.MarkEmailVerified(ctx, db.MarkEmailVerifiedParams{Now: now, ID: userID}); err != nil {
			return fmt.Errorf("marcar e-mail confirmado: %w", err)
		}
		if err := q.DeleteAccountLinks(ctx, db.DeleteAccountLinksParams{
			UserID: userID, Purpose: propositoConfirmacao,
		}); err != nil {
			return fmt.Errorf("revogar links de confirmação: %w", err)
		}
		return audit(ctx, q, userID, "user.email_verified", nil, nil, nil, now)
	})
}

// conferirSenha é o passo 3 de ConfirmarEmail e devolve o hash que conferiu.
// ipHash é o HMAC do IP de quem tenta, que a auditoria grava se esta
// tentativa travar a conta. A conta travada é auth.LockedError, com o fim da
// trava relido depois da recusa (a trava pode ter vindo de outra tentativa
// no meio); se ela já venceu nesse intervalo, o fim é agora.
func (c *Cadastro) conferirSenha(ctx context.Context, dono uuid.UUID, senha string, ipHash []byte) (string, error) {
	u, err := c.users.ByID(ctx, dono)
	if err != nil {
		// O dono do token tem conta (a FK apaga os tokens com ela): ausente
		// aqui é corrida com a exclusão, e o link já não vale.
		if errors.Is(err, auth.ErrNotFound) {
			return "", ErrTokenInvalido
		}
		return "", err
	}
	agora := c.clk.Now()
	tentativas := auth.AccountAttempts(c.users, dono, ipHash, agora)
	ok, err := auth.CheckPasswordOrLocked(ctx, tentativas, u, senha)
	if errors.Is(err, auth.ErrLocked) {
		return "", c.fimDaTrava(ctx, dono, agora)
	}
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrInvalidCredentials
	}
	return u.PasswordHash, nil
}

// fimDaTrava é o auth.LockedError da conta travada, com o locked_until atual.
func (c *Cadastro) fimDaTrava(ctx context.Context, dono uuid.UUID, agora time.Time) error {
	u, err := c.users.ByID(ctx, dono)
	if err != nil {
		return fmt.Errorf("ler o fim da trava: %w", err)
	}
	ate := agora
	if u.LockedUntil != nil && u.LockedUntil.After(agora) {
		ate = *u.LockedUntil
	}
	return auth.LockedError{Until: ate}
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
