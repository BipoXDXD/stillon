package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

func lerTravas(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (int, *time.Time) {
	t.Helper()
	var tentativas int
	var ate *time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT failed_attempts, locked_until FROM users WHERE id = $1`, id).
		Scan(&tentativas, &ate)
	if err != nil {
		t.Fatal(err)
	}
	return tentativas, ate
}

func TestByEmailFindsTheUser(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)

	u, err := store.ByEmail(context.Background(), "pessoa@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != userID {
		t.Errorf("id = %v, queria %v", u.ID, userID)
	}
	if u.PasswordHash == "" {
		t.Error("ByEmail não trouxe o hash de senha")
	}
	if u.FailedAttempts != 0 || u.LockedUntil != nil {
		t.Errorf("usuário novo já vem com trava: %d / %v", u.FailedAttempts, u.LockedUntil)
	}
}

// ErrNotFound é o que permite ao login distinguir "não existe" de "falhou a
// consulta" — e tratar só o primeiro com VerifyDummy.
func TestByEmailReturnsErrNotFoundForUnknownEmail(t *testing.T) {
	_, pool, _, _ := setup(t)
	store := auth.NewPGStore(pool)

	_, err := store.ByEmail(context.Background(), "ninguem@exemplo.com")
	if !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("erro = %v, queria ErrNotFound", err)
	}
}

// O bloqueio só pode cair no limiar, nunca antes: travar cedo seria negação
// de serviço contra o dono da conta.
func TestRecordFailureLocksExactlyAtTheThreshold(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	agora := clk.Now()

	for i := 1; i < auth.LockThreshold; i++ {
		if err := store.RecordFailure(ctx, userID, agora); err != nil {
			t.Fatal(err)
		}
		tentativas, ate := lerTravas(t, pool, userID)
		if tentativas != i {
			t.Errorf("depois de %d falhas, failed_attempts = %d", i, tentativas)
		}
		if ate != nil {
			t.Fatalf("conta travou com apenas %d falhas (limiar é %d)", i, auth.LockThreshold)
		}
	}

	if err := store.RecordFailure(ctx, userID, agora); err != nil {
		t.Fatal(err)
	}
	tentativas, ate := lerTravas(t, pool, userID)
	if tentativas != auth.LockThreshold {
		t.Errorf("failed_attempts = %d, queria %d", tentativas, auth.LockThreshold)
	}
	if ate == nil {
		t.Fatal("conta não travou no limiar")
	}
	if querido := agora.Add(auth.LockDuration); !ate.Equal(querido) {
		t.Errorf("locked_until = %v, queria %v", ate, querido)
	}
}

func TestResetFailuresClearsTheLock(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	for i := 0; i < auth.LockThreshold; i++ {
		if err := store.RecordFailure(ctx, userID, clk.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ResetFailures(ctx, userID); err != nil {
		t.Fatal(err)
	}

	tentativas, ate := lerTravas(t, pool, userID)
	if tentativas != 0 || ate != nil {
		t.Errorf("depois do reset: %d falhas, locked_until = %v", tentativas, ate)
	}
}

// IsLocked compara com o relógio injetado: um bloqueio vencido não bloqueia.
func TestIsLockedRespectsTheDeadline(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	futuro := agora.Add(time.Minute)
	passado := agora.Add(-time.Minute)

	if (auth.User{}).IsLocked(agora) {
		t.Error("usuário sem locked_until apareceu como bloqueado")
	}
	if !(auth.User{LockedUntil: &futuro}).IsLocked(agora) {
		t.Error("bloqueio no futuro deveria bloquear")
	}
	if (auth.User{LockedUntil: &passado}).IsLocked(agora) {
		t.Error("bloqueio vencido continuou bloqueando")
	}
}

func TestUpdatePasswordHashPersistsTheNewHash(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	novo, err := auth.HashPassword("outra-senha-bem-comprida")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePasswordHash(ctx, userID, novo); err != nil {
		t.Fatal(err)
	}

	u, err := store.ByEmail(ctx, "pessoa@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != novo {
		t.Errorf("hash gravado = %q, queria %q", u.PasswordHash, novo)
	}
}

// Compilação: o mesmo *PGStore precisa satisfazer Store e UserStore, porque
// é ele que o wiring entrega às duas interfaces.
var (
	_ auth.Store     = (*auth.PGStore)(nil)
	_ auth.UserStore = (*auth.PGStore)(nil)
)

// TestExpiredLockRestartsTheFailureCount fecha um DoS de conta permanente.
//
// O contador só era zerado por ResetFailures, que só roda num login BEM
// SUCEDIDO — e o login bem sucedido é recusado enquanto a conta está travada.
// Então, vencido o bloqueio, failed_attempts continuava no limiar e UMA senha
// errada re-travava por mais quinze minutos. Um atacante com uma requisição a
// cada quinze minutos (96 por dia, três ordens de grandeza abaixo do
// limitador) mantinha a vítima fora da própria conta para sempre, sem nunca
// ganhar acesso e sem que ela descobrisse o motivo: o bloqueio é silencioso
// por design (R34).
func TestExpiredLockRestartsTheFailureCount(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	agora := clk.Now()

	for range auth.LockThreshold {
		if err := store.RecordFailure(ctx, userID, agora); err != nil {
			t.Fatal(err)
		}
	}
	if _, ate := lerTravas(t, pool, userID); ate == nil {
		t.Fatal("a conta não travou no limiar; o resto do teste seria vácuo")
	}

	// Depois do vencimento, uma única falha não pode re-travar.
	depois := agora.Add(auth.LockDuration).Add(time.Minute)
	if err := store.RecordFailure(ctx, userID, depois); err != nil {
		t.Fatal(err)
	}

	tentativas, ate := lerTravas(t, pool, userID)
	if tentativas != 1 {
		t.Errorf("failed_attempts = %d depois da primeira falha pós-vencimento, queria 1", tentativas)
	}
	if ate != nil && depois.Before(*ate) {
		t.Errorf("uma única falha re-travou a conta até %v: DoS permanente a 96 requisições por dia", ate)
	}
}

// A contagem dentro de uma janela ainda válida continua somando: corrigir o
// vencimento não pode virar um reset que apaga a força bruta em curso.
func TestFailuresStillAccumulateWhileTheLockIsLive(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()
	agora := clk.Now()

	for i := range 3 {
		if err := store.RecordFailure(ctx, userID, agora.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if tentativas, _ := lerTravas(t, pool, userID); tentativas != 3 {
		t.Errorf("failed_attempts = %d, queria 3: a contagem parou de somar", tentativas)
	}
}

// Só o espaço em volta sai; a caixa fica (o citext compara sem ela).
func TestNormalizeEmail(t *testing.T) {
	if got := auth.NormalizeEmail("  Pessoa@Exemplo.com \t"); got != "Pessoa@Exemplo.com" {
		t.Errorf("NormalizeEmail = %q", got)
	}
}

// CheckPassword é a verificação do login para toda rota que pede a senha. Os
// três caminhos: senha certa não mexe no contador (quem chama decide quando
// zerar); senha errada conta uma falha; conta bloqueada recusa até a senha
// certa, sem contar falha nova.
func TestCheckPassword(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	hash, err := auth.HashPassword(senha)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePasswordHash(ctx, userID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFailure(ctx, userID, clk.Now()); err != nil {
		t.Fatal(err)
	}
	u, err := store.ByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}

	ok, _, err := auth.CheckPassword(ctx, store, u, senha, clk.Now())
	if err != nil || !ok {
		t.Fatalf("senha certa: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 1 {
		t.Errorf("senha certa mexeu no contador: %d falhas, queria 1", n)
	}

	ok, _, err = auth.CheckPassword(ctx, store, u, "senha-errada-com-mais-de-15", clk.Now())
	if err != nil || ok {
		t.Fatalf("senha errada: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 2 {
		t.Errorf("senha errada não contou: %d falhas, queria 2", n)
	}

	ate := clk.Now().Add(time.Minute)
	u.LockedUntil = &ate
	ok, _, err = auth.CheckPassword(ctx, store, u, senha, clk.Now())
	if err != nil || ok {
		t.Fatalf("conta bloqueada aceitou a senha certa: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 2 {
		t.Errorf("conta bloqueada contou falha: %d, queria 2", n)
	}
}
