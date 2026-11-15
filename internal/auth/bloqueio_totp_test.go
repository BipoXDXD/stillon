package auth_test

import (
	"context"
	"errors"
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

func prepararTOTPProgressivo(t *testing.T) (*auth.TOTP, *pgxpool.Pool, uuid.UUID, string) {
	t.Helper()
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	ativacao := time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC)
	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	segredo := secretFromURI(t, uri)
	if _, err := svc.Confirm(ctx, userID, familiaViva(t, pool, userID, ativacao), codigoEm(t, segredo, ativacao), ativacao); err != nil {
		t.Fatal(err)
	}
	return svc, pool, userID, segredo
}

func registrarErroTOTP(t *testing.T, svc *auth.TOTP, userID uuid.UUID, at time.Time) bool {
	t.Helper()
	_, err := svc.VerifyLogin(context.Background(), userID, "", nil, at)
	switch {
	case errors.Is(err, auth.ErrTOTPInvalid):
		return true
	case errors.Is(err, auth.ErrTOTPLocked):
		return false
	default:
		t.Fatalf("verificar código de teste: %v", err)
		return false
	}
}

func gastarTOTP(t *testing.T, svc *auth.TOTP, userID uuid.UUID, at time.Time, n int) {
	t.Helper()
	for i := range n {
		if !registrarErroTOTP(t, svc, userID, at) {
			t.Fatalf("tentativa %d de %d bloqueada em %v", i+1, n, at)
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
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, svc, userID, t0, 5)
	if registrarErroTOTP(t, svc, userID, t0) {
		t.Fatal("a 6ª reserva passou durante o primeiro bloqueio")
	}
	if registrarErroTOTP(t, svc, userID, t0.Add(15*time.Minute-time.Second)) {
		t.Fatal("o primeiro bloqueio acabou antes de 15 min")
	}

	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, svc, userID, t1, 5)
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 10 || got.ate == nil || !got.ate.Equal(t1.Add(30*time.Minute)) {
		t.Fatalf("depois do segundo bloqueio: falhas=%d até=%v, queria 10 e %v", got.falhas, got.ate, t1.Add(30*time.Minute))
	}
	if registrarErroTOTP(t, svc, userID, t1.Add(30*time.Minute-time.Second)) {
		t.Fatal("o segundo bloqueio acabou antes de 30 min")
	}
	if !registrarErroTOTP(t, svc, userID, t1.Add(30*time.Minute)) {
		t.Fatal("a reserva no fim exato do segundo bloqueio foi recusada")
	}
}

func TestConcurrentTOTPFailuresStopAtTheLock(t *testing.T) {
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	const tentativas = auth.TOTPLockThreshold + 3
	inicio := make(chan struct{})
	resultados := make(chan error, tentativas)

	for range tentativas {
		go func() {
			<-inicio
			_, err := svc.VerifyLogin(ctx, userID, "", nil, now)
			resultados <- err
		}()
	}
	close(inicio)
	var invalidos, bloqueados int
	for range tentativas {
		switch err := <-resultados; {
		case errors.Is(err, auth.ErrTOTPInvalid):
			invalidos++
		case errors.Is(err, auth.ErrTOTPLocked):
			bloqueados++
		default:
			t.Errorf("resultado inesperado: %v", err)
		}
	}
	if invalidos != auth.TOTPLockThreshold || bloqueados != tentativas-auth.TOTPLockThreshold {
		t.Fatalf("inválidos=%d bloqueados=%d, queria %d/%d", invalidos, bloqueados, auth.TOTPLockThreshold, tentativas-auth.TOTPLockThreshold)
	}
	if estado := lerTravaTOTP(t, pool, userID); estado.falhas != invalidos || estado.ate == nil || !estado.ate.Equal(now.Add(auth.TOTPLockBase)) {
		t.Fatalf("estado após as tentativas: %+v", estado)
	}
}

// Um acerto zera tudo: depois dele, o próximo bloqueio volta a ser o primeiro,
// de 15 min, e não o segundo.
func TestTOTPResetStartsTheProgressionOver(t *testing.T) {
	svc, pool, userID, segredo := prepararTOTPProgressivo(t)
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, svc, userID, t0, 5)
	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, svc, userID, t1, 2)
	if _, err := svc.VerifyLogin(context.Background(), userID, codigoEm(t, segredo, t1), nil, t1); err != nil {
		t.Fatal(err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 0 || got.ate != nil || got.permanente {
		t.Fatalf("depois do reset: %+v, queria tudo zerado", got)
	}

	gastarTOTP(t, svc, userID, t1, 5)
	if got := lerTravaTOTP(t, pool, userID); got.ate == nil || !got.ate.Equal(t1.Add(15*time.Minute)) {
		t.Fatalf("bloqueio depois do reset até %v, queria %v", got.ate, t1.Add(15*time.Minute))
	}
}

// A 50ª falha trava sem prazo: nem dez anos depois a reserva passa. Só o
// reset-2fa do CLI (a query ResetTOTP) libera.
func TestTOTPAbsoluteCapOnlyTheCLIReleases(t *testing.T) {
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET totp_failures = 49 WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}

	if !registrarErroTOTP(t, svc, userID, t0) {
		t.Fatal("a 50ª reserva foi recusada; ela trava, mas é verificada")
	}
	if got := lerTravaTOTP(t, pool, userID); !got.permanente || got.falhas != 50 {
		t.Fatalf("depois da 50ª: %+v, queria trava permanente com 50 falhas", got)
	}
	if avisos := lerAvisos(t, store); len(avisos) != 1 || !avisos[0].Permanent {
		t.Fatalf("avisos = %+v, queria um, permanente", avisos)
	}
	if registrarErroTOTP(t, svc, userID, t0.AddDate(10, 0, 0)) {
		t.Fatal("a trava permanente venceu com o tempo")
	}

	if n, err := db.New(pool).ResetTOTP(ctx, userID); err != nil || n != 1 {
		t.Fatalf("ResetTOTP = %d, %v", n, err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 0 || got.ate != nil || got.permanente {
		t.Fatalf("depois do reset-2fa: %+v, queria tudo zerado", got)
	}
	if _, err := svc.VerifyLogin(ctx, userID, "", nil, t0); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Fatalf("depois do reset-2fa: %v, queria fator desativado", err)
	}
	if avisos := lerAvisos(t, store); len(avisos) != 1 || !avisos[0].Permanent || !avisos[0].LockedAt.Equal(t0) {
		t.Fatalf("aviso deixou de representar o bloqueio registrado: %+v", avisos)
	}
}

func TestReset2FAAllowsReenrollmentAfterAPermanentLockAndDisable(t *testing.T) {
	svc, pool, userID := desligarComTrava(t, "infinity", auth.TOTPMaxFailures, true)
	ctx := context.Background()
	q := db.New(pool)
	if n, err := q.ResetTOTP(ctx, userID); err != nil || n != 1 {
		t.Fatalf("reset com fator desligado = %d, %v, queria uma linha", n, err)
	}
	if n, err := q.ResetTOTP(ctx, userID); err != nil || n != 0 {
		t.Fatalf("segundo reset = %d, %v, queria zero linhas", n, err)
	}

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	uri, err := svc.Enroll(ctx, userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	segredo := secretFromURI(t, uri)
	if _, err := svc.Confirm(ctx, userID, familiaViva(t, pool, userID, now), codigoEm(t, segredo, now), now); err != nil {
		t.Fatal(err)
	}
	verificacao := now.Add(time.Minute)
	if _, err := svc.VerifyLogin(ctx, userID, codigoEm(t, segredo, verificacao), nil, verificacao); err != nil {
		t.Fatalf("verificar fator recadastrado: %v", err)
	}
}

// O contador do 2FA é só dele: falhas de senha (o que um anônimo consegue
// gastar) não o movem, e o do 2FA não move o da conta.
func TestPasswordAndTOTPCountersAreSeparate(t *testing.T) {
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
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
	gastarTOTP(t, svc, userID, t0, 3)
	if falhas, ate := lerTravas(t, pool, userID); falhas != 0 || ate != nil {
		t.Fatalf("falhas do 2FA mexeram na conta: %d, %v", falhas, ate)
	}
}

// Erro de leitura depois de quatro códigos incorretos não fecha a janela
// de tentativas nem produz aviso.
func TestInternalTOTPFailureDoesNotApplyALock(t *testing.T) {
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, svc, userID, t0, 4)
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_secret_enc = $2 WHERE id = $1`, userID, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyLogin(ctx, userID, "", nil, t0); err == nil || errors.Is(err, auth.ErrTOTPInvalid) {
		t.Fatalf("segredo ilegível retornou %v, queria erro interno", err)
	}
	if got := lerTravaTOTP(t, pool, userID); got.falhas != 4 || got.ate != nil || got.avisoEm != nil {
		t.Fatalf("depois do erro interno: %+v, queria 4 falhas sem trava nem aviso", got)
	}
}

// Cada bloqueio deixa um aviso pendente para o dono; abaixo do limiar, nenhum.
// O aviso só sai da fila com o carimbo do bloqueio que ele anuncia: um
// bloqueio novo no meio do envio continua pendente.
func TestTOTPLockLeavesAPendingAlertUntilItIsCleared(t *testing.T) {
	svc, pool, userID, _ := prepararTOTPProgressivo(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	gastarTOTP(t, svc, userID, t0, 4)
	if pendentes := lerAvisos(t, store); len(pendentes) != 0 {
		t.Fatalf("aviso antes do bloqueio: %+v", pendentes)
	}
	gastarTOTP(t, svc, userID, t0, 1)
	pendentes := lerAvisos(t, store)
	if len(pendentes) != 1 || pendentes[0].UserID != userID || pendentes[0].Email != "totp@exemplo.com" ||
		!pendentes[0].LockedAt.Equal(t0) || pendentes[0].Permanent {
		t.Fatalf("avisos = %+v, queria um, do bloqueio de %v, não permanente", pendentes, t0)
	}

	t1 := t0.Add(15 * time.Minute)
	gastarTOTP(t, svc, userID, t1, 5)
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
