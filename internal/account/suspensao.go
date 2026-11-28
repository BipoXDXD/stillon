package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// ErrOperatorNotSuspendable: a conta do operador não se suspende. A recusa
// não muda nada.
var ErrOperatorNotSuspendable = errors.New("a conta do operador não pode ser suspensa")

// Suspend marca suspended_at (mantém o primeiro instante), revoga todas as
// famílias de sessão e audita user.suspended, numa transação. Devolve
// ErrUserNotFound para e-mail não cadastrado e ErrOperatorNotSuspendable para
// o operador; revogadas conta as famílias vivas, como o ResetPassword.
//
// A marca e a revogação vão sob a trava de sessões do usuário, a mesma da
// emissão e da rotação (auth.Manager), que releem a suspensão sob ela: ou a
// suspensão comita antes (e a emissão ou a rotação recusa), ou a sessão comita
// antes (e a revogação daqui a alcança).
func (s *Service) Suspend(ctx context.Context, email string) (revogadas int64, err error) {
	now := s.clk.Now()
	err = s.inTx(ctx, func(q *db.Queries) error {
		userID, err := usuarioTravado(ctx, q, email)
		if err != nil {
			return err
		}
		n, err := q.SuspendUser(ctx, db.SuspendUserParams{ID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("suspender usuário: %w", err)
		}
		if n == 0 {
			return ErrOperatorNotSuspendable
		}
		revogadas, err = q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: userID, Now: now})
		if err != nil {
			return fmt.Errorf("revogar sessões: %w", err)
		}
		return audit(ctx, q, userID, "user.suspended", nil, nil, nil, now)
	})
	if err != nil {
		return 0, err
	}
	return revogadas, nil
}

// Unsuspend limpa suspended_at e audita user.unsuspended. As sessões
// encerradas pela suspensão não voltam: o dono faz login de novo. Devolve
// ErrUserNotFound para e-mail não cadastrado.
func (s *Service) Unsuspend(ctx context.Context, email string) error {
	now := s.clk.Now()
	return s.inTx(ctx, func(q *db.Queries) error {
		userID, err := usuarioTravado(ctx, q, email)
		if err != nil {
			return err
		}
		if _, err := q.UnsuspendUser(ctx, userID); err != nil {
			return fmt.Errorf("reativar usuário: %w", err)
		}
		return audit(ctx, q, userID, "user.unsuspended", nil, nil, nil, now)
	})
}

// usuarioTravado acha o usuário pelo e-mail, liga o escopo dele (RLS de
// sessions, migration 00029) e toma a trava de sessões, na ordem do
// set-password. O que muda depois é escrito sob a trava.
func usuarioTravado(ctx context.Context, q *db.Queries, email string) (uuid.UUID, error) {
	userID, err := q.GetUserIDByEmail(ctx, auth.NormalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("buscar usuário: %w", err)
	}
	if err := q.ScopeToUser(ctx, userID); err != nil {
		return uuid.Nil, fmt.Errorf("definir escopo do usuário: %w", err)
	}
	if err := q.LockUserSessions(ctx, userID); err != nil {
		return uuid.Nil, fmt.Errorf("travar sessões: %w", err)
	}
	return userID, nil
}
