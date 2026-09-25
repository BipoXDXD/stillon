package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
)

// A troca de senha e o set-password matam uma troca de autenticador em
// andamento (decisão do controlador no Plano 9): o pendente não tem prazo, e
// quem o iniciou (com a senha e o fator) pode ser o invasor contra quem o dono
// está trocando a senha; uma sessão roubada depois o confirmaria e ficaria com
// o 2FA. O pendente cai na mesma transação que troca o hash. A perna positiva
// ("nenhuma") é o mesmo cenário sem a troca de senha: o confirm passa.
func TestPasswordChangeKillsAPendingRotation(t *testing.T) {
	for _, caso := range []struct {
		nome   string
		status int
	}{
		{"nenhuma", http.StatusOK},
		{"troca de senha", http.StatusForbidden},
		{"set-password", http.StatusForbidden},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			velho := cadastrarTOTP(t, a, clientWithJar(t))
			token := accessTokenValido(t, a)
			st, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
			if st != http.StatusOK {
				t.Fatalf("rotate devolveu %d", st)
			}

			a.clk.Advance(30 * time.Second)
			switch caso.nome {
			case "troca de senha":
				resp := trocarSenha(t, a, token, map[string]string{
					"current_password": testPass, "new_password": senhaNova, "totp_code": codigoDe(t, a, velho)})
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("a troca de senha devolveu %d", resp.StatusCode)
				}
			case "set-password":
				if _, err := account.NewService(a.pool, nil, a.clk, nil).ResetPassword(context.Background(), testEmail, senhaNova); err != nil {
					t.Fatal(err)
				}
				// O set-password encerrou todas as sessões; o confirm sai de
				// outra família e seria 403 mesmo sem a limpeza (o pendente é
				// da família que o criou). Quem prova a limpeza aqui é o
				// temPendente, que confere o segredo E a família.
				token = accessTokenValido(t, a)
			}
			if caso.status == http.StatusForbidden && temPendente(t, a) {
				t.Errorf("%s deixou o segredo pendente", caso.nome)
			}

			a.clk.Advance(30 * time.Second)
			st, codigo, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo))
			if st != caso.status {
				t.Errorf("confirm depois de %q: %d %s, queria %d", caso.nome, st, codigo, caso.status)
			}
		})
	}
}
