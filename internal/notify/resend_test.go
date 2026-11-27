package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// O e-mail de conta sai pelo remetente da instância (a de conta, montada na
// fiação) e leva a chave da API e uma Idempotency-Key nova a cada envio.
func TestSendAccountUsaORemetenteDaInstancia(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	const chaveDeTeste = "chave-resend-sintetica"
	r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret(chaveDeTeste), from: "StillOn <conta@conta.stillon.example>"}

	for range 2 {
		if err := r.SendAccount(context.Background(), crypto.Secret("p@x.com"), "Confirme seu e-mail", "Corpo da conta\n"); err != nil {
			t.Fatal(err)
		}
	}
	p1, p2 := receber(t, pedidos), receber(t, pedidos)

	var corpo resendPayload
	if err := json.Unmarshal(p1.Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.From != "StillOn <conta@conta.stillon.example>" || len(corpo.To) != 1 || corpo.To[0] != "p@x.com" ||
		corpo.Subject != "Confirme seu e-mail" || corpo.Text != "Corpo da conta\n" {
		t.Errorf("payload = %+v", corpo)
	}
	for i, p := range []pedido{p1, p2} {
		if got := p.Cabecalho.Get("Authorization"); got != "Bearer "+chaveDeTeste {
			t.Errorf("pedido %d: Authorization não recebeu a chave sintética como Bearer", i+1)
		}
	}
	k1, k2 := p1.Cabecalho.Get("Idempotency-Key"), p2.Cabecalho.Get("Idempotency-Key")
	if k1 == "" || k2 == "" || k1 == k2 {
		t.Errorf("Idempotency-Key = %q e %q, queria duas chaves não vazias e diferentes", k1, k2)
	}
}
