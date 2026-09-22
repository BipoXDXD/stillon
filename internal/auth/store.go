package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("registro não encontrado")

// SessionRow é a linha crua de sessions.
type SessionRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
	UsedAt    *time.Time
	IPHash    []byte
	UserAgent string
}

// Tx expõe as operações que precisam rodar na mesma transação. A rotação
// inteira é atômica: sem isso, duas rotações simultâneas com o mesmo token
// poderiam ambas passar pela checagem de reúso antes de qualquer uma marcar
// used_at.
type Tx interface {
	LockSessionByHash(ctx context.Context, hash []byte) (SessionRow, error)
	InsertSession(ctx context.Context, r SessionRow) error
	MarkSessionUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error
	InsertAudit(ctx context.Context, userID uuid.UUID, action string, ipHash []byte, at time.Time) error
}

type Store interface {
	InTx(ctx context.Context, fn func(context.Context, Tx) error) error
}

// PGStore é a implementação em Postgres. Devolver o tipo concreto, e não a
// interface, permite que o mesmo valor satisfaça Store agora e UserStore na
// Task 8 — um consumidor pede a interface de que precisa.
//
// O Plano 2 troca o corpo destes métodos por código gerado pelo sqlc (spec
// §3.3) sem mexer nas interfaces — ver o desvio registrado nas Global
// Constraints deste plano.
type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) InTx(ctx context.Context, fn func(context.Context, Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if err := fn(ctx, &pgTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type pgTx struct{ tx pgx.Tx }

func (t *pgTx) LockSessionByHash(ctx context.Context, hash []byte) (SessionRow, error) {
	var r SessionRow
	err := t.tx.QueryRow(ctx, `
		SELECT id, user_id, family_id, token_hash, expires_at, revoked_at, used_at
		  FROM sessions
		 WHERE token_hash = $1
		   FOR UPDATE`, hash).
		Scan(&r.ID, &r.UserID, &r.FamilyID, &r.TokenHash, &r.ExpiresAt, &r.RevokedAt, &r.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionRow{}, ErrNotFound
	}
	if err != nil {
		return SessionRow{}, fmt.Errorf("buscar sessão: %w", err)
	}
	return r, nil
}

func (t *pgTx) InsertSession(ctx context.Context, r SessionRow) error {
	_, err := t.tx.Exec(ctx, `
		INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at, ip_hash, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.ID, r.UserID, r.FamilyID, r.TokenHash, r.ExpiresAt, r.IPHash, r.UserAgent)
	if err != nil {
		return fmt.Errorf("inserir sessão: %w", err)
	}
	return nil
}

func (t *pgTx) MarkSessionUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := t.tx.Exec(ctx, `UPDATE sessions SET used_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("marcar sessão usada: %w", err)
	}
	return nil
}

func (t *pgTx) RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error {
	_, err := t.tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID, at)
	if err != nil {
		return fmt.Errorf("revogar família: %w", err)
	}
	return nil
}

func (t *pgTx) InsertAudit(ctx context.Context, userID uuid.UUID, action string, ipHash []byte, at time.Time) error {
	return insertAudit(ctx, t.tx, userID, action, ipHash, at)
}

// execer é o mínimo de pgx que insertAudit precisa. Existe para que o registro
// de auditoria seja o MESMO código dentro do Tx do Store e dentro das
// transações que o TOTP abre por conta própria (R39): duas cópias da mesma
// instrução acabariam divergindo.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertAudit(ctx context.Context, q execer, userID uuid.UUID, action string, ipHash []byte, at time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("gerar id de auditoria: %w", err)
	}
	_, err = q.Exec(ctx,
		`INSERT INTO audit_log (id, user_id, action, ip_hash, at) VALUES ($1, $2, $3, $4, $5)`,
		id, userID, action, ipHash, at)
	if err != nil {
		return fmt.Errorf("inserir auditoria: %w", err)
	}
	return nil
}
