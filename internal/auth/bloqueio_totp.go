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

// TOTPAttempts é o bloqueio do segundo fator como Attempts, com o instante do
// verify e o ip_hash de quem tenta (para a auditoria do bloqueio).
func TOTPAttempts(users UserStore, userID uuid.UUID, ipHash []byte, now time.Time) Attempts {
	return totpAttempts{users: users, userID: userID, ipHash: ipHash, now: now}
}

type totpAttempts struct {
	users  UserStore
	userID uuid.UUID
	ipHash []byte
	now    time.Time
}

func (a totpAttempts) Reserve(ctx context.Context) (bool, error) {
	return a.users.ReserveTOTPAttempt(ctx, a.userID, a.ipHash, a.now)
}

func (a totpAttempts) Refund(ctx context.Context) error {
	return a.users.RefundTOTPAttempt(ctx, a.userID)
}

func (a totpAttempts) Reset(ctx context.Context) error {
	return a.users.ResetTOTPFailures(ctx, a.userID)
}

// ReserveTOTPAttempt reserva um código ANTES da verificação, como
// ReserveAttempt: N códigos simultâneos gastam N. Devolve false, sem contar
// nada, se o segundo fator está travado em at. A reserva que trava ainda é
// concedida e verificada; se o código estiver certo, quem chama zera.
//
// O FOR UPDATE serializa as reservas da mesma conta, e é ele que deixa a
// duração ser calculada em Go (TOTPLockFor) a partir da contagem lida. O
// bloqueio grava, no mesmo UPDATE, o aviso pendente ao dono
// (totp_lock_alert_at) e, na mesma transação, a auditoria.
func (s *PGStore) ReserveTOTPAttempt(ctx context.Context, userID uuid.UUID, ipHash []byte, at time.Time) (bool, error) {
	var concedida, travou, permanente bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var falhas int
		var travado bool
		if err := tx.QueryRow(ctx, `
			SELECT totp_failures, totp_locked_until IS NOT NULL AND totp_locked_until > $2
			  FROM users WHERE id = $1 FOR UPDATE`, userID, at).Scan(&falhas, &travado); err != nil {
			return err
		}
		if travado {
			return nil
		}
		concedida = true
		falhas++
		duracao, perm := TOTPLockFor(falhas)
		permanente = perm
		var ate *time.Time
		if duracao > 0 {
			fim := at.Add(duracao)
			ate = &fim
		}
		if _, err := tx.Exec(ctx, `
			UPDATE users
			   SET totp_failures = $2,
			       totp_locked_until = CASE WHEN $3 THEN 'infinity'::timestamptz ELSE $4::timestamptz END,
			       totp_lock_alert_at = CASE WHEN $3 OR $4::timestamptz IS NOT NULL THEN $5 ELSE totp_lock_alert_at END
			 WHERE id = $1`,
			userID, falhas, permanente, ate, at); err != nil {
			return err
		}
		travou = permanente || ate != nil
		if !travou {
			return nil
		}
		return insertLockAudit(ctx, tx, userID, ipHash, at, acaoTOTPBloqueado, 0)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reservar tentativa de segundo fator: %w", err)
	}
	if travou {
		logTOTPBloqueado(ctx, userID, permanente)
	}
	return concedida, nil
}

// RefundTOTPAttempt devolve UMA reserva: a de um verify que falhou por erro
// interno, sem dizer nada sobre o código. Se a reserva devolvida tinha
// travado (a contagem está num ponto de bloqueio), a trava sai com ela. O
// aviso pendente fica: é raro, e avisar a mais é o lado seguro.
func (s *PGStore) RefundTOTPAttempt(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET totp_failures = GREATEST(totp_failures - 1, 0),
		       totp_locked_until = CASE
		           WHEN totp_failures >= $3 OR (totp_failures > 0 AND totp_failures % $2 = 0) THEN NULL
		           ELSE totp_locked_until
		       END
		 WHERE id = $1`,
		userID, TOTPLockThreshold, TOTPMaxFailures)
	if err != nil {
		return fmt.Errorf("devolver tentativa de segundo fator: %w", err)
	}
	return nil
}

// ResetTOTPFailures zera o contador e a trava: só um segundo fator certo (ou o
// reset-2fa do CLI) chega aqui.
func (s *PGStore) ResetTOTPFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET totp_failures = 0, totp_locked_until = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("zerar falhas de segundo fator: %w", err)
	}
	return nil
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
		SELECT id, email, totp_lock_alert_at, totp_locked_until = 'infinity'::timestamptz
		  FROM users
		 WHERE totp_lock_alert_at IS NOT NULL
		 ORDER BY totp_lock_alert_at
		 LIMIT $1`, limite)
	if err != nil {
		return nil, fmt.Errorf("listar avisos de bloqueio do segundo fator: %w", err)
	}
	avisos, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (TOTPLockAlert, error) {
		var a TOTPLockAlert
		var permanente *bool
		err := row.Scan(&a.UserID, &a.Email, &a.LockedAt, &permanente)
		a.Permanent = permanente != nil && *permanente
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
		`UPDATE users SET totp_lock_alert_at = NULL WHERE id = $1 AND totp_lock_alert_at = $2`,
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
// mesmo e-mail uma vez só. O aviso só sai da fila depois do envio; o que
// falha fica para a próxima rodada.
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
			continue
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
