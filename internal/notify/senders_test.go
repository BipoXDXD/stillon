package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// pedido é o que o servidor de teste viu. A rede não conta como
// sincronização para o -race, então o handler entrega o pedido
// por canal, e não numa variável que o teste lê.
type pedido struct {
	Metodo, Caminho, Query string
	Cabecalho              http.Header
	Corpo                  []byte
}

// provedor sobe um httptest que responde com responder e entrega cada
// pedido em pedidos. Devolve o cliente do safedial liberado só para ele.
func provedor(t *testing.T, responder func(w http.ResponseWriter)) (*httptest.Server, *http.Client, <-chan pedido) {
	t.Helper()
	pedidos := make(chan pedido, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(r.Body)
		pedidos <- pedido{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), corpo}
		responder(w)
	}))
	t.Cleanup(srv.Close)
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return srv, safedial.NewClientAllowing(0, ap), pedidos
}

func receber(t *testing.T, c <-chan pedido) pedido {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("o provedor não recebeu o pedido")
		return pedido{}
	}
}

func status(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code) }
}

var evAberto = Event{
	NotificationID: uuid.MustParse("01920000-0000-7000-8000-000000000001"),
	Kind:           incident.EventOpened,
	TargetName:     "API de pagamentos",
	StartedAt:      time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
	Cause:          "timeout",
}

func comoSendError(t *testing.T, err error) *SendError {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("erro = %v, queria *SendError", err)
	}
	return se
}

// Os limites {1,20} do id e {1,100} do token são a única defesa de formato
// abaixo do maxDiscordURL: as URLs daqui ficam bem abaixo do teto.
func TestValidateDiscordURLIdLengthBoundary(t *testing.T) {
	const base = "https://discord.com/api/webhooks/"
	if err := ValidateDiscordURL(base + strings.Repeat("1", 20) + "/t"); err != nil {
		t.Errorf("id de 20 dígitos: %v, queria aceito", err)
	}
	if err := ValidateDiscordURL(base + strings.Repeat("1", 21) + "/t"); !errors.Is(err, ErrDiscordURL) {
		t.Errorf("id de 21 dígitos: %v, queria ErrDiscordURL", err)
	}
}

func TestValidateDiscordURLTokenLengthBoundary(t *testing.T) {
	const base = "https://discord.com/api/webhooks/1/"
	aceito := base + strings.Repeat("a", 100)
	recusado := base + strings.Repeat("a", 101)
	if len(recusado) > maxDiscordURL {
		t.Fatalf("URL de %d caracteres passa do maxDiscordURL; o teste não provaria a regex", len(recusado))
	}
	if err := ValidateDiscordURL(aceito); err != nil {
		t.Errorf("token de 100 caracteres: %v, queria aceito", err)
	}
	if err := ValidateDiscordURL(recusado); !errors.Is(err, ErrDiscordURL) {
		t.Errorf("token de 101 caracteres: %v, queria ErrDiscordURL", err)
	}
}

func TestValidateDiscordURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://discord.com/api/webhooks/123456789012345678/abcDEF_ghi-JKL", true},
		{"https://discordapp.com/api/webhooks/1/t", true},
		{"https://canary.discord.com/api/v10/webhooks/1/t", true},
		{"https://Discord.com/api/webhooks/1/t", true},
		{"http://discord.com/api/webhooks/1/t", false},              // sem TLS
		{"https://discord.com.evil.io/api/webhooks/1/t", false},     // sufixo
		{"https://evil.io/api/webhooks/1/t", false},                 // outro host
		{"https://discord.com:8443/api/webhooks/1/t", false},        // porta
		{"https://user:pw@discord.com/api/webhooks/1/t", false},     // credencial
		{"https://discord.com/api/webhooks/1/t?thread_id=2", false}, // query
		{"https://discord.com/api/webhooks/1/t#x", false},           // fragmento
		{"https://discord.com/api/webhooks/abc/t", false},           // id não numérico
		{"https://discord.com/api/webhooks/1/t/slack", false},       // outro endpoint
		{"https://discord.com/api/webhooks/1/../../users/@me", false},
		{"https://discord.com/api/webhooks/1/" + strings.Repeat("a", 600), false},
		{"https://127.0.0.1/api/webhooks/1/t", false},
		{"", false},
	}
	for _, c := range cases {
		if err := ValidateDiscordURL(c.url); (err == nil) != c.ok {
			t.Errorf("ValidateDiscordURL(%q) = %v, queria ok=%v", c.url, err, c.ok)
		}
	}
}

type transporteFunc func(*http.Request) (*http.Response, error)

func (f transporteFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// O redirecionamento para o servidor local acontece no transporte do teste.
// O sender valida e monta a requisição com o mesmo código de produção.
func discordDoProvedor(t *testing.T, endereco string, client *http.Client) *Discord {
	t.Helper()
	destino, err := url.Parse(endereco)
	if err != nil {
		t.Fatal(err)
	}
	transporte := client.Transport
	copia := *client
	copia.Transport = transporteFunc(func(r *http.Request) (*http.Response, error) {
		pedido := r.Clone(r.Context())
		pedido.URL.Scheme = destino.Scheme
		pedido.URL.Host = destino.Host
		pedido.Host = destino.Host
		return transporte.RoundTrip(pedido)
	})
	return &Discord{client: &copia}
}

func TestDiscordSendsToTheCanonicalWebhookOrigin(t *testing.T) {
	for _, host := range []string{"discord.com", "discordapp.com", "canary.discord.com", "ptb.discord.com"} {
		t.Run(host, func(t *testing.T) {
			const caminho = "/api/v10/webhooks/1/Teste_-"
			var enviado string
			d := NewDiscord()
			d.client.Transport = transporteFunc(func(r *http.Request) (*http.Response, error) {
				enviado = r.URL.String()
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			})
			dest := crypto.Secret("https://" + strings.ToUpper(host) + caminho)
			if err := d.SendTest(context.Background(), dest); err != nil {
				t.Fatal(err)
			}
			if esperado := "https://" + host + caminho + "?wait=true"; enviado != esperado {
				t.Errorf("URL enviada = %q, queria %q", enviado, esperado)
			}
		})
	}
}

func TestDiscordSendsWithoutMentionsOrEmbeds(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := discordDoProvedor(t, srv.URL, client)

	if err := d.Send(context.Background(), crypto.Secret(webhookDeTeste), evAberto); err != nil {
		t.Fatal(err)
	}
	p := receber(t, pedidos)
	if p.Metodo != http.MethodPost || p.Caminho != "/api/webhooks/1/tok" || p.Query != "wait=true" {
		t.Errorf("pedido = %s %s?%s", p.Metodo, p.Caminho, p.Query)
	}
	if p.Cabecalho.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", p.Cabecalho.Get("Content-Type"))
	}
	// O JSON cru, e não o decodificado: "parse": null e a ausência da chave
	// decodificam para o mesmo nil, e os dois ligam as menções no Discord.
	if !strings.Contains(string(p.Corpo), `"allowed_mentions":{"parse":[]}`) {
		t.Errorf("allowed_mentions não desliga as menções: %s", p.Corpo)
	}
	var corpo discordPayload
	if err := json.Unmarshal(p.Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.Flags != suppressEmbeds || !strings.Contains(corpo.Content, "API de pagamentos") || !strings.Contains(corpo.Content, "tempo esgotado") {
		t.Errorf("payload = %+v", corpo)
	}
}

func TestDiscordClassifiesResponses(t *testing.T) {
	cases := []struct {
		nome      string
		responder func(http.ResponseWriter)
		reason    Reason
		permanent bool
		retry     time.Duration
	}{
		{"204 é entregue", status(http.StatusNoContent), "", false, 0},
		{"429 pede espera", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "2.5")
			w.WriteHeader(http.StatusTooManyRequests)
		}, ReasonRateLimited, false, 2500 * time.Millisecond},
		{"500 é transitório", status(http.StatusInternalServerError), ReasonServerError, false, 0},
		{"404 é webhook apagado", status(http.StatusNotFound), ReasonRejected, true, 0},
		{"401 é token inválido", status(http.StatusUnauthorized), ReasonRejected, true, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			srv, client, _ := provedor(t, c.responder)
			d := discordDoProvedor(t, srv.URL, client)
			err := d.Send(context.Background(), crypto.Secret(webhookDeTeste), evAberto)
			if c.reason == "" {
				if err != nil {
					t.Fatalf("erro = %v, queria entregue", err)
				}
				return
			}
			se := comoSendError(t, err)
			if se.Reason != c.reason || se.Permanent != c.permanent || se.RetryAfter != c.retry {
				t.Errorf("SendError = %+v, queria %s permanente=%v retry=%v", se, c.reason, c.permanent, c.retry)
			}
		})
	}
}

// A revalidação no envio é a guarda contra uma URL que entrou no banco por
// fora da API. O cliente deste teste ALCANÇA o servidor: se a validação
// sumisse, o pedido chegaria, e o contador mostraria.
func TestDiscordRevalidatesTheURLBeforeSending(t *testing.T) {
	var chegou atomic.Int32
	srv, client, _ := provedor(t, func(w http.ResponseWriter) { chegou.Add(1); w.WriteHeader(http.StatusOK) })
	d := &Discord{client: client}

	err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
	se := comoSendError(t, err)
	if se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("SendError = %+v, queria invalid_channel permanente", se)
	}
	if chegou.Load() != 0 {
		t.Error("o pedido chegou ao servidor: a URL não foi revalidada antes do envio")
	}
}

// Reclassificar o redirecionamento não muda o despacho: um webhook que
// redireciona sem fim continua falha definitiva de canal inválido, como quando
// o erro era ErrBlocked. Sem isto, virava falha de rede e seria tentado de
// novo.
func TestDiscordRedirectLoopIsAPermanentFailure(t *testing.T) {
	srv, client, _ := provedor(t, func(w http.ResponseWriter) {
		w.Header().Set("Location", "/api/webhooks/1/t")
		w.WriteHeader(http.StatusFound)
	})
	d := discordDoProvedor(t, srv.URL, client)
	err := d.Send(context.Background(), crypto.Secret("https://discord.com/api/webhooks/1/t"), evAberto)
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("redirecionamento sem fim deu %+v, queria invalid_channel permanente", se)
	}
}

// O SendError não carrega o erro de origem, que traz a URL com o token.
func TestSendErrorNeverCarriesTheWebhookToken(t *testing.T) {
	d := discordDoProvedor(t, "http://127.0.0.1:1", safedial.NewClient(0))
	err := d.Send(context.Background(), crypto.Secret("https://discord.com/api/webhooks/1/token-secreto"), evAberto)
	if err == nil || strings.Contains(err.Error(), "token-secreto") {
		t.Errorf("erro = %v; não pode ser nil nem conter o token", err)
	}
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("destino bloqueado pelo safedial deu %+v, queria invalid_channel permanente", se)
	}
}

func TestResendSendsTheAlertWithAnIdempotencyKey(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL + "/emails", apiKey: crypto.Secret("re_chave"), from: "StillOn <alertas@stillon.example>"}

	for range 2 {
		if err := r.Send(context.Background(), crypto.Secret("pessoa@exemplo.com"), evAberto); err != nil {
			t.Fatal(err)
		}
	}
	p1, p2 := receber(t, pedidos), receber(t, pedidos)

	if p1.Cabecalho.Get("Authorization") != "Bearer re_chave" {
		t.Errorf("Authorization = %q", p1.Cabecalho.Get("Authorization"))
	}
	if k := p1.Cabecalho.Get("Idempotency-Key"); k != "notification/"+evAberto.NotificationID.String() {
		t.Errorf("Idempotency-Key = %q", k)
	}
	// A mesma chave com outro corpo é recusada pelo Resend: a retentativa
	// tem de montar o mesmo corpo, byte a byte.
	if string(p1.Corpo) != string(p2.Corpo) {
		t.Errorf("duas montagens do mesmo evento diferem:\n%s\n%s", p1.Corpo, p2.Corpo)
	}
	var corpo resendPayload
	if err := json.Unmarshal(p1.Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.From != r.from || len(corpo.To) != 1 || corpo.To[0] != "pessoa@exemplo.com" ||
		!strings.Contains(corpo.Subject, "API de pagamentos") || !strings.Contains(corpo.Text, "24/09/2026 12:00 UTC") {
		t.Errorf("payload = %+v", corpo)
	}
}

func TestResendVerificationCarriesTheLinkAndNoIdempotencyKey(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}

	if err := r.SendVerification(context.Background(), uuid.New(), crypto.Secret("p@x.com"), "https://v.dev/verify/abc"); err != nil {
		t.Fatal(err)
	}
	p := receber(t, pedidos)
	if p.Cabecalho.Get("Idempotency-Key") != "" {
		t.Error("verificação levou Idempotency-Key; cada envio tem token novo")
	}
	if !strings.Contains(string(p.Corpo), "https://v.dev/verify/abc") {
		t.Errorf("corpo sem o link: %s", p.Corpo)
	}
}

// O aviso de bloqueio do segundo fator leva a chave de idempotência do
// bloqueio e nenhum detalhe sensível; o permanente diz como liberar.
func TestResendTOTPLockedCarriesTheKeyAndNoSensitiveDetail(t *testing.T) {
	casos := []struct {
		nome       string
		permanente bool
		comoSair   string
	}{
		{"temporário", false, "dura o dobro"},
		{"permanente", true, "stillon user reset-2fa"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			srv, client, pedidos := provedor(t, status(http.StatusOK))
			r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}

			if _, err := r.SendTOTPLocked(context.Background(), "dono@x.com", c.permanente, "totp-lock/u/1"); err != nil {
				t.Fatal(err)
			}
			p := receber(t, pedidos)
			if k := p.Cabecalho.Get("Idempotency-Key"); k != "totp-lock/u/1" {
				t.Errorf("Idempotency-Key = %q", k)
			}
			var corpo resendPayload
			if err := json.Unmarshal(p.Corpo, &corpo); err != nil {
				t.Fatal(err)
			}
			if len(corpo.To) != 1 || corpo.To[0] != "dono@x.com" || !strings.Contains(corpo.Subject, "Segundo fator bloqueado") {
				t.Errorf("payload = %+v", corpo)
			}
			if !strings.Contains(corpo.Text, c.comoSair) {
				t.Errorf("texto sem %q: %s", c.comoSair, corpo.Text)
			}
			if c.permanente == strings.Contains(corpo.Text, "dura o dobro") {
				t.Errorf("texto do caso errado: %s", corpo.Text)
			}
		})
	}
}

func TestResendTOTPLockedPreservesTheRegisteredAddress(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}
	const endereco = "titular@intranet"
	if invalido, err := r.SendTOTPLocked(context.Background(), endereco, false, "totp-lock/u/1"); invalido || err != nil {
		t.Fatalf("destino cadastrado: inválido=%t, erro=%v", invalido, err)
	}
	var corpo resendPayload
	if err := json.Unmarshal(receber(t, pedidos).Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if len(corpo.To) != 1 || corpo.To[0] != endereco {
		t.Fatalf("destino enviado = %v, queria %s", corpo.To, endereco)
	}
}

func TestResendTOTPLockedDistinguishesDestinationFromProviderConfiguration(t *testing.T) {
	for _, codigo := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(codigo), func(t *testing.T) {
			var restaurado atomic.Bool
			srv, client, pedidos := provedor(t, func(w http.ResponseWriter) {
				if restaurado.Load() {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(codigo)
			})
			r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}
			ctx := context.Background()
			if invalido, err := r.SendTOTPLocked(ctx, "sem endereço", false, "totp-lock/u/1"); !invalido || err == nil {
				t.Fatalf("destino inválido: invalido=%t, erro=%v", invalido, err)
			}
			if invalido, err := r.SendTOTPLocked(ctx, "dono@x.com", true, "totp-lock/u/2"); invalido || err == nil {
				t.Fatalf("recusa do provedor: invalido=%t, erro=%v", invalido, err)
			}
			antes := receber(t, pedidos)
			restaurado.Store(true)
			if invalido, err := r.SendTOTPLocked(ctx, "dono@x.com", true, "totp-lock/u/2"); invalido || err != nil {
				t.Fatalf("provedor restaurado: invalido=%t, erro=%v", invalido, err)
			}
			depois := receber(t, pedidos)
			if antes.Cabecalho.Get("Idempotency-Key") != depois.Cabecalho.Get("Idempotency-Key") || string(antes.Corpo) != string(depois.Corpo) {
				t.Fatal("a retentativa alterou a chave ou o conteúdo do aviso")
			}
		})
	}
}

func TestResendClassifiesResponses(t *testing.T) {
	conflito := func(nome string) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"statusCode":409,"name":"`+nome+`","message":"x"}`)
		}
	}
	cases := []struct {
		nome      string
		responder func(http.ResponseWriter)
		reason    Reason
		permanent bool
	}{
		{"mesma chave em processamento", conflito("concurrent_idempotent_requests"), ReasonRateLimited, false},
		{"mesma chave com outro corpo", conflito("invalid_idempotent_request"), ReasonRejected, true},
		// Repetir com a mesma chave é seguro: se o 409 era o de outro corpo,
		// ele volta igual até o teto de tentativas.
		{"409 com corpo ilegível", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `<html>conflito</html>`)
		}, ReasonServerError, false},
		{"409 com nome parcialmente decodificado", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"name":"invalid_idempotent_request","name":1}`)
		}, ReasonServerError, false},
		{"409 com nome desconhecido", conflito("outro_conflito"), ReasonServerError, false},
		{"422 é pedido inválido", status(http.StatusUnprocessableEntity), ReasonRejected, true},
		{"403 é domínio não verificado", status(http.StatusForbidden), ReasonRejected, true},
		{"503 é transitório", status(http.StatusServiceUnavailable), ReasonServerError, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			srv, client, _ := provedor(t, c.responder)
			r := &Resend{client: client, endpoint: srv.URL, apiKey: crypto.Secret("k"), from: "a@b.dev"}
			se := comoSendError(t, r.Send(context.Background(), crypto.Secret("p@x.com"), evAberto))
			if se.Reason != c.reason || se.Permanent != c.permanent {
				t.Errorf("SendError = %+v, queria %s permanente=%v", se, c.reason, c.permanent)
			}
		})
	}
}

func TestSendTimesOutWithTheContext(t *testing.T) {
	liberar := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(liberar) }) })
	srv, client, _ := provedor(t, func(w http.ResponseWriter) { <-liberar })
	d := discordDoProvedor(t, srv.URL, client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	se := comoSendError(t, d.Send(ctx, crypto.Secret(webhookDeTeste), evAberto))
	once.Do(func() { close(liberar) })
	if se.Reason != ReasonTimeout || se.Permanent {
		t.Errorf("SendError = %+v, queria timeout transitório", se)
	}
}

func TestRetryAfter(t *testing.T) {
	for v, quer := range map[string]time.Duration{
		"3":                             3 * time.Second,
		" 3 ":                           3 * time.Second,
		"0.25":                          250 * time.Millisecond,
		"":                              0,
		"-1":                            0,
		"NaN":                           0,
		"Inf":                           0,
		"Wed, 21 Oct 2026 07:28:00 GMT": 0,
		"999999":                        maxRetryAfter,
	} {
		if got := retryAfter(v); got != quer {
			t.Errorf("retryAfter(%q) = %v, queria %v", v, got, quer)
		}
	}
}

// O lembrete chega no Discord com a cor da abertura: o alvo segue fora.
func TestDiscordReminderHasTheOpenedColor(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := discordDoProvedor(t, srv.URL, client)
	ev := evAberto
	ev.Kind = incident.EventReminder
	ev.QueuedAt = ev.StartedAt.Add(time.Hour)

	if err := d.Send(context.Background(), crypto.Secret(webhookDeTeste), ev); err != nil {
		t.Fatal(err)
	}
	var corpo discordPayload
	if err := json.Unmarshal(receber(t, pedidos).Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(corpo.Content, "🔴 API de pagamentos continua fora do ar") {
		t.Errorf("conteúdo = %q", corpo.Content)
	}
}

// Só o fechamento sai em verde.
func TestDiscordResolvedHasTheResolvedColor(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := discordDoProvedor(t, srv.URL, client)
	ev := evAberto
	ev.Kind = incident.EventResolved
	ev.ResolvedAt = ev.StartedAt.Add(time.Hour)

	if err := d.Send(context.Background(), crypto.Secret(webhookDeTeste), ev); err != nil {
		t.Fatal(err)
	}
	var corpo discordPayload
	if err := json.Unmarshal(receber(t, pedidos).Corpo, &corpo); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(corpo.Content, "🟢 ") {
		t.Errorf("conteúdo = %q", corpo.Content)
	}
}

func TestResponseClassificationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		status     int
		motivo     Reason
		permanente bool
	}{
		{199, ReasonRejected, true},
		{200, "", false},
		{299, "", false},
		{300, ReasonRejected, true},
		{429, ReasonRateLimited, false},
		{499, ReasonRejected, true},
		{500, ReasonServerError, false},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			err := classificarResposta(&http.Response{StatusCode: tc.status, Header: http.Header{}})
			if tc.motivo == "" {
				if err != nil {
					t.Fatalf("erro = %v, queria entregue", err)
				}
				return
			}
			var se *SendError
			if !errors.As(err, &se) || se.Reason != tc.motivo || se.Permanent != tc.permanente {
				t.Errorf("erro = %v, queria %s (permanente: %v)", err, tc.motivo, tc.permanente)
			}
		})
	}
}

func TestErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		nome       string
		err        error
		motivo     Reason
		permanente bool
	}{
		{"destino bloqueado", fmt.Errorf("x: %w", safedial.ErrBlocked), ReasonInvalidChannel, true},
		{"URL inválida", fmt.Errorf("x: %w", safedial.ErrInvalidURL), ReasonInvalidChannel, true},
		{"redirecionamento recusado", fmt.Errorf("x: %w", safedial.ErrRedirect), ReasonInvalidChannel, true},
		{"prazo", fmt.Errorf("x: %w", context.DeadlineExceeded), ReasonTimeout, false},
		{"rede", errors.New("reset"), ReasonNetwork, false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			var se *SendError
			if err := classificarErro(tc.err); !errors.As(err, &se) || se.Reason != tc.motivo || se.Permanent != tc.permanente {
				t.Errorf("erro = %v, queria %s (permanente: %v)", err, tc.motivo, tc.permanente)
			}
		})
	}
}
