// Package account é o serviço da conta do dono (Plano 5): criar e redefinir a
// senha pelo CLI; ler a conta e trocar a senha pela API. O CLI e a API chamam
// o mesmo serviço, para que a
// política de senha e a normalização do e-mail sejam as mesmas nos dois (D20
// do Plano 5).
//
// users e sessions não têm RLS (D7 do Plano 2). A barreira aqui é outra: toda
// query que parte de um usuário recebe o id dele, e cada uma tem teste de
// isolamento com sabotagem.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var (
	// ErrUserExists: o sistema já tem usuário (spec §2.1: usuário único).
	ErrUserExists = errors.New("já existe um usuário")
	// ErrUserNotFound: nenhum usuário com este e-mail.
	ErrUserNotFound = errors.New("usuário não cadastrado")
	// ErrInvalidEmail: não é um endereço puro (sem nome de exibição).
	ErrInvalidEmail = errors.New("e-mail inválido")
	// ErrInvalidCredentials: senha atual errada, segundo fator errado ou
	// ausente, conta bloqueada, ou a senha mudou no meio. Um erro só, pelo
	// motivo do R34: distinguir os casos contaria ao atacante o que acertou.
	ErrInvalidCredentials = errors.New("credenciais inválidas")
	// ErrSessionGone: a família de sessão do token já não está viva (D11 do
	// Plano 5), ou o usuário não existe mais.
	ErrSessionGone = errors.New("sessão encerrada")
)

// PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
// ou auth.ReasonTooLong.
type PolicyError struct{ Reason string }

func (e PolicyError) Error() string { return "senha fora da política: " + e.Reason }

// maxEmailLen é o limite prático de um endereço (RFC 5321 §4.5.3.1.3 e a
// errata 1690: 254 no caminho).
const maxEmailLen = 254

type Service struct {
	pool  *pgxpool.Pool
	users auth.UserStore
	totp  *auth.TOTP
	clk   clock.Clock
	ipKey []byte
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem de usuários e a inserção.
	depoisDeContar func()
}

// NewService: totp e ipKey só servem à API (troca de senha). O CLI passa nil
// nos dois, e ChangePassword recusa rodar sem eles.
func NewService(pool *pgxpool.Pool, totp *auth.TOTP, clk clock.Clock, ipKey []byte) *Service {
	return &Service{pool: pool, users: auth.NewPGStore(pool), totp: totp, clk: clk, ipKey: ipKey}
}

// Account é o que GET /me devolve.
type Account struct {
	ID          uuid.UUID
	Email       string
	TOTPEnabled bool
	CreatedAt   time.Time
}

// Me devolve ErrUserNotFound se o usuário do token não existe mais.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (Account, error) {
	r, err := db.New(s.pool).GetAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrUserNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("ler conta: %w", err)
	}
	return Account{ID: r.ID, Email: r.Email, TOTPEnabled: r.TotpEnabled, CreatedAt: r.CreatedAt}, nil
}

// FamilyIsLive diz se a família de sessão do token ainda está viva: tem a
// linha corrente, não usada, não revogada e não vencida.
func (s *Service) FamilyIsLive(ctx context.Context, userID, familyID uuid.UUID) (bool, error) {
	return familyIsLive(ctx, db.New(s.pool), userID, familyID, s.clk.Now())
}

func familyIsLive(ctx context.Context, q *db.Queries, userID, familyID uuid.UUID, now time.Time) (bool, error) {
	vivo, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: userID, FamilyID: familyID, Now: now})
	if err != nil {
		return false, fmt.Errorf("conferir família de sessão: %w", err)
	}
	return vivo, nil
}

// ChangePasswordInput é o corpo de POST /auth/password.
type ChangePasswordInput struct {
	Current  string
	New      string
	TOTPCode string
}

// ChangePassword troca a senha pela API (D12 do Plano 5), na ordem:
//
//  1. a política da senha nova (pública: validá-la primeiro não vaza nada e
//     não gasta Argon2 nem conta falha por erro de digitação na nova);
//  2. a família do token viva (leitura barata; a verdade vem no passo 6);
//  3. a senha atual, pela verificação do login (auth.CheckPassword): conta
//     bloqueada recusa, senha errada conta no bloqueio;
//  4. o segundo fator, se ligado, por auth.TOTP.Verify (aceita o código de
//     recuperação); errado ou ausente conta no bloqueio (R40);
//  5. o hash novo, fora da transação;
//  6. a transação: trava do usuário (D1), família viva de novo, CAS do hash,
//     as OUTRAS famílias revogadas, auditoria.
//
// Devolve PolicyError, ErrSessionGone ou ErrInvalidCredentials; nil quer
// dizer "trocada, e a família atual continua viva".
func (s *Service) ChangePassword(ctx context.Context, userID, familyID uuid.UUID, in ChangePasswordInput, meta auth.Meta) error {
	if s.totp == nil || len(s.ipKey) == 0 {
		return errors.New("account: ChangePassword sem segundo fator ou sem chave de IP")
	}
	if motivo := auth.ValidateNewPassword(in.New); motivo != "" {
		return PolicyError{Reason: motivo}
	}
	now := s.clk.Now()

	vivo, err := s.FamilyIsLive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if !vivo {
		return ErrSessionGone
	}
	u, err := s.users.ByID(ctx, userID)
	if errors.Is(err, auth.ErrNotFound) {
		return ErrSessionGone
	}
	if err != nil {
		return err
	}

	ok, _, err := auth.CheckPassword(ctx, s.users, u, in.Current, now)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}

	ligado, err := s.totp.IsEnabled(ctx, userID)
	if err != nil {
		return err
	}
	if ligado {
		// O Verify consome o timestep (ou o código de recuperação) fora da
		// transação abaixo: se ela desistir, quem tentar de novo espera o
		// próximo código de 30 s.
		err := s.totp.Verify(ctx, userID, in.TOTPCode, now)
		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
			if err := s.users.RecordFailure(ctx, userID, now); err != nil {
				return err
			}
			return ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
	}

	hash, err := auth.HashPassword(in.New)
	if err != nil {
		return err
	}
	ipHash := crypto.HashIP(s.ipKey, meta.IP)

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		// A família pode ter caído entre o passo 2 e a trava (um DELETE
		// /sessions de outro aparelho, a detecção de reúso). Então nada muda:
		// o 204 sempre quer dizer "trocada e a atual continua viva".
		vivo, err := familyIsLive(ctx, q, userID, familyID, now)
		if err != nil {
			return err
		}
		if !vivo {
			return ErrSessionGone
		}
		n, err := q.SwapUserHash(ctx, db.SwapUserHashParams{ID: userID, NewHash: hash, OldHash: u.PasswordHash})
		if err != nil {
			return fmt.Errorf("gravar senha: %w", err)
		}
		if n == 0 {
			// A senha mudou depois de verificada: a "atual" já não é.
			return ErrInvalidCredentials
		}
		if _, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{
			UserID: userID, KeepFamilyID: familyID, Now: now,
		}); err != nil {
			return fmt.Errorf("revogar as outras sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.password_changed", nil, nil, ipHash, now)
	})
}

// ValidateEmail normaliza como o login (auth.NormalizeEmail) e exige um
// endereço puro: "Nome <a@b.com>" é recusado, porque o login compara o texto
// inteiro e nunca casaria com ele.
func ValidateEmail(email string) (string, error) {
	e := auth.NormalizeEmail(email)
	if len(e) == 0 || len(e) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Name != "" || addr.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// HasUsers responde se o sistema já tem usuário. O CLI pergunta ANTES de
// pedir a senha, para ninguém digitar duas vezes à toa; CreateUser confere de
// novo sob a trava.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := db.New(s.pool).CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("contar usuários: %w", err)
	}
	return n > 0, nil
}

// UserIDByEmail devolve ErrUserNotFound quando o e-mail não está cadastrado.
func (s *Service) UserIDByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	id, err := db.New(s.pool).GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("buscar usuário: %w", err)
	}
	return id, nil
}

// CreateUser cria o dono. Recusa com ErrUserExists se já existe QUALQUER
// usuário (D8 do Plano 5), e a recusa não muda nada.
//
// A contagem e a inserção rodam sob uma advisory lock de transação (D9): sem
// ela, dois CLIs simultâneos contariam zero e criariam dois donos.
func (s *Service) CreateUser(ctx context.Context, email, password string) (uuid.UUID, error) {
	e, err := ValidateEmail(email)
	if err != nil {
		return uuid.Nil, err
	}
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return uuid.Nil, PolicyError{Reason: motivo}
	}
	// O Argon2id leva ~100 ms: fora da transação, para não segurar a trava.
	hash, err := auth.HashPassword(password)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("gerar id do usuário: %w", err)
	}
	now := s.clk.Now()

	err = s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockUserCreation(ctx); err != nil {
			return fmt.Errorf("travar criação de usuário: %w", err)
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("contar usuários: %w", err)
		}
		if s.depoisDeContar != nil {
			s.depoisDeContar()
		}
		if n > 0 {
			return ErrUserExists
		}
		if err := q.InsertUser(ctx, db.InsertUserParams{
			ID: id, Email: e, PasswordHash: hash, CreatedAt: now,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrUserExists
			}
			return fmt.Errorf("inserir usuário: %w", err)
		}
		return audit(ctx, q, id, "user.created", nil, nil, nil, now)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ResetPassword é o caminho de recuperação do operador (vigilume user
// set-password, D10 do Plano 5): grava a senha nova, tira o bloqueio e encerra
// TODAS as sessões. Recuperação quase sempre é "perdi" ou "vazou", e manter as
// sessões deixaria o invasor dentro. Não mexe no segundo fator.
//
// Devolve quantas linhas de sessão foram revogadas.
func (s *Service) ResetPassword(ctx context.Context, email, password string) (int64, error) {
	if motivo := auth.ValidateNewPassword(password); motivo != "" {
		return 0, PolicyError{Reason: motivo}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	now := s.clk.Now()

	var revogadas int64
	err = s.inTx(ctx, func(q *db.Queries) error {
		userID, err := q.GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("buscar usuário: %w", err)
		}
		// A trava do usuário antes de tocar em sessions (D1): sem ela, uma
		// rotação em curso deixaria o sucessor fora da revogação.
		if err := q.LockUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("travar sessões: %w", err)
		}
		if _, err := q.ReplaceUserHash(ctx, db.ReplaceUserHashParams{ID: userID, PasswordHash: hash}); err != nil {
			return fmt.Errorf("gravar senha: %w", err)
		}
		revogadas, err = q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("revogar sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.password_reset", nil, nil, nil, now)
	})
	return revogadas, err
}

func (s *Service) inTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// audit grava na mesma transação da mudança: não existe mudança sem rastro,
// nem rastro de mudança que não aconteceu (mesma razão do R39).
func audit(ctx context.Context, q *db.Queries, userID uuid.UUID, action string, entity *string, entityID *uuid.UUID, ipHash []byte, at time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	if err := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ID: id, UserID: &userID, Action: action, Entity: entity, EntityID: entityID, IpHash: ipHash, At: at,
	}); err != nil {
		return fmt.Errorf("auditar %s: %w", action, err)
	}
	return nil
}
