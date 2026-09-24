package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/account"
)

// As rotas do segundo fator exigem a família do token viva, como /sessions e
// /auth/password (D11): um access token de sessão encerrada não cadastra,
// não confirma nem desliga o 2FA. Morta é 401 unauthorized (D24), antes de
// olhar senha ou código.
func TestTOTPRoutesRejectADeadFamily(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()

	for rota, corpo := range map[string]map[string]string{
		"enroll":  {"password": testPass},
		"confirm": {"code": "000000"},
		"disable": {"password": testPass, "code": "000000"},
	} {
		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/"+rota, token, corpo)
		codigo := codigoDoErro(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || codigo != "unauthorized" {
			t.Errorf("%s com a família morta: %d %s; queria 401 unauthorized", rota, resp.StatusCode, codigo)
		}
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas contadas: a família morta tem de ser recusada antes da senha", n)
	}
}

// O invasor com um access token cadastra o SEU autenticador (enroll pede a
// senha, que ele tem). O dono troca a senha, ou o operador roda set-password,
// e o invasor tenta confirmar com o token antigo e um código válido do app
// dele: se passasse, ficaria dono do 2FA e dos códigos de recuperação.
//
// Guarda dupla (D23) no "o 2FA não ficou ligado": a família viva exigida no
// /totp/confirm (o 401 unauthorized é só dela) e o segredo pendente apagado
// na mesma transação da troca e do set-password (que também tem prova
// própria: TestPasswordChangeDropsAPendingTOTPEnrollment, em
// internal/account). Sabotar só o segredo pendente deixa este teste verde; a
// sabotagem registrada (Task 11) tira as duas, e o 2FA do invasor liga.
func TestIntruderCannotConfirmTOTPAfterThePasswordChanges(t *testing.T) {
	for _, r := range []string{"troca de senha", "set-password"} {
		t.Run(r, func(t *testing.T) {
			a := newAPI(t)
			_, doInvasor := entrar(t, a)
			er := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", doInvasor,
				map[string]string{"password": testPass})
			var enroll struct {
				URI string `json:"otpauth_uri"`
			}
			if err := json.NewDecoder(er.Body).Decode(&enroll); err != nil {
				t.Fatal(err)
			}
			er.Body.Close()
			key, err := otp.NewKeyFromURL(enroll.URI)
			if err != nil {
				t.Fatalf("enroll não devolveu URI (%d): %v", er.StatusCode, err)
			}

			switch r {
			case "troca de senha":
				_, doDono := entrar(t, a)
				resp := trocarSenha(t, a, doDono, map[string]string{"current_password": testPass, "new_password": senhaNova})
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("a troca devolveu %d", resp.StatusCode)
				}
			case "set-password":
				if _, err := account.NewService(a.pool, nil, a.clk, nil).ResetPassword(context.Background(), testEmail, senhaNova); err != nil {
					t.Fatal(err)
				}
			}

			code, err := totp.GenerateCode(key.Secret(), a.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", doInvasor,
				map[string]string{"code": code})
			codigo := codigoDoErro(t, resp)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized || codigo != "unauthorized" {
				t.Errorf("confirm com o token do invasor: %d %s; queria 401 unauthorized", resp.StatusCode, codigo)
			}
			if confirmado, _, _, _ := estadoDoSegundoFator(t, a); confirmado {
				t.Error("o invasor ligou o 2FA com o autenticador dele depois da troca de senha")
			}
		})
	}
}
