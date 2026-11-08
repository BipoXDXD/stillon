package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
)

// Os testes deste arquivo fecham lacunas achadas por mutação: cada um falha
// com uma regra específica do pacote desligada.

func assinar(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// O token é válido em tudo, menos na audience: sem a checagem, o token de
// outro propósito valeria como credencial completa.
func TestAccessTokenWithAudienceIsRejected(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw := assinar(t, jwt.MapClaims{
		"sub": uuid.NewString(), "sid": uuid.NewString(), "exp": now.Add(time.Minute).Unix(), "aud": "outro-proposito",
	})
	if _, err := auth.ParseAccessToken(jwtSecret, raw, now); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("erro = %v, queria ErrInvalidToken", err)
	}
}

// O desafio tem instante de emissão e falta só o carimbo: é o carimbo que
// recusa, e não a checagem do instante.
func TestChallengeTokenWithAnIssueInstantStillRequiresTheStamp(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw := assinar(t, jwt.MapClaims{
		"sub": uuid.NewString(), "aud": []string{"totp-challenge"}, "exp": now.Add(time.Minute).Unix(), "cat": now.UnixMicro(),
	})
	if _, err := auth.ParseChallengeToken(jwtSecret, raw, now); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("erro = %v, queria ErrInvalidToken", err)
	}
}

// O caminho do e-mail inexistente paga um Argon2id de verdade.
func TestVerifyDummyPaysOneVerification(t *testing.T) {
	antes := auth.Argon2Verifications()
	if err := auth.VerifyDummy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := auth.Argon2Verifications() - antes; got != 1 {
		t.Errorf("%d verificações, queria 1", got)
	}
}

// A prova de segundo fator sem segredo não vale para quem não tem 2FA: nil e
// nil são iguais para bytes.Equal, e a sessão sairia sem fator nenhum.
func TestSecondFactorProofWithoutASecretIsStale(t *testing.T) {
	mgr, _, _, userID := setup(t)
	_, _, err := mgr.IssueAfterSecondFactor(context.Background(), userID, carimbo, auth.SecondFactorProof{}, meta)
	if !errors.Is(err, auth.ErrStaleSecondFactor) {
		t.Errorf("erro = %v, queria ErrStaleSecondFactor", err)
	}
}

// On point do vencimento: no instante exato de expires_at o refresh ainda vale.
func TestRefreshStillRotatesAtTheExactExpiry(t *testing.T) {
	mgr, _, clk, userID := setup(t)
	ctx := context.Background()
	tok, _, err := mgr.Issue(ctx, userID, carimbo, meta)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(auth.RefreshTTL)
	if _, _, err := mgr.Rotate(ctx, tok, meta); err != nil {
		t.Errorf("rotação no instante do vencimento: %v", err)
	}
}

type txQueRegistra struct {
	txFalsa
	ordem *[]string
}

func (t txQueRegistra) anotar(passo string) { *t.ordem = append(*t.ordem, passo) }

func (t txQueRegistra) LockUserSessions(context.Context, uuid.UUID) error {
	t.anotar("trava do usuário")
	return nil
}

func (t txQueRegistra) UserPasswordHash(ctx context.Context, id uuid.UUID) (string, error) {
	t.anotar("lê o hash")
	return t.txFalsa.UserPasswordHash(ctx, id)
}

func (t txQueRegistra) SessionUserByHash(ctx context.Context, h []byte) (uuid.UUID, error) {
	t.anotar("acha o usuário")
	return t.txFalsa.SessionUserByHash(ctx, h)
}

func (t txQueRegistra) LockSessionByHash(ctx context.Context, h []byte) (auth.SessionRow, error) {
	t.anotar("trava a linha")
	return t.txFalsa.LockSessionByHash(ctx, h)
}

type storeDeTx struct{ tx auth.Tx }

func (s storeDeTx) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return fn(ctx, s.tx)
}

// A trava do usuário vem antes da leitura do hash: é ela que fecha a corrida
// com uma redefinição de senha (D1 do Plano 5).
func TestIssueLocksTheUserBeforeReadingTheHash(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	var ordem []string
	mgr := auth.NewManager(storeDeTx{txQueRegistra{txFalsa{usadaEm: agora}, &ordem}}, clock.NewFake(agora), ipKey)
	if _, _, err := mgr.Issue(context.Background(), uuid.New(), carimbo, meta); err != nil {
		t.Fatal(err)
	}
	if quer := []string{"trava do usuário", "lê o hash"}; !slices.Equal(ordem, quer) {
		t.Errorf("ordem = %v, queria %v", ordem, quer)
	}
}

// Quem muda sessões pega a trava do usuário antes da trava da linha.
func TestRotateLocksTheUserBeforeTheRow(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	var ordem []string
	mgr := auth.NewManager(storeDeTx{txQueRegistra{txFalsa{usadaEm: agora}, &ordem}}, clock.NewFake(agora), ipKey)
	// A sessão falsa já foi usada: o Rotate termina em reúso, depois das travas.
	if _, _, err := mgr.Rotate(context.Background(), "qualquer", meta); !errors.Is(err, auth.ErrTokenReuse) {
		t.Fatalf("erro = %v, queria ErrTokenReuse", err)
	}
	if quer := []string{"acha o usuário", "trava do usuário", "trava a linha"}; !slices.Equal(ordem, quer) {
		t.Errorf("ordem = %v, queria %v", ordem, quer)
	}
}

// On point de familiaParada: com exatamente 24 h sem reserva, a família fica.
func TestFamilyProofsKeepAFamilyIdleForExactlyTheLimit(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	provas := auth.NewFamilyProofs(clk)
	provas.Reserve(uuid.New())
	clk.Advance(24 * time.Hour)
	provas.Reserve(uuid.New())
	if got := provas.Len(); got != 2 {
		t.Errorf("%d famílias, queria 2", got)
	}
}

// Um sucesso zera a família: as provas que estavam em voo não contam mais.
func TestFamilyProofsInFlightFailuresAfterASuccessDoNotCount(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()
	emVoo := make([]*auth.ProofReservation, 0, auth.FamilyProofLimit)
	for range auth.FamilyProofLimit {
		emVoo = append(emVoo, provas.Reserve(familia))
	}
	provas.Reserve(familia).Succeed()
	for i, r := range emVoo {
		if r.Fail() {
			t.Errorf("a falha em voo %d derrubou a família depois do sucesso", i+1)
		}
	}
}

// O sucesso de uma reserva antiga não zera a conta que nasceu depois dela.
func TestFamilyProofsOldSuccessDoesNotResetANewCount(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()
	velha := provas.Reserve(familia)
	provas.Reserve(familia).Succeed()
	for range auth.FamilyProofLimit - 1 {
		provas.Reserve(familia).Fail()
	}
	velha.Succeed()
	if !provas.Reserve(familia).Fail() {
		t.Error("a falha do limite não derrubou a família: o sucesso antigo zerou a conta nova")
	}
}

// Reserva devolvida não aproxima a família do esgotamento.
func TestFamilyProofsReleasedReservationsDoNotExhaust(t *testing.T) {
	provas := auth.NewFamilyProofs(clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)))
	familia := uuid.New()
	for range auth.FamilyProofLimit + 1 {
		provas.Reserve(familia).Release()
	}
	if provas.Reserve(familia).Exhausted() {
		t.Error("reservas devolvidas esgotaram a família")
	}
}

// On point: no instante de locked_until a conta já não está bloqueada.
func TestIsLockedEndsAtTheExactDeadline(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if (auth.User{LockedUntil: &agora}).IsLocked(agora) {
		t.Error("o bloqueio ainda valia no instante do vencimento")
	}
}

// No instante do vencimento, a reserva é concedida e a contagem recomeça: as
// duas metades do UPDATE usam a mesma fronteira.
func TestReserveAttemptAtTheExactLockExpiryRestartsTheCount(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	agora := clk.Now()
	for range auth.LockThreshold {
		reservar(t, store, userID, agora)
	}
	if !reservar(t, store, userID, agora.Add(auth.LockDuration)) {
		t.Fatal("a reserva no instante do vencimento foi recusada")
	}
	if tentativas, ate := lerTravas(t, pool, userID); tentativas != 1 || ate != nil {
		t.Errorf("failed_attempts = %d, locked_until = %v; queria 1 e nil", tentativas, ate)
	}
}

// O dispositivo parado há mais de um dia sai do contador: o mapa não cresce.
func TestDeviceAttemptsForgetIdleDevices(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	dispositivos := auth.NewDevices([]byte("chave-mestra-de-teste-32-bytes!!"), clk)
	ctx := context.Background()
	if _, err := dispositivos.Attempts(auth.DeviceID{1}).Reserve(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Advance(24*time.Hour + time.Second)
	if _, err := dispositivos.Attempts(auth.DeviceID{2}).Reserve(ctx); err != nil {
		t.Fatal(err)
	}
	if got := dispositivos.Len(); got != 1 {
		t.Errorf("%d dispositivos, queria 1", got)
	}
}

// A janela de skew é de um período para cada lado: dois já ficam de fora.
func TestVerifyRejectsACodeTwoPeriodsAway(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)
	for nome, deslocamento := range map[string]time.Duration{"atrás": -time.Minute, "à frente": time.Minute} {
		t.Run(nome, func(t *testing.T) {
			svc, pool, userID := setupTOTP(t)
			gravarSegredoTOTP(t, pool, userID, secret)
			codigo, err := totp.GenerateCode(secret, now.Add(deslocamento))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Verify(context.Background(), userID, codigo, now); !errors.Is(err, auth.ErrTOTPInvalid) {
				t.Errorf("erro = %v, queria ErrTOTPInvalid", err)
			}
		})
	}
}

// Desligar o 2FA leva junto os códigos de recuperação.
func TestDisableRemovesTheRecoveryCodes(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)
	uri, err := svc.Enroll(ctx, userID, "pessoa@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	secret := secretFromURI(t, uri)
	codigo, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, codigo, now); err != nil {
		t.Fatal(err)
	}
	depois := now.Add(time.Minute)
	codigo, err = totp.GenerateCode(secret, depois)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Disable(ctx, userID, codigo, depois); err != nil {
		t.Fatal(err)
	}
	var restantes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID).Scan(&restantes); err != nil {
		t.Fatal(err)
	}
	if restantes != 0 {
		t.Errorf("%d códigos de recuperação sobraram, queria 0", restantes)
	}
}

// Linha confirmada sem segredo é conta sem segundo fator, e não erro interno.
func TestDisableTreatsAConfirmationWithoutASecretAsNotEnrolled(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 12, 0, 15, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_confirmed_at = $2 WHERE id = $1`, userID, now); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disable(ctx, userID, "000000", now); !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("erro = %v, queria ErrTOTPNotEnrolled", err)
	}
}

// Quem provou o fator atual na troca de autenticador zera o bloqueio da conta,
// no início e na confirmação.
func TestRotationClearsTheAccountLock(t *testing.T) {
	travar := func(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), `UPDATE users SET failed_attempts = 3, locked_until = $2 WHERE id = $1`,
			userID, inicioDaTroca.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	conferir := func(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
		t.Helper()
		if tentativas, ate := lerTravas(t, pool, userID); tentativas != 0 || ate != nil {
			t.Errorf("failed_attempts = %d, locked_until = %v; queria 0 e nil", tentativas, ate)
		}
	}
	t.Run("início", func(t *testing.T) {
		svc, pool, userID := setupTOTP(t)
		travar(t, pool, userID)
		iniciarTroca(t, svc, pool, userID, familiaViva(t, pool, userID, inicioDaTroca))
		conferir(t, pool, userID)
	})
	t.Run("confirmação", func(t *testing.T) {
		svc, pool, userID := setupTOTP(t)
		familia := familiaViva(t, pool, userID, inicioDaTroca)
		novo := iniciarTroca(t, svc, pool, userID, familia)
		travar(t, pool, userID)
		em := inicioDaTroca.Add(time.Minute)
		if _, err := svc.ConfirmRotation(context.Background(), userID, familia, rotacaoAtual(t, pool, userID), codigoEm(t, novo, em), em); err != nil {
			t.Fatal(err)
		}
		conferir(t, pool, userID)
	})
}

func TestEmailPossibleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		nome  string
		email string
		quer  bool
	}{
		{"254 bytes", strings.Repeat("a", 248) + "@x.com", true},
		{"255 bytes", strings.Repeat("a", 249) + "@x.com", false},
		{"caractere de controle", "a\x00@x.com", false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			if got := auth.EmailPossivel(tc.email); got != tc.quer {
				t.Errorf("EmailPossivel = %v, queria %v", got, tc.quer)
			}
		})
	}
}
