package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Os valores esperados são calculados à mão: 15 min dobrando a cada bloqueio,
// teto de 24 h, e a 50ª falha trava até o CLI.
func TestTOTPLockForGrowsAndEndsInAPermanentLock(t *testing.T) {
	casos := []struct {
		falhas     int
		duracao    time.Duration
		permanente bool
	}{
		{1, 0, false},
		{4, 0, false},
		{5, 15 * time.Minute, false},
		{6, 0, false},
		{10, 30 * time.Minute, false},
		{15, time.Hour, false},
		{20, 2 * time.Hour, false},
		{25, 4 * time.Hour, false},
		{30, 8 * time.Hour, false},
		{35, 16 * time.Hour, false},
		{40, 24 * time.Hour, false},
		{45, 24 * time.Hour, false},
		{49, 0, false},
		{50, 0, true},
		{51, 0, true},
	}
	for _, c := range casos {
		duracao, permanente := auth.TOTPLockFor(c.falhas)
		if duracao != c.duracao || permanente != c.permanente {
			t.Errorf("TOTPLockFor(%d) = (%v, %v), queria (%v, %v)",
				c.falhas, duracao, permanente, c.duracao, c.permanente)
		}
	}
}

func reservarTOTP(t *testing.T, store *auth.PGStore, userID uuid.UUID, at time.Time) bool {
	t.Helper()
	ok, err := store.ReserveTOTPAttempt(context.Background(), userID, nil, at)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// gastarTOTP faz n reservas concedidas em at e falha se alguma for recusada.
func gastarTOTP(t *testing.T, store *auth.PGStore, userID uuid.UUID, at time.Time, n int) {
	t.Helper()
	for i := range n {
		if !reservarTOTP(t, store, userID, at) {
			t.Fatalf("reserva %d de %d recusada em %v", i+1, n, at)
		}
	}
}

type travaTOTP struct {
	falhas     int
	permanente bool
	ate        *time.Time
	avisoEm    *time.Time
}

func lerTravaTOTP(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) travaTOTP {
	t.Helper()
	var tr travaTOTP
	err := pool.QueryRow(context.Background(), `
		SELECT totp_failures, COALESCE(totp_locked_until = 'infinity', false),
		       CASE WHEN totp_locked_until = 'infinity' THEN NULL ELSE totp_locked_until END,
		       totp_lock_alert_at
		  FROM users WHERE id = $1`, id).
		Scan(&tr.falhas, &tr.permanente, &tr.ate, &tr.avisoEm)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Falhas seguidas sem acerto travam por durações crescentes: o primeiro
// bloqueio dura 15 min, o segundo 30. O fim de um bloqueio NÃO zera a contagem,
// ao contrário do bloqueio da conta.
func TestTOTPFailuresLockForGrowingDurations(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, store, userID, t0, 5)
	if reservarTOTP(t, store, userID, t0) {
		t.Fatal("a 6ª reserva passou durante o primeiro bloqueio")
	}
	if reservarTOTP(t, store, userID, t0.Add(15*time.Minute-time.Second)) {
		t.Fatal("o primeiro bloqueio acabou antes de 15 min")
	}

	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, store, userID, t1, 5)
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 10 || got.ate == nil || !got.ate.Equal(t1.Add(30*time.Minute)) {
		t.Fatalf("depois do segundo bloqueio: falhas=%d até=%v, queria 10 e %v", got.falhas, got.ate, t1.Add(30*time.Minute))
	}
	if reservarTOTP(t, store, userID, t1.Add(30*time.Minute-time.Second)) {
		t.Fatal("o segundo bloqueio acabou antes de 30 min")
	}
	if !reservarTOTP(t, store, userID, t1.Add(30*time.Minute)) {
		t.Fatal("a reserva no fim exato do segundo bloqueio foi recusada")
	}
}

// Um acerto zera tudo: depois dele, o próximo bloqueio volta a ser o primeiro,
// de 15 min, e não o segundo.
func TestTOTPResetStartsTheProgressionOver(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, store, userID, t0, 5)
	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, store, userID, t1, 2)
	if err := store.ResetTOTPFailures(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 0 || got.ate != nil || got.permanente {
		t.Fatalf("depois do reset: %+v, queria tudo zerado", got)
	}

	gastarTOTP(t, store, userID, t1, 5)
	if got := lerTravaTOTP(t, pool, userID); got.ate == nil || !got.ate.Equal(t1.Add(15*time.Minute)) {
		t.Fatalf("bloqueio depois do reset até %v, queria %v", got.ate, t1.Add(15*time.Minute))
	}
}

// A 50ª falha trava sem prazo: nem dez anos depois a reserva passa. Só o
// reset-2fa do CLI (a query ResetTOTP) libera.
func TestTOTPAbsoluteCapOnlyTheCLIReleases(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET totp_failures = 49, totp_confirmed_at = $2 WHERE id = $1`, userID, t0); err != nil {
		t.Fatal(err)
	}

	if !reservarTOTP(t, store, userID, t0) {
		t.Fatal("a 50ª reserva foi recusada; ela trava, mas é verificada")
	}
	if got := lerTravaTOTP(t, pool, userID); !got.permanente || got.falhas != 50 {
		t.Fatalf("depois da 50ª: %+v, queria trava permanente com 50 falhas", got)
	}
	if avisos := lerAvisos(t, store); len(avisos) != 1 || !avisos[0].Permanent {
		t.Fatalf("avisos = %+v, queria um, permanente", avisos)
	}
	if reservarTOTP(t, store, userID, t0.AddDate(10, 0, 0)) {
		t.Fatal("a trava permanente venceu com o tempo")
	}

	if n, err := db.New(pool).ResetTOTP(ctx, userID); err != nil || n != 1 {
		t.Fatalf("ResetTOTP = %d, %v", n, err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 0 || got.ate != nil || got.permanente {
		t.Fatalf("depois do reset-2fa: %+v, queria tudo zerado", got)
	}
	if !reservarTOTP(t, store, userID, t0) {
		t.Fatal("depois do reset-2fa a reserva continuou recusada")
	}
}

// O contador do 2FA é só dele: falhas de senha (o que um anônimo consegue
// gastar) não o movem, e o do 2FA não move o da conta.
func TestPasswordAndTOTPCountersAreSeparate(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	for range auth.LockThreshold {
		reservar(t, store, userID, t0)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 0 || got.ate != nil {
		t.Fatalf("falhas de senha mexeram no 2FA: %+v", got)
	}
	if err := store.ResetFailures(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	gastarTOTP(t, store, userID, t0, 3)
	if falhas, ate := lerTravas(t, pool, userID); falhas != 0 || ate != nil {
		t.Fatalf("falhas do 2FA mexeram na conta: %d, %v", falhas, ate)
	}
}

// A devolução de uma reserva que travou tira a trava junto, como no bloqueio
// da conta.
func TestRefundTOTPAttemptUndoesTheLockItApplied(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, store, userID, t0, 5)
	if err := store.RefundTOTPAttempt(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 4 || got.ate != nil {
		t.Fatalf("depois da devolução: %+v, queria 4 falhas sem trava", got)
	}
	if !reservarTOTP(t, store, userID, t0) {
		t.Fatal("a trava devolvida continuou valendo")
	}
}

// Cada bloqueio deixa um aviso pendente para o dono; abaixo do limiar, nenhum.
// O aviso só sai da fila com o carimbo do bloqueio que ele anuncia: um
// bloqueio novo no meio do envio continua pendente.
func TestTOTPLockLeavesAPendingAlertUntilItIsCleared(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, store, userID, t0, 4)
	if pendentes := lerAvisos(t, store); len(pendentes) != 0 {
		t.Fatalf("aviso antes do bloqueio: %+v", pendentes)
	}
	gastarTOTP(t, store, userID, t0, 1)
	pendentes := lerAvisos(t, store)
	if len(pendentes) != 1 || pendentes[0].UserID != userID || pendentes[0].Email != "pessoa@exemplo.com" ||
		!pendentes[0].LockedAt.Equal(t0) || pendentes[0].Permanent {
		t.Fatalf("avisos = %+v, queria um, do bloqueio de %v, não permanente", pendentes, t0)
	}

	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, store, userID, t1, 5)
	if limpou, err := store.ClearTOTPLockAlert(ctx, userID, t0); err != nil || limpou {
		t.Fatalf("ClearTOTPLockAlert com o carimbo velho = %v, %v; queria false", limpou, err)
	}
	if limpou, err := store.ClearTOTPLockAlert(ctx, userID, t1); err != nil || !limpou {
		t.Fatalf("ClearTOTPLockAlert com o carimbo atual = %v, %v; queria true", limpou, err)
	}
	if pendentes := lerAvisos(t, store); len(pendentes) != 0 {
		t.Fatalf("aviso continuou pendente: %+v", pendentes)
	}
}

func lerAvisos(t *testing.T, store *auth.PGStore) []auth.TOTPLockAlert {
	t.Helper()
	avisos, err := store.PendingTOTPLockAlerts(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	return avisos
}
