package notify

import (
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// O valor de um panic vem de qualquer código no caminho do envio e pode
// carregar a URL do webhook (com o token) ou a chave do Resend. Ao log vai só
// o tipo, mais a mensagem quando quem fala é o runtime.
func TestPanicValueStaysOutOfTheLog(t *testing.T) {
	const canario = "https://discord.com/api/webhooks/1/CANARY-5c1f0e2a"
	casos := []struct {
		nome   string
		valor  any
		querNo string
	}{
		{"string", "webhook ruim: " + canario, "valor string omitido"},
		{"erro com a URL", &url.Error{Op: "Post", URL: canario, Err: errors.New("quebrou")}, "valor *url.Error omitido"},
		{"erro do runtime", erroDeRuntime(), "index out of range"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			log := &logSeguro{}
			antigo := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
			t.Cleanup(func() { slog.SetDefault(antigo) })

			b := novaBancada(t, nil)
			b.canal.panico = c.valor
			b.enfileirar(t, incident.EventOpened)
			b.despachar(t, 1)

			if strings.Contains(log.String(), "CANARY-5c1f0e2a") {
				t.Errorf("o valor do panic foi para o log:\n%s", log)
			}
			if !strings.Contains(log.String(), c.querNo) {
				t.Errorf("o log não diz %q:\n%s", c.querNo, log)
			}
		})
	}
}

// erroDeRuntime devolve o valor que o runtime passa a um panic de índice.
func erroDeRuntime() (r any) {
	defer func() { r = recover() }()
	var vazio []int
	_ = vazio[len(vazio)]
	return nil
}
