// Package account é o serviço da conta do dono: criar e redefinir a senha
// pelo CLI (Plano 5). O CLI e a API chamam o mesmo serviço, para que a
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
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

var (
	// ErrUserExists: o sistema já tem usuário (spec §2.1: usuário único).
	ErrUserExists = errors.New("já existe um usuário")
	// ErrUserNotFound: nenhum usuário com este e-mail.
	ErrUserNotFound = errors.New("usuário não cadastrado")
	// ErrInvalidEmail: não é um endereço puro (sem nome de exibição).
	ErrInvalidEmail = errors.New("e-mail inválido")
)

// PolicyError é a senha nova fora da política; Reason é auth.ReasonTooShort
// ou auth.ReasonTooLong.
type PolicyError struct{ Reason string }

func (e PolicyError) Error() string { return "senha fora da política: " + e.Reason }

// maxEmailLen é o limite prático de um endereço (RFC 5321 §4.5.3.1.3 e a
// errata 1690: 254 no caminho).
const maxEmailLen = 254

type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
	// depoisDeContar existe para o teste de criação concorrente: roda entre a
	// contagem de usuários e a inserção.
	depoisDeContar func()
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
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
