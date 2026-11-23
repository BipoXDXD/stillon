// Package turnstile confere no siteverify da Cloudflare o token do desafio
// que o painel manda no cadastro e no "esqueci a senha". O servidor não
// guarda token: cada um vale uma verificação, e a Cloudflare recusa o
// repetido.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

const (
	endpointSiteverify = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	// tamanhoMaximoDoToken é o teto documentado do token do widget.
	tamanhoMaximoDoToken = 2048
	prazoMaximo          = 5 * time.Second
	limiteDoCorpo        = 64 << 10
)

// ErrRecusado é a resposta da Cloudflare (ou da conferência local) de que o
// desafio não vale: token inválido, vencido, já usado, de outro host ou de
// outra ação. Qualquer outro erro de Verificar é falha de infraestrutura, e
// quem chama falha fechado (503), sem confundir as duas coisas.
var ErrRecusado = errors.New("turnstile: desafio recusado")

// Verificador fala com o siteverify. O secret só sai no corpo do POST:
// nunca em log nem em erro.
type Verificador struct {
	secret       crypto.Secret
	hostEsperado string
	endpoint     string
	client       *http.Client
}

// New usa o siteverify da Cloudflare pelo cliente do safedial, como o envio
// de e-mail: o host é fixo e confiável, e o processo não ganha um cliente sem
// guarda. hostEsperado vazio desliga a conferência do hostname (dev).
func New(secret crypto.Secret, hostEsperado string) *Verificador {
	return NewComEndpoint(secret, hostEsperado, endpointSiteverify, safedial.NewClient(0))
}

// NewComEndpoint troca o endpoint e o cliente, para os testes. O cliente é
// copiado e deixa de seguir redirect: um 307 reenviaria o secret a outro
// endereço.
func NewComEndpoint(secret crypto.Secret, hostEsperado, endpoint string, client *http.Client) *Verificador {
	semRedirect := *client
	semRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Verificador{secret: secret, hostEsperado: hostEsperado, endpoint: endpoint, client: &semRedirect}
}

type respostaSiteverify struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
}

// Verificar confere o token. ip é o do cliente (vazio, não vai); acao é a
// esperada no widget ("signup", "forgot"). Resposta sem ação passa, porque
// as chaves de teste não mandam ação.
func (v *Verificador) Verificar(ctx context.Context, token, ip, acao string) error {
	if token == "" || len(token) > tamanhoMaximoDoToken {
		return ErrRecusado
	}

	ctx, cancel := context.WithTimeout(ctx, prazoMaximo)
	defer cancel()

	form := url.Values{
		"secret":          {string(v.secret.Reveal())},
		"response":        {token},
		"idempotency_key": {uuid.NewString()},
	}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("turnstile: montar o pedido: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: siteverify fora do ar: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, limiteDoCorpo))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: siteverify respondeu %d: %w", resp.StatusCode, errStatus)
	}

	var r respostaSiteverify
	if err := json.NewDecoder(io.LimitReader(resp.Body, limiteDoCorpo)).Decode(&r); err != nil {
		return fmt.Errorf("turnstile: resposta ilegível do siteverify: %w", err)
	}

	switch {
	case !r.Success:
		slog.InfoContext(ctx, "turnstile: desafio recusado", "error_codes", r.ErrorCodes)
		return ErrRecusado
	case v.hostEsperado != "" && !strings.EqualFold(r.Hostname, v.hostEsperado):
		slog.InfoContext(ctx, "turnstile: desafio de outro host", "hostname", r.Hostname)
		return ErrRecusado
	case r.Action != "" && r.Action != acao:
		slog.InfoContext(ctx, "turnstile: desafio de outra ação", "action", r.Action, "esperada", acao)
		return ErrRecusado
	}
	return nil
}

var errStatus = errors.New("status inesperado")
