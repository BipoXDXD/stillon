package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/observability"
)

// Bloqueio progressivo do segundo fator. É um contador à parte do
// bloqueio da conta, e só /totp/verify o move: chegar lá exige a senha certa,
// então quem erra aqui já tem a senha. Por isso ele não volta a zero quando um
// bloqueio vence, ao contrário do da conta: só um acerto o zera.
const (
	// TOTPLockThreshold é quantas falhas seguidas travam o segundo fator.
	TOTPLockThreshold = 5
	// TOTPLockBase é a duração do primeiro bloqueio; cada bloqueio seguinte
	// dura o dobro do anterior, até TOTPLockCap.
	TOTPLockBase = 15 * time.Minute
	TOTPLockCap  = 24 * time.Hour
	// TOTPMaxFailures é o teto absoluto: a falha número TOTPMaxFailures trava
	// o segundo fator até o reset-2fa do CLI. Com a janela de 3 códigos, são
	// 150 palpites em seis dígitos, ~0,015% de acerto.
	TOTPMaxFailures = 50

	// acaoTOTPBloqueado é a ação de audit_log do bloqueio do segundo fator.
	acaoTOTPBloqueado = "totp.locked"
)

// TOTPLockFor diz o bloqueio que a falha número falhas aplica: duração zero e
// permanente false quando ela não trava. A partir de TOTPMaxFailures, a trava
// é permanente.
func TOTPLockFor(falhas int) (duracao time.Duration, permanente bool) {
	if falhas >= TOTPMaxFailures {
		return 0, true
	}
	if falhas < TOTPLockThreshold || falhas%TOTPLockThreshold != 0 {
		return 0, false
	}
	duracao = TOTPLockBase
	for range falhas/TOTPLockThreshold - 1 {
		duracao *= 2
		if duracao >= TOTPLockCap {
			return TOTPLockCap, false
		}
	}
	return duracao, false
}

// ErrTOTPLocked indica que o segundo fator ainda está bloqueado.
var ErrTOTPLocked = errors.New("segundo fator bloqueado")

// VerifyLogin confere o código e grava o desfecho sob a mesma trava de users.
// Assim, só uma falha confirmada pode bloquear o fator e gerar um aviso.
func (s *TOTP) VerifyLogin(ctx context.Context, userID uuid.UUID, code string, ipHash []byte, now time.Time) (SecondFactorProof, error) {
	tx, err := s.beginUserTx(ctx, userID)
	if err != nil {
		return SecondFactorProof{}, fmt.Errorf("abrir transação do segundo fator: %w", err)
	}
	defer tx.Rollback(ctx)

	var falhas int
	var travado bool
	err = tx.QueryRow(ctx, `
		SELECT totp_failures, COALESCE(totp_locked_until > $2, false)
		  FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID, now).Scan(&falhas, &travado)
	if errors.Is(err, pgx.ErrNoRows) {
		return SecondFactorProof{}, ErrNotFound
	}
	if err != nil {
		return SecondFactorProof{}, fmt.Errorf("ler bloqueio do segundo fator: %w", err)
	}
	if travado {
		return SecondFactorProof{}, ErrTOTPLocked
	}

	fator, err := s.fatorTravado(ctx, tx, userID)
	if err != nil {
		return SecondFactorProof{}, err
	}
	if !fator.confirmado {
		return SecondFactorProof{}, ErrTOTPNotEnrolled
	}
	step, recuperacao, err := fator.provar(ctx, tx, userID, code, now)
	if errors.Is(err, ErrTOTPInvalid) {
		return SecondFactorProof{}, gravarFalhaTOTP(ctx, tx, userID, ipHash, falhas+1, now)
	}
	if err != nil {
		return SecondFactorProof{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET totp_last_timestep = COALESCE($2::bigint, totp_last_timestep),
		       totp_failures = 0, totp_locked_until = NULL
		 WHERE id = $1`, userID, step); err != nil {
		return SecondFactorProof{}, fmt.Errorf("gravar acerto do segundo fator: %w", err)
	}
	if recuperacao {
		if err := insertAudit(ctx, tx, userID, "totp.recovery_used", nil, now); err != nil {
			return SecondFactorProof{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SecondFactorProof{}, fmt.Errorf("confirmar acerto do segundo fator: %w", err)
	}
	return SecondFactorProof{sealed: fator.sealed}, nil
}

func gravarFalhaTOTP(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ipHash []byte, falhas int, now time.Time) error {
	duracao, permanente := TOTPLockFor(falhas)
	var ate *time.Time
	if duracao > 0 {
		fim := now.Add(duracao)
		ate = &fim
	}
	travou := permanente || ate != nil
	if _, err := tx.Exec(ctx, `
		UPDATE users
		   SET totp_failures = $2,
		       totp_locked_until = CASE WHEN $3 THEN 'infinity'::timestamptz ELSE $4::timestamptz END,
		       totp_lock_alert_at = CASE WHEN $6 THEN $5 ELSE totp_lock_alert_at END,
		       totp_lock_alert_permanent = CASE WHEN $6 THEN $3 ELSE totp_lock_alert_permanent END
		 WHERE id = $1`, userID, falhas, permanente, ate, now, travou); err != nil {
		return fmt.Errorf("gravar falha do segundo fator: %w", err)
	}
	if travou {
		if err := insertLockAudit(ctx, tx, userID, ipHash, now, acaoTOTPBloqueado, 0); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmar falha do segundo fator: %w", err)
	}
	if travou {
		logTOTPBloqueado(ctx, userID, permanente)
	}
	return ErrTOTPInvalid
}

// TOTPLockAlert é um aviso de bloqueio do segundo fator à espera de envio.
type TOTPLockAlert struct {
	UserID uuid.UUID
	Email  string
	// LockedAt é o instante do bloqueio avisado: entra na chave de
	// idempotência e na condição que tira o aviso da fila.
	LockedAt time.Time
	// Permanent diz se o segundo fator está travado até o CLI.
	Permanent bool
}

// PendingTOTPLockAlerts devolve até limite avisos pendentes, os mais antigos
// primeiro.
func (s *PGStore) PendingTOTPLockAlerts(ctx context.Context, limite int) ([]TOTPLockAlert, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, totp_lock_alert_at, totp_lock_alert_permanent
		  FROM users
		 WHERE totp_lock_alert_at IS NOT NULL
		 ORDER BY totp_lock_alert_at
		 LIMIT $1`, limite)
	if err != nil {
		return nil, fmt.Errorf("listar avisos de bloqueio do segundo fator: %w", err)
	}
	avisos, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (TOTPLockAlert, error) {
		var a TOTPLockAlert
		err := row.Scan(&a.UserID, &a.Email, &a.LockedAt, &a.Permanent)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("ler avisos de bloqueio do segundo fator: %w", err)
	}
	return avisos, nil
}

// ClearTOTPLockAlert tira o aviso da fila só se ele ainda é o do bloqueio de
// lockedAt: um bloqueio novo durante o envio continua pendente.
func (s *PGStore) ClearTOTPLockAlert(ctx context.Context, userID uuid.UUID, lockedAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET totp_lock_alert_at = NULL, totp_lock_alert_permanent = FALSE WHERE id = $1 AND totp_lock_alert_at = $2`,
		userID, lockedAt)
	if err != nil {
		return false, fmt.Errorf("tirar aviso de bloqueio do segundo fator da fila: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TOTPLockAlertStore é a fila dos avisos. *PGStore satisfaz.
type TOTPLockAlertStore interface {
	PendingTOTPLockAlerts(ctx context.Context, limite int) ([]TOTPLockAlert, error)
	ClearTOTPLockAlert(ctx context.Context, userID uuid.UUID, lockedAt time.Time) (bool, error)
}

// TOTPLockMailer manda o e-mail do bloqueio. chave é a chave de idempotência
// do envio. *notify.Resend satisfaz.
type TOTPLockMailer interface {
	SendTOTPLocked(ctx context.Context, email string, permanente bool, chave string) error
}

// totpAvisosPorRodada limita uma rodada de envio; o resto fica para a próxima.
const totpAvisosPorRodada = 20

// SendTOTPLockAlerts faz uma rodada de envio. A chave de idempotência é a do
// bloqueio (usuário e instante): duas réplicas da API na mesma rodada mandam o
// mesmo e-mail uma vez só. Um aviso sai da fila após entrega ou recusa
// definitiva. Falhas transitórias ficam para a próxima rodada.
func SendTOTPLockAlerts(ctx context.Context, store TOTPLockAlertStore, mailer TOTPLockMailer) {
	avisos, err := store.PendingTOTPLockAlerts(ctx, totpAvisosPorRodada)
	if err != nil {
		slog.ErrorContext(ctx, "listar avisos de bloqueio do segundo fator", "erro", err)
		return
	}
	for _, a := range avisos {
		chave := fmt.Sprintf("totp-lock/%s/%d", a.UserID, a.LockedAt.UnixMicro())
		if err := mailer.SendTOTPLocked(ctx, a.Email, a.Permanent, chave); err != nil {
			slog.WarnContext(ctx, "enviar aviso de bloqueio do segundo fator", "user_id", a.UserID, "erro", err)
			var recusa interface{ IsPermanent() bool }
			if !errors.As(err, &recusa) || !recusa.IsPermanent() {
				continue
			}
		}
		if _, err := store.ClearTOTPLockAlert(ctx, a.UserID, a.LockedAt); err != nil {
			slog.ErrorContext(ctx, "tirar aviso de bloqueio do segundo fator da fila", "user_id", a.UserID, "erro", err)
		}
	}
}

// RunTOTPLockAlerts roda SendTOTPLockAlerts a cada intervalo até ctx acabar.
func RunTOTPLockAlerts(ctx context.Context, store TOTPLockAlertStore, mailer TOTPLockMailer, intervalo time.Duration) error {
	t := time.NewTicker(intervalo)
	defer t.Stop()
	for {
		SendTOTPLockAlerts(ctx, store, mailer)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// logTOTPBloqueado registra o bloqueio do segundo fator: como o da conta, ele
// não se anuncia a quem tenta.
func logTOTPBloqueado(ctx context.Context, userID uuid.UUID, permanente bool) {
	slog.WarnContext(ctx, "segundo fator bloqueado por códigos errados",
		"user_id", userID, "permanente", permanente, "trace_id", observability.TraceID(ctx))
}
