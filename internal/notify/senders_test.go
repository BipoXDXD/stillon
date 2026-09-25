package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
// sincronização para o -race (Plano 2), então o handler entrega o pedido
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

func aceitarQualquer(string) error { return nil }

func comoSendError(t *testing.T, err error) *SendError {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("erro = %v, queria *SendError", err)
	}
	return se
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

func TestDiscordSendsWithoutMentionsOrEmbeds(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	d := &Discord{client: client, validate: aceitarQualquer}

	if err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/tok"), evAberto); err != nil {
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
			d := &Discord{client: client, validate: aceitarQualquer}
			err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
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
	d := &Discord{client: client, validate: ValidateDiscordURL}

	err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
	se := comoSendError(t, err)
	if se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("SendError = %+v, queria invalid_channel permanente", se)
	}
	if chegou.Load() != 0 {
		t.Error("o pedido chegou ao servidor: a URL não foi revalidada antes do envio")
	}
}

// C3 (Plano 10) não muda o despacho: um webhook que redireciona sem fim
// continua falha definitiva de canal inválido, como quando o erro era
// ErrBlocked. Sem isto, virava falha de rede e seria tentado de novo.
func TestDiscordRedirectLoopIsAPermanentFailure(t *testing.T) {
	srv, client, _ := provedor(t, func(w http.ResponseWriter) {
		w.Header().Set("Location", "/api/webhooks/1/t")
		w.WriteHeader(http.StatusFound)
	})
	d := &Discord{client: client, validate: aceitarQualquer}
	err := d.Send(context.Background(), crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto)
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("redirecionamento sem fim deu %+v, queria invalid_channel permanente", se)
	}
}

// O SendError não carrega o erro de origem, que traz a URL com o token.
func TestSendErrorNeverCarriesTheWebhookToken(t *testing.T) {
	d := &Discord{client: safedial.NewClient(0), validate: aceitarQualquer}
	err := d.Send(context.Background(), crypto.Secret("http://127.0.0.1:1/api/webhooks/1/token-secreto"), evAberto)
	if err == nil || strings.Contains(err.Error(), "token-secreto") {
		t.Errorf("erro = %v; não pode ser nil nem conter o token", err)
	}
	if se := comoSendError(t, err); se.Reason != ReasonInvalidChannel || !se.Permanent {
		t.Errorf("destino bloqueado pelo safedial deu %+v, queria invalid_channel permanente", se)
	}
}

func TestResendSendsTheAlertWithAnIdempotencyKey(t *testing.T) {
	srv, client, pedidos := provedor(t, status(http.StatusOK))
	r := &Resend{client: client, endpoint: srv.URL + "/emails", apiKey: crypto.Secret("re_chave"), from: "Vigilume <alertas@vigilume.dev>"}

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

	if err := r.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/verify/abc"); err != nil {
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
	d := &Discord{client: client, validate: aceitarQualquer}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	se := comoSendError(t, d.Send(ctx, crypto.Secret(srv.URL+"/api/webhooks/1/t"), evAberto))
	once.Do(func() { close(liberar) })
	if se.Reason != ReasonTimeout || se.Permanent {
		t.Errorf("SendError = %+v, queria timeout transitório", se)
	}
}

func TestRetryAfter(t *testing.T) {
	for v, quer := range map[string]time.Duration{
		"3":                             3 * time.Second,
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
