package httpapi_test

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// respostaDeLogin é o que o cliente vê de um login: o status e o corpo sem o
// trace_id, que muda a cada pedido.
type respostaDeLogin struct {
	status int
	corpo  map[string]any
}

func loginVisto(t *testing.T, a *api, senha string) respostaDeLogin {
	t.Helper()
	resp := login(t, a, clientWithJar(t), testEmail, senha)
	defer resp.Body.Close()
	bruto, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var corpo map[string]any
	if err := json.Unmarshal(bruto, &corpo); err != nil {
		t.Fatalf("corpo do login não é JSON (%d): %s", resp.StatusCode, bruto)
	}
	delete(corpo, "trace_id")
	if ck := refreshCookie(resp); ck != nil && ck.Value != "" {
		t.Error("o login emitiu cookie de refresh")
	}
	return respostaDeLogin{status: resp.StatusCode, corpo: corpo}
}

// O login de quem está suspenso, com a senha CERTA, é indistinguível do login
// com a senha errada: o mesmo status, o mesmo código e o mesmo corpo. Com o 2FA
// ligado, nenhum desafio sai (o desafio contaria que a senha estava certa). O
// contador de falhas não zera: a tentativa conta como a da senha errada.
func TestSuspendedLoginIsIndistinguishable(t *testing.T) {
	for _, caso := range []struct {
		nome   string
		com2FA bool
	}{
		{"sem 2FA", false},
		{"com 2FA", true},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			if caso.com2FA {
				cadastrarTOTP(t, a, clientWithJar(t))
			}

			errada := loginVisto(t, a, "uma-senha-que-nao-e-a-dele")
			falhasAntes, _ := estadoDoBloqueio(t, a)
			if falhasAntes == 0 {
				t.Fatal("a senha errada não contou falha: o cenário não prova o contador")
			}

			testdb.Suspender(t, a.userID)
			suspenso := loginVisto(t, a, testPass)

			if suspenso.status != http.StatusUnauthorized || suspenso.status != errada.status {
				t.Errorf("suspenso com a senha certa: status %d; senha errada: %d (queria 401 nos dois)",
					suspenso.status, errada.status)
			}
			if !maps.EqualFunc(suspenso.corpo, errada.corpo, func(x, y any) bool {
				bx, _ := json.Marshal(x)
				by, _ := json.Marshal(y)
				return string(bx) == string(by)
			}) {
				t.Errorf("corpos diferentes:\n suspenso: %v\n errada:   %v", suspenso.corpo, errada.corpo)
			}
			if _, tem := suspenso.corpo["challenge"]; tem {
				t.Error("o login de suspenso entregou desafio de 2FA")
			}
			if falhas, _ := estadoDoBloqueio(t, a); falhas != falhasAntes+1 {
				t.Errorf("failed_attempts = %d depois do login de suspenso, queria %d (contado como a senha errada)",
					falhas, falhasAntes+1)
			}
		})
	}
}

// Um refresh emitido antes da suspensão não renova, mesmo que as famílias não
// tenham sido revogadas (a suspensão aqui é gravada por fora, sem o Suspend).
// A perna positiva é o mesmo refresh renovando antes de suspender.
func TestSuspendedRefreshRefused(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	entrada := login(t, a, c, testEmail, testPass)
	entrada.Body.Close()
	if entrada.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", entrada.StatusCode)
	}
	antes := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	antes.Body.Close()
	if antes.StatusCode != http.StatusOK {
		t.Fatalf("refresh antes da suspensão devolveu %d, queria 200", antes.StatusCode)
	}

	testdb.Suspender(t, a.userID)

	resp := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh de suspenso devolveu %d, queria 401", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "invalid_session" {
		t.Errorf("code = %q, queria invalid_session", code)
	}
}

// Um desafio de 2FA emitido antes da suspensão não abre sessão, nem com o
// código certo: o mesmo 401 invalid_credentials do código errado.
func TestSuspendedTOTPVerifyRefused(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	c := clientWithJar(t)
	desafio := desafioAtual(t, a, c)

	testdb.Suspender(t, a.userID)
	a.clk.Advance(30 * time.Second) // o timestep da confirmação já foi gasto

	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": codigoDe(t, a, segredo)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("verify de suspenso devolveu %d, queria 401", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", code)
	}
	if ck := refreshCookie(resp); ck != nil && ck.Value != "" {
		t.Error("o verify de suspenso emitiu cookie de refresh")
	}
}
