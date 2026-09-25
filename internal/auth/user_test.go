package auth_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
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

// reservar é ReserveAttempt com o teste parando no erro.
func reservar(t *testing.T, store *auth.PGStore, userID uuid.UUID, at time.Time) bool {
	t.Helper()
	ok, err := store.ReserveAttempt(context.Background(), userID, at)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// O bloqueio só pode cair no limiar, nunca antes: travar cedo seria negação
// de serviço contra o dono da conta. A reserva que atinge o limiar trava a
// conta e ainda é concedida (é a quinta tentativa, e é verificada); a
// seguinte é recusada e não conta nada (A2 do Plano 10).
func TestReserveAttemptLocksExactlyAtTheThreshold(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	agora := clk.Now()

	for i := 1; i < auth.LockThreshold; i++ {
		if !reservar(t, store, userID, agora) {
			t.Fatalf("reserva %d recusada antes do limiar", i)
		}
		tentativas, ate := lerTravas(t, pool, userID)
		if tentativas != i {
			t.Errorf("depois de %d reservas, failed_attempts = %d", i, tentativas)
		}
		if ate != nil {
			t.Fatalf("conta travou com apenas %d reservas (limiar é %d)", i, auth.LockThreshold)
		}
	}

	if !reservar(t, store, userID, agora) {
		t.Fatal("a reserva do limiar foi recusada: a quinta tentativa nem seria verificada")
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

	if reservar(t, store, userID, agora) {
		t.Error("a conta travada concedeu mais uma reserva")
	}
	if tentativas, _ := lerTravas(t, pool, userID); tentativas != auth.LockThreshold {
		t.Errorf("a reserva recusada contou: failed_attempts = %d, queria %d", tentativas, auth.LockThreshold)
	}
}

// A2 (Plano 10): reservas simultâneas não furam o limiar. Doze de uma vez
// concedem exatamente LockThreshold, e o contador para nele.
func TestConcurrentReservationsStopAtTheThreshold(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)

	var concedidas atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			ok, err := store.ReserveAttempt(context.Background(), userID, clk.Now())
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				concedidas.Add(1)
			}
		})
	}
	wg.Wait()
	if n := concedidas.Load(); n != auth.LockThreshold {
		t.Errorf("%d reservas concedidas em 12 simultâneas, queria %d", n, auth.LockThreshold)
	}
	if tentativas, _ := lerTravas(t, pool, userID); tentativas != auth.LockThreshold {
		t.Errorf("failed_attempts = %d, queria %d", tentativas, auth.LockThreshold)
	}
}

// RefundAttempt devolve uma reserva só: a da senha certa com o 2FA pela
// frente. Se ela tinha travado a conta, a trava sai junto; abaixo de zero o
// contador não vai.
func TestRefundAttemptGivesBackOneReservation(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	for range auth.LockThreshold {
		reservar(t, store, userID, clk.Now())
	}
	if err := store.RefundAttempt(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if tentativas, ate := lerTravas(t, pool, userID); tentativas != auth.LockThreshold-1 || ate != nil {
		t.Errorf("depois de devolver a quinta: %d falhas, locked_until = %v; queria %d e sem trava",
			tentativas, ate, auth.LockThreshold-1)
	}

	if err := store.ResetFailures(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := store.RefundAttempt(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if tentativas, _ := lerTravas(t, pool, userID); tentativas != 0 {
		t.Errorf("devolver com o contador em zero deixou %d", tentativas)
	}
}

func TestResetFailuresClearsTheLock(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	for range auth.LockThreshold {
		reservar(t, store, userID, clk.Now())
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

// O rehash do login é um compare-and-swap (Task 11 do Plano 5): com o hash
// antigo certo, grava; com outro (um set-password comitou depois da
// verificação), não grava nada.
func TestRehashPasswordIsACompareAndSwap(t *testing.T) {
	_, pool, _, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	novo, err := auth.HashPassword(context.Background(), "outra-senha-bem-comprida")
	if err != nil {
		t.Fatal(err)
	}
	trocou, err := store.RehashPassword(ctx, userID, "hash-que-ja-nao-e-o-gravado", novo)
	if err != nil || trocou {
		t.Fatalf("hash antigo errado: trocou = %v, erro = %v; queria false", trocou, err)
	}
	u, err := store.ByEmail(ctx, "pessoa@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != "x" {
		t.Fatalf("o rehash com o hash antigo errado gravou %q", u.PasswordHash)
	}

	trocou, err = store.RehashPassword(ctx, userID, "x", novo)
	if err != nil || !trocou {
		t.Fatalf("hash antigo certo: trocou = %v, erro = %v; queria true", trocou, err)
	}
	u, err = store.ByEmail(ctx, "pessoa@exemplo.com")
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
	agora := clk.Now()

	for range auth.LockThreshold {
		reservar(t, store, userID, agora)
	}
	if _, ate := lerTravas(t, pool, userID); ate == nil {
		t.Fatal("a conta não travou no limiar; o resto do teste seria vácuo")
	}

	// Depois do vencimento, uma única falha não pode re-travar.
	depois := agora.Add(auth.LockDuration).Add(time.Minute)
	if !reservar(t, store, userID, depois) {
		t.Fatal("o bloqueio vencido recusou a reserva")
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
	agora := clk.Now()

	for i := range 3 {
		reservar(t, store, userID, agora.Add(time.Duration(i)*time.Second))
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

// CheckPassword é a verificação do login (A2 do Plano 10): reserva a
// tentativa no contador recebido ANTES do Argon2id. Os três caminhos: senha
// certa deixa a reserva (quem chama devolve ou zera); senha errada também;
// contador bloqueado recusa até a senha certa, sem reservar nada, pagando o
// mesmo Argon2id (o dummy).
func TestCheckPassword(t *testing.T) {
	_, pool, clk, userID := setup(t)
	store := auth.NewPGStore(pool)
	ctx := context.Background()

	hash, err := auth.HashPassword(context.Background(), senha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
		t.Fatal(err)
	}
	u, err := store.ByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	conta := auth.AccountAttempts(store, userID, clk.Now())

	ok, _, err := auth.CheckPassword(ctx, conta, u, senha)
	if err != nil || !ok {
		t.Fatalf("senha certa: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 1 {
		t.Errorf("senha certa: %d reservas, queria 1 (quem chama devolve ou zera)", n)
	}

	ok, _, err = auth.CheckPassword(ctx, conta, u, "senha-errada-com-mais-de-15")
	if err != nil || ok {
		t.Fatalf("senha errada: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 2 {
		t.Errorf("senha errada não contou: %d reservas, queria 2", n)
	}

	if _, err := pool.Exec(ctx, `UPDATE users SET locked_until = $2 WHERE id = $1`, userID, clk.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	antes := auth.Argon2Verifications()
	ok, _, err = auth.CheckPassword(ctx, conta, u, senha)
	if err != nil || ok {
		t.Fatalf("conta bloqueada aceitou a senha certa: ok = %v, erro = %v", ok, err)
	}
	if n, _ := lerTravas(t, pool, userID); n != 2 {
		t.Errorf("conta bloqueada contou reserva: %d, queria 2", n)
	}
	if n := auth.Argon2Verifications() - antes; n != 1 {
		t.Errorf("conta bloqueada: %d Argon2id, queria 1 (o dummy): o bloqueio se anunciaria pelo tempo", n)
	}
}
