package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/auth"
)

func estadoDoSegundoFatorBloqueado(t *testing.T, a *api) (falhas int, ate, avisoEm *time.Time) {
	t.Helper()
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_failures, totp_locked_until, totp_lock_alert_at FROM users WHERE id = $1`, a.userID).
		Scan(&falhas, &ate, &avisoEm); err != nil {
		t.Fatal(err)
	}
	return falhas, ate, avisoEm
}

// Os códigos errados contam no contador próprio do 2FA, mesmo num navegador
// com o cookie de dispositivo (que só protege o login por senha), e o quinto
// trava o segundo fator por 15 min e deixa o aviso ao dono na fila.
func TestWrongCodesLockTheSecondFactorAndQueueTheAlert(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)

	desafio := desafioAtual(t, a, c)
	for i := range auth.TOTPLockThreshold {
		resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": "000000"})
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusUnauthorized {
			t.Fatalf("tentativa %d devolveu %d, queria 401", i+1, status)
		}
	}

	falhas, ate, avisoEm := estadoDoSegundoFatorBloqueado(t, a)
	agora := a.clk.Now()
	if falhas != auth.TOTPLockThreshold || ate == nil || !ate.Equal(agora.Add(auth.TOTPLockBase)) {
		t.Fatalf("falhas=%d até=%v, queria %d e %v", falhas, ate, auth.TOTPLockThreshold, agora.Add(auth.TOTPLockBase))
	}
	if avisoEm == nil || !avisoEm.Equal(agora) {
		t.Fatalf("aviso ao dono = %v, queria pendente desde %v", avisoEm, agora)
	}
}

// Sem a senha não se chega ao contador do 2FA: senha errada no login e desafio
// forjado no /totp/verify não movem nada.
func TestAnonymousWithoutThePasswordNeverTouchesTheTOTPCounter(t *testing.T) {
	a := newAPI(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	c := clientWithJar(t)

	for range 3 {
		login(t, a, c, testEmail, "senha-errada-123").Body.Close()
	}
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": "desafio.forjado.aqui", "code": "000000"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("desafio forjado devolveu %d, queria 401", resp.StatusCode)
	}

	if falhas, ate, avisoEm := estadoDoSegundoFatorBloqueado(t, a); falhas != 0 || ate != nil || avisoEm != nil {
		t.Fatalf("contador do 2FA mexido sem senha: falhas=%d até=%v aviso=%v", falhas, ate, avisoEm)
	}
}

func TestSecondFactorTurnedOffBeforeVerificationDoesNotCount(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET totp_confirmed_at = NULL, totp_secret_enc = NULL WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	falhasAntes, _ := estadoDoBloqueio(t, a)
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": "000000"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, queria 401", resp.StatusCode)
	}
	if falhas, _, _ := estadoDoSegundoFatorBloqueado(t, a); falhas != 0 {
		t.Errorf("tentativa sem fator ativo contou no 2FA: %d", falhas)
	}
	if falhas, _ := estadoDoBloqueio(t, a); falhas != falhasAntes {
		t.Errorf("tentativa sem fator ativo contou na conta: %d → %d", falhasAntes, falhas)
	}
}

// O segundo fator travado recusa até o código certo, com o mesmo 401 genérico,
// e a recusa não conta no bloqueio da conta.
func TestLockedSecondFactorRefusesTheRightCode(t *testing.T) {
	a := newAPI(t)
	// c não tem o cookie de dispositivo, para que uma recusa contada caísse no
	// bloqueio da conta e aparecesse abaixo.
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	c := clientWithJar(t)
	// O passo de tempo do cadastro já foi gasto: sem avançar, o código certo
	// seria recusado como repetido, e o teste passaria sem testar o bloqueio.
	a.clk.Advance(30 * time.Second)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET totp_failures = 5, totp_locked_until = $2 WHERE id = $1`,
		a.userID, a.clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	falhasAntes, _ := estadoDoBloqueio(t, a)

	codigo, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	desafio := desafioAtual(t, a, c)
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": codigo})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("código certo com o 2FA travado devolveu %d, queria 401", resp.StatusCode)
	}
	if falhas, _ := estadoDoBloqueio(t, a); falhas != falhasAntes {
		t.Fatalf("a recusa do 2FA travado contou na conta: %d → %d", falhasAntes, falhas)
	}
	if falhas, _, _ := estadoDoSegundoFatorBloqueado(t, a); falhas != 5 {
		t.Fatalf("a recusa do 2FA travado contou no 2FA: falhas=%d, queria 5", falhas)
	}
}

// Um código certo zera o contador do 2FA: as falhas de antes não se somam às
// próximas, e o próximo bloqueio volta a ser o primeiro.
func TestRightCodeResetsTheTOTPCounter(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	c := clientWithJar(t)
	a.clk.Advance(30 * time.Second)

	desafio := desafioAtual(t, a, c)
	for range auth.TOTPLockThreshold - 1 {
		post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": "000000"}).Body.Close()
	}
	if falhas, _, _ := estadoDoSegundoFatorBloqueado(t, a); falhas != auth.TOTPLockThreshold-1 {
		t.Fatalf("falhas antes do acerto = %d, queria %d", falhas, auth.TOTPLockThreshold-1)
	}

	codigo, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": codigo})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("código certo devolveu %d, queria 200", resp.StatusCode)
	}
	if falhas, ate, avisoEm := estadoDoSegundoFatorBloqueado(t, a); falhas != 0 || ate != nil || avisoEm != nil {
		t.Fatalf("depois do acerto: falhas=%d até=%v aviso=%v, queria zerado e sem aviso", falhas, ate, avisoEm)
	}
}
