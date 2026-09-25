package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/account"
)

// I1 da revisão do Plano 10: o cookie de dispositivo morre com a remediação.
// Quem já teve acesso completo à conta guardou cookies de 180 dias, e cada um
// tirava o login do bloqueio da conta. O dono reage trocando a senha, pelo
// set-password ou pelo reset-2fa (os dois encerram todas as sessões), e os
// cookies continuavam valendo. Agora o MAC cobre um carimbo das credenciais
// (o hash da senha e a data de ativação do 2FA), e cada um desses eventos o
// muda.
//
// Cada caso: o dono entra e guarda o cookie; a perna positiva prova que ele
// vale (conta trancada, o dono entra); o evento acontece; com a conta trancada
// de novo, o mesmo cookie não tira mais o login do bloqueio, nem com a senha
// certa.
func TestDeviceCookieDiesWithTheCredentials(t *testing.T) {
	casos := []struct {
		nome string
		// com2FA: o cookie é cunhado num login completo com o 2FA ligado (o
		// caso do reset-2fa, que só muda algo com o 2FA ligado).
		com2FA bool
		// evento acontece com o cookie guardado e devolve a senha que vale
		// depois dele.
		evento func(t *testing.T, a *api) string
	}{
		{"troca de senha pela API", false, func(t *testing.T, a *api) string {
			resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/password", accessTokenValido(t, a),
				map[string]string{"current_password": testPass, "new_password": senhaNova})
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("troca de senha: %d", resp.StatusCode)
			}
			return senhaNova
		}},
		{"set-password", false, func(t *testing.T, a *api) string {
			if _, err := account.NewService(a.pool, nil, a.clk, nil).ResetPassword(context.Background(), testEmail, senhaNova); err != nil {
				t.Fatal(err)
			}
			return senhaNova
		}},
		{"reset-2fa", true, func(t *testing.T, a *api) string {
			resetarPeloCLI(t, a)
			return testPass
		}},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := apiAtrasDeProxy(t)
			dono := clientWithJar(t)
			var segredo string
			if caso.com2FA {
				segredo = cadastrarTOTP(t, a, clientWithJar(t))
			}
			entrarCompleto := func() int {
				t.Helper()
				a.clk.Advance(time.Second)
				if caso.com2FA {
					return entrarComFator(t, a, dono, segredo)
				}
				st, _ := entrarDe(t, a, dono, testPass)
				return st
			}

			if st := entrarCompleto(); st != http.StatusOK {
				t.Fatalf("primeiro login do dono: %d", st)
			}
			bloquearConta(t, a)
			if st := entrarCompleto(); st != http.StatusOK {
				t.Fatalf("antes do evento, o dono com o cookie e a conta trancada: %d, queria 200", st)
			}

			senha := caso.evento(t, a)

			bloquearConta(t, a)
			if st, codigo := entrarDe(t, a, dono, senha); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
				t.Errorf("depois do evento, o cookie antigo e a conta trancada: %d %s, queria 401 invalid_credentials", st, codigo)
			}
		})
	}
}
