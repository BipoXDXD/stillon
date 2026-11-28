package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// naReplica é a mesma api de a, mas falando com outra instância sobre o mesmo
// banco (novaReplica): os helpers de a (lerCru) passam a pedir a ela.
func naReplica(t *testing.T, a *api) *api {
	t.Helper()
	b := *a
	b.srv = novaReplica(t, a)
	return &b
}

type rotaPorUsuario struct {
	nome    string
	caminho string
	corpo   func(i int) any
	rajada  int
	// antes prepara a conta (ex.: tira a confirmação do e-mail).
	antes func(t *testing.T, a *api)
}

var rotasComBaldeNoBanco = []rotaPorUsuario{
	{
		nome: "reenvio da confirmação", caminho: "/api/v1/auth/email/resend", rajada: 3,
		corpo: func(int) any { return nil },
		antes: func(t *testing.T, a *api) {
			t.Helper()
			if _, err := a.pool.Exec(t.Context(), `UPDATE users SET email_verified_at = NULL WHERE id = $1`, a.userID); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		nome: "criação de projeto", caminho: "/api/v1/projects", rajada: 5,
		corpo: func(i int) any { return map[string]string{"name": fmt.Sprintf("Projeto %d", i)} },
	},
	{
		nome: "envio de verificação de canal", caminho: "/api/v1/channels", rajada: 5,
		corpo: func(int) any { return map[string]string{"kind": "discord", "url": webhookDeTeste} },
	},
}

// O balde por usuário é um só, seja qual for a réplica que atende: os pedidos
// alternam entre duas instâncias sobre o mesmo banco, e o primeiro além da
// rajada somada recebe 429. Com o balde na memória de cada processo, a outra
// réplica daria a rajada inteira de novo. Dentro da rajada a resposta pode ser
// qualquer coisa menos 429 (a criação de projeto esbarra na cota do plano, que
// também gasta a ficha).
func TestResendBucketIsShared(t *testing.T) {
	for _, r := range rotasComBaldeNoBanco {
		t.Run(r.nome, func(t *testing.T) {
			a := newAPI(t)
			if r.antes != nil {
				r.antes(t, a)
			}
			token := tokenDe(t, a, a.userID)
			replicas := []*api{naReplica(t, a), naReplica(t, a)}

			for i := range r.rajada {
				status, corpo := lerCru(t, replicas[i%2], http.MethodPost, r.caminho, token, r.corpo(i))
				if status == http.StatusTooManyRequests {
					t.Fatalf("pedido %d da rajada na réplica %d: 429 %s", i+1, i%2, corpo)
				}
			}
			status, corpo := lerCru(t, replicas[r.rajada%2], http.MethodPost, r.caminho, token, r.corpo(r.rajada))
			exigirErro(t, "além da rajada somada", respostaCrua{status: status, corpo: corpo},
				http.StatusTooManyRequests, apierr.CodeRateLimited)
		})
	}
}
