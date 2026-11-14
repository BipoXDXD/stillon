package notify

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// corpoContado é um corpo de resposta enorme que conta o que foi lido dele.
type corpoContado struct {
	resto   int
	lidos   int
	fechado bool
}

func (c *corpoContado) Read(p []byte) (int, error) {
	if c.resto == 0 {
		return 0, io.EOF
	}
	n := min(len(p), c.resto)
	clear(p[:n])
	c.resto -= n
	c.lidos += n
	return n, nil
}

func (c *corpoContado) Close() error { c.fechado = true; return nil }

// respostaFixa devolve, sem rede, a mesma resposta a qualquer pedido.
type respostaFixa struct {
	status int
	corpo  *corpoContado
}

func (r respostaFixa) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: r.status, Header: http.Header{}, Body: r.corpo, Request: req}, nil
}

// Um provedor que responde com um corpo sem fim não segura o envio: os
// senders leem no máximo 64 KiB do corpo que não usam e fecham a resposta.
func TestSendersReadAtMostABoundedBodyBeforeClosing(t *testing.T) {
	const enorme, teto = 1 << 20, 64 << 10
	casos := []struct {
		nome   string
		enviar func(*http.Client) error
	}{
		{"discord", func(c *http.Client) error {
			d := &Discord{client: c, validate: func(string) error { return nil }}
			return d.Send(context.Background(), crypto.Secret(webhookDeTeste), evAberto)
		}},
		{"resend", func(c *http.Client) error {
			r := &Resend{client: c, endpoint: "https://api.resend.example/emails", apiKey: crypto.Secret("re_chave"), from: "StillOn <alertas@stillon.example>"}
			return r.Send(context.Background(), crypto.Secret("pessoa@exemplo.com"), evAberto)
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			corpo := &corpoContado{resto: enorme}
			client := &http.Client{Transport: respostaFixa{status: http.StatusOK, corpo: corpo}}
			if err := c.enviar(client); err != nil {
				t.Fatalf("Send = %v", err)
			}
			if corpo.lidos > teto {
				t.Errorf("leu %d bytes do corpo; o teto é %d", corpo.lidos, teto)
			}
			if !corpo.fechado {
				t.Error("a resposta não foi fechada")
			}
		})
	}
}
