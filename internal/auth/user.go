package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockThreshold e LockDuration implementam o bloqueio progressivo da
// spec §5.2.
const (
	LockThreshold = 5
	LockDuration  = 15 * time.Minute
)

type User struct {
	ID             uuid.UUID
	Email          string
	PasswordHash   string
	FailedAttempts int
	LockedUntil    *time.Time
}

// IsLocked recebe o instante em vez de chamar time.Now para que o bloqueio
// seja testável com o relógio injetado.
func (u User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

type UserStore interface {
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, userID uuid.UUID) (User, error)
	RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error
	ResetFailures(ctx context.Context, userID uuid.UUID) error
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error
}

func (s *PGStore) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until
		  FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}

// RecordFailure incrementa o contador e aplica o bloqueio no mesmo UPDATE.
// Fazer as duas coisas em uma instrução evita corrida entre tentativas
// simultâneas de força bruta.
func (s *PGStore) RecordFailure(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET failed_attempts = failed_attempts + 1,
		       locked_until = CASE
		           WHEN failed_attempts + 1 >= $2 THEN $3
		           ELSE locked_until
		       END
		 WHERE id = $1`,
		userID, LockThreshold, at.Add(LockDuration))
	if err != nil {
		return fmt.Errorf("registrar falha de login: %w", err)
	}
	return nil
}

func (s *PGStore) ResetFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("zerar falhas de login: %w", err)
	}
	return nil
}

func (s *PGStore) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
	if err != nil {
		return fmt.Errorf("atualizar hash de senha: %w", err)
	}
	return nil
}

// ByID atende os dois caminhos de TOTP que só têm o id vindo do token: o
// cadastro, que precisa do e-mail para o nome da conta na URI otpauth://, e a
// verificação do segundo fator, que precisa do estado de bloqueio (R40).
//
// Uma consulta só, em vez de um EmailByID ao lado de um ByID lendo a mesma
// linha da mesma tabela.
func (s *PGStore) ByID(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, failed_attempts, locked_until
		  FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FailedAttempts, &u.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuário: %w", err)
	}
	return u, nil
}
