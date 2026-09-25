package auth_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
)

// As provas do 2FA depois do Plano 10: amarradas ao segredo que as validou
// (A4), conferidas na ordem global de travas (A5), a troca identificada pelo
// rotation (A7) e o código de recuperação normalizado (A8).

// ligarComCodigos liga o 2FA pelo caminho do painel (Enroll e Confirm) e
// devolve o segredo e os dez códigos de recuperação.
func ligarComCodigos(t *testing.T, svc *auth.TOTP, userID uuid.UUID, em time.Time) (string, []string) {
	t.Helper()
	uri, err := svc.Enroll(context.Background(), userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	segredo := secretFromURI(t, uri)
	codigos, err := svc.Confirm(context.Background(), userID, codigoEm(t, segredo, em), em)
	if err != nil {
		t.Fatal(err)
	}
	return segredo, codigos
}

func segredoCifrado(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []byte {
	t.Helper()
	var sealed []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, userID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func quantosCodigos(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A4: o consumo do timestep é um compare-and-swap também no segredo. Uma
// troca de autenticador que comite entre a leitura do segredo e o consumo faz
// o código do app antigo ser recusado; com o segredo que o validou, passa.
func TestTimestepConsumptionIsBoundToTheSecret(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	lido := segredoCifrado(t, pool, userID)
	passo := inicioDaTroca.Unix() / 30

	gravarSegredoTOTP(t, pool, userID, "KRSXG5CTMVRXEZLUKRSXG5CTMVRXEZLU") // a troca comitou
	if err := svc.ConsumirTimestep(ctx, userID, passo, lido); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("consumir com o segredo de antes da troca: %v, queria ErrTOTPInvalid", err)
	}
	if err := svc.ConsumirTimestep(ctx, userID, passo, segredoCifrado(t, pool, userID)); err != nil {
		t.Errorf("consumir com o segredo vigente: %v", err)
	}
}

// A4 (auditoria S4): o disable CONSOME o timestep. O código que abriu o login
// não desliga o 2FA na mesma janela; o do período seguinte desliga.
func TestDisableConsumesTheTimestep(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	codigo := codigoEm(t, segredoDeTroca, inicioDaTroca)

	if err := svc.Verify(ctx, userID, codigo, inicioDaTroca); err != nil {
		t.Fatalf("o login com o código: %v", err)
	}
	if err := svc.Disable(ctx, userID, codigo, inicioDaTroca); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("disable com o código já usado no login: %v, queria ErrTOTPInvalid", err)
	}
	if ligado, _ := svc.IsEnabled(ctx, userID); !ligado {
		t.Fatal("o código já usado desligou o 2FA")
	}
	depois := inicioDaTroca.Add(30 * time.Second)
	if err := svc.Disable(ctx, userID, codigoEm(t, segredoDeTroca, depois), depois); err != nil {
		t.Errorf("disable com o código do período seguinte: %v", err)
	}
}

// A5: o disable toma a trava de sessões do usuário ANTES de tocar em qualquer
// linha. Com a trava presa, ele espera, e enquanto espera o código de
// recuperação que ele vai gastar não está travado — antes, ele apagava o
// código primeiro e só então travava users, a ordem inversa da do
// rotate/confirm e do reset-2fa, e os dois fechavam um ciclo (deadlock).
func TestDisableTakesTheUserLockFirst(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	_, codigos := ligarComCodigos(t, svc, userID, inicioDaTroca)

	soltar := prenderTravaDoUsuario(t, pool, userID)
	fim := make(chan error, 1)
	go func() { fim <- svc.Disable(ctx, userID, codigos[0], inicioDaTroca) }()

	esperou := esperarTravaAdvisory(t, pool)
	livre := codigoLivre(t, pool, userID)
	soltar()
	err := <-fim
	if !esperou {
		t.Fatalf("o disable não esperou a trava do usuário (terminou com %v)", err)
	}
	if !livre {
		t.Error("o disable travou os códigos de recuperação antes da trava do usuário")
	}
	if err != nil {
		t.Errorf("o disable depois de soltar a trava: %v", err)
	}
}

// A5: o rotate confere a família e o 2FA ligado ANTES de gastar a prova, sob
// a trava. Com a família caída, nem o código de recuperação nem o timestep do
// código do app são gastos.
func TestStartRotationSpendsNothingForADeadFamily(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	segredo, codigos := ligarComCodigos(t, svc, userID, inicioDaTroca)
	familia := familiaViva(t, pool, userID, inicioDaTroca)
	revogarFamilia(t, pool, familia, inicioDaTroca)

	depois := inicioDaTroca.Add(30 * time.Second)
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigos[0], depois); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("rotate com código de recuperação e família caída: %v, queria ErrInvalidSession", err)
	}
	if n := quantosCodigos(t, pool, userID); n != 10 {
		t.Errorf("%d códigos de recuperação depois do rotate recusado, queria 10: o código foi gasto", n)
	}
	codigo := codigoEm(t, segredo, depois)
	if _, _, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigo, depois); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("rotate com código do app e família caída: %v, queria ErrInvalidSession", err)
	}
	if err := svc.Verify(ctx, userID, codigo, depois); err != nil {
		t.Errorf("o código do app usado no rotate recusado não entra mais: %v (o timestep foi gasto)", err)
	}
}

// A7: o rotate devolve o identificador da troca (32 caracteres hex, diferente
// a cada rotate), e o confirm só conclui a troca que o cliente conhece.
// Ausente ou de uma troca já substituída é ErrTOTPNoRotation, antes do código.
func TestConfirmRotationNeedsTheRotation(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
	familia := familiaViva(t, pool, userID, inicioDaTroca)

	_, rot1, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(rot1) {
		t.Fatalf("rotation = %q, queria 32 caracteres hex", rot1)
	}
	em := inicioDaTroca.Add(30 * time.Second)
	uri2, rot2, err := svc.StartRotation(ctx, userID, familia, "totp@exemplo.com", codigoEm(t, segredoDeTroca, em), em)
	if err != nil {
		t.Fatal(err)
	}
	if rot2 == rot1 {
		t.Fatal("dois rotates devolveram o mesmo rotation")
	}
	segundo := secretFromURI(t, uri2)

	em = em.Add(30 * time.Second)
	for nome, rot := range map[string]string{"ausente": "", "da troca substituída": rot1, "inventado": strings.Repeat("ab", 16)} {
		if _, err := svc.ConfirmRotation(ctx, userID, familia, rot, codigoEm(t, segundo, em), em); !errors.Is(err, auth.ErrTOTPNoRotation) {
			t.Errorf("confirm com rotation %s: %v, queria ErrTOTPNoRotation", nome, err)
		}
	}
	if _, err := svc.ConfirmRotation(ctx, userID, familia, rot2, codigoEm(t, segundo, em), em); err != nil {
		t.Errorf("confirm com o rotation da troca vigente: %v", err)
	}
}

// A8: o código de recuperação é gerado em base32 maiúsculo, sem separador, e
// é aceito como o dono o digitar: em minúsculas, com hífens e espaços. Um uso
// só, em qualquer grafia.
func TestRecoveryCodesAreNormalized(t *testing.T) {
	svc, _, userID := setupTOTP(t)
	ctx := context.Background()
	_, codigos := ligarComCodigos(t, svc, userID, inicioDaTroca)
	for _, c := range codigos {
		if !regexp.MustCompile(`^[A-Z2-7]{16}$`).MatchString(c) {
			t.Fatalf("código gerado %q fora do formato base32 maiúsculo de 16 caracteres", c)
		}
	}

	digitado := " " + strings.ToLower(codigos[0][:4]) + "-" + codigos[0][4:8] + " " + strings.ToLower(codigos[0][8:]) + "\t"
	if err := svc.Verify(ctx, userID, digitado, inicioDaTroca); err != nil {
		t.Fatalf("código %q (o %q digitado à mão): %v", digitado, codigos[0], err)
	}
	if err := svc.Verify(ctx, userID, codigos[0], inicioDaTroca); !errors.Is(err, auth.ErrTOTPInvalid) {
		t.Errorf("o código já gasto em outra grafia passou de novo: %v", err)
	}
}

// A4: a sessão depois do 2FA só nasce se o segredo que aceitou o código ainda
// é o ativo, conferido sob a trava do usuário. Uma troca de autenticador ou um
// disable no meio fazem a emissão recusar com ErrStaleSecondFactor.
func TestIssueAfterSecondFactorRechecksTheSecret(t *testing.T) {
	ctx := context.Background()
	casos := map[string]func(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID){
		"nada mudou": func(*testing.T, *pgxpool.Pool, uuid.UUID) {},
		"troca de autenticador": func(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
			gravarSegredoTOTP(t, pool, userID, "KRSXG5CTMVRXEZLUKRSXG5CTMVRXEZLU")
		},
		"disable": func(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
			if _, err := pool.Exec(ctx, `UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL WHERE id = $1`, userID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for nome, mudar := range casos {
		t.Run(nome, func(t *testing.T) {
			svc, pool, userID := setupTOTP(t)
			gravarSegredoTOTP(t, pool, userID, segredoDeTroca)
			mgr := auth.NewManager(auth.NewPGStore(pool), clock.NewFake(inicioDaTroca), ipKey)
			prova, err := svc.VerifyWithProof(ctx, userID, codigoEm(t, segredoDeTroca, inicioDaTroca), inicioDaTroca)
			if err != nil {
				t.Fatal(err)
			}
			mudar(t, pool, userID)
			_, _, err = mgr.IssueAfterSecondFactor(ctx, userID, auth.PasswordStamp("x"), prova, meta)
			if nome == "nada mudou" {
				if err != nil {
					t.Errorf("sem mudança no meio: %v", err)
				}
				return
			}
			if !errors.Is(err, auth.ErrStaleSecondFactor) {
				t.Errorf("com %s no meio: %v, queria ErrStaleSecondFactor", nome, err)
			}
		})
	}
}

// prenderTravaDoUsuario toma, numa conexão do teste, a trava de sessões do
// usuário (a mesma advisory lock das mutações de sessão). Soltar é
// idempotente, e o Cleanup solta se o teste morrer antes.
func prenderTravaDoUsuario(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) func() {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chave := "hashtextextended('stillon.sessions:' || '" + userID.String() + "', 0)"
	var umaVez sync.Once
	soltar := func() {
		umaVez.Do(func() {
			if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chave+`)`); err != nil {
				t.Error(err)
			}
			conn.Release()
		})
	}
	t.Cleanup(soltar)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}
	return soltar
}

// esperarTravaAdvisory espera alguém parar numa advisory lock deste banco.
func esperarTravaAdvisory(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	for prazo := time.Now().Add(5 * time.Second); time.Now().Before(prazo); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return true
		}
	}
	return false
}

// codigoLivre diz se nenhum código de recuperação do usuário está travado por
// outra transação agora.
func codigoLivre(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SELECT id FROM totp_recovery_codes WHERE user_id = $1 FOR UPDATE NOWAIT`, userID)
	return err == nil
}
