package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

type discordFalso struct {
	mu  sync.Mutex
	err error
}

func (f *discordFalso) SendTest(context.Context, crypto.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

type carteiroFalso struct {
	mu    sync.Mutex
	links []string
}

func (f *carteiroFalso) SendVerification(_ context.Context, _ crypto.Secret, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, link)
	return nil
}

// token tira o token do último link enviado.
func (f *carteiroFalso) token(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.links) == 0 {
		t.Fatal("nenhum e-mail de verificação enviado")
	}
	_, token, ok := strings.Cut(f.links[len(f.links)-1], "/verify/")
	if !ok {
		t.Fatalf("link fora do formato: %s", f.links[len(f.links)-1])
	}
	return token
}

const webhookDeTeste = "https://discord.com/api/webhooks/123456789/token-do-webhook"

type canalJSON struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Hint   string `json:"hint"`
	Status string `json:"status"`
}

func canaisDe(t *testing.T, a *api, token string) []canalJSON {
	t.Helper()
	var l struct {
		Items []canalJSON `json:"items"`
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/channels", token, nil)
	lerCorpo(t, resp, &l)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /channels deu %d", resp.StatusCode)
	}
	return l.Items
}

func TestDiscordChannelFlow(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	var criado canalJSON
	cru := lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated || criado.Status != "verified" || criado.Hint != "webhook …6789" {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	if strings.Contains(string(cru), "token-do-webhook") {
		t.Fatal("a resposta contém o token do webhook")
	}
	if cs := canaisDe(t, a, tok); len(cs) != 1 || cs[0].ID != criado.ID {
		t.Fatalf("lista = %+v", cs)
	}

	resp = chamar(t, a, http.MethodDelete, "/api/v1/channels/"+criado.ID, tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE deu %d", resp.StatusCode)
	}
	if cs := canaisDe(t, a, tok); len(cs) != 0 {
		t.Errorf("depois do DELETE, %d canais", len(cs))
	}
}

// Pendência do Plano 3: o canal que falhou de vez aparece como failing, com
// o motivo e a hora; antes da falha, os dois campos são null.
func TestFailingChannelShowsTheLastError(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	var criado canalJSON
	cru := lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste}), &criado)
	if !strings.Contains(string(cru), `"last_error":null`) || !strings.Contains(string(cru), `"last_failure_at":null`) {
		t.Errorf("canal novo = %s; queria last_error e last_failure_at null", cru)
	}

	em := a.clk.Now().UTC()
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		motivo := "rejected"
		return q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: uuid.MustParse(criado.ID), Now: em, LastError: &motivo})
	})
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Items []struct {
			Status        string     `json:"status"`
			LastError     *string    `json:"last_error"`
			LastFailureAt *time.Time `json:"last_failure_at"`
		} `json:"items"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/channels", tok, nil), &l)
	if len(l.Items) != 1 || l.Items[0].Status != "failing" || l.Items[0].LastError == nil || *l.Items[0].LastError != "rejected" ||
		l.Items[0].LastFailureAt == nil || !l.Items[0].LastFailureAt.Equal(em) {
		t.Errorf("GET /channels = %+v; queria failing, rejected, %v", l.Items, em)
	}
}

// O caminho do navegador: o GET mostra o botão e NÃO verifica; o POST do
// formulário, sem X-Requested-With, verifica.
func TestEmailVerificationThroughTheBrowser(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	var criado canalJSON
	lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated || criado.Status != "pending" {
		t.Fatalf("POST deu %d: %+v", resp.StatusCode, criado)
	}
	link := a.srv.URL + "/verify/" + a.email.token(t)

	get, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if get.StatusCode != http.StatusOK || !strings.Contains(string(html), `<form method="post">`) {
		t.Fatalf("GET deu %d: %s", get.StatusCode, html)
	}
	for cabecalho, quer := range map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Content-Security-Policy": "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	} {
		if got := get.Header.Get(cabecalho); got != quer {
			t.Errorf("%s = %q, queria %q", cabecalho, got, quer)
		}
	}
	if cs := canaisDe(t, a, tok); cs[0].Status != "pending" {
		t.Fatal("o GET verificou o canal: um leitor de links confirmaria endereços de terceiros")
	}

	post, err := http.Post(link, "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST do formulário deu %d", post.StatusCode)
	}
	if cs := canaisDe(t, a, tok); cs[0].Status != "verified" {
		t.Errorf("depois do POST, status = %s", cs[0].Status)
	}
}

// corpoAcimaDoTetoDeVerify é um byte a mais que o teto de 1 KiB do formulário.
const corpoAcimaDoTetoDeVerify = 1<<10 + 1

// criarCanalPendente cadastra um e-mail e devolve a sessão do dono e o token
// recebido no link de verificação.
func criarCanalPendente(t *testing.T, a *api) (sessao, tokenVerificacao string) {
	t.Helper()
	sessao = tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", sessao, map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	var criado canalJSON
	lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated || criado.Status != "pending" {
		t.Fatalf("POST deu %d: %+v", resp.StatusCode, criado)
	}
	return sessao, a.email.token(t)
}

func confirmarComCorpo(a *api, tokenVerificacao string, corpo io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/verify/"+tokenVerificacao, corpo)
	rec := httptest.NewRecorder()
	a.srv.Config.Handler.ServeHTTP(rec, req)
	return rec
}

func exigirStatusDoCanal(t *testing.T, a *api, sessao, quer string) {
	t.Helper()
	if cs := canaisDe(t, a, sessao); len(cs) != 1 || cs[0].Status != quer {
		t.Errorf("canais = %+v, queria um só com status %s", cs, quer)
	}
}

// O corpo é recusado antes do banco: o token continua valendo para o envio
// legítimo que vier depois.
func TestVerifyBodyOverCeilingKeepsTokenUsable(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	sessao, token := criarCanalPendente(t, a)

	recusado := confirmarComCorpo(a, token, strings.NewReader(strings.Repeat("x", corpoAcimaDoTetoDeVerify)))
	if recusado.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo acima do teto deu %d, queria 413", recusado.Code)
	}
	exigirStatusDoCanal(t, a, sessao, "pending")

	aceito := confirmarComCorpo(a, token, nil)
	if aceito.Code != http.StatusOK {
		t.Errorf("POST sem corpo excedente deu %d, queria 200", aceito.Code)
	}
	exigirStatusDoCanal(t, a, sessao, "verified")
}

// Corpo que falha no meio da leitura (conexão cortada, chunked truncado) não
// é corpo grande: responde 400 e não confirma.
func TestVerifyBodyReadFailureIs400AndConfirmsNothing(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	sessao, token := criarCanalPendente(t, a)

	rec := confirmarComCorpo(a, token, iotest.ErrReader(io.ErrUnexpectedEOF))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("falha de leitura deu %d, queria 400", rec.Code)
	}
	exigirStatusDoCanal(t, a, sessao, "pending")
}

func TestUnknownVerifyTokenIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	for _, token := range []string{"nao-existe", strings.Repeat("A", 43)} {
		get, err := http.Get(a.srv.URL + "/verify/" + token)
		if err != nil {
			t.Fatal(err)
		}
		get.Body.Close()
		post, err := http.Post(a.srv.URL+"/verify/"+token, "application/x-www-form-urlencoded", nil)
		if err != nil {
			t.Fatal(err)
		}
		post.Body.Close()
		if get.StatusCode != http.StatusNotFound || post.StatusCode != http.StatusNotFound {
			t.Errorf("token %q: GET %d, POST %d; queria 404 nos dois", token, get.StatusCode, post.StatusCode)
		}
	}
}

func TestChannelErrorsMapToCodes(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)

	var env envelopeComCampos
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "slack"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "kind" {
		t.Errorf("kind inválido deu %d/%+v", resp.StatusCode, env.Error)
	}

	a.discord.mu.Lock()
	a.discord.err = &notify.SendError{Reason: notify.ReasonRejected, Permanent: true}
	a.discord.mu.Unlock()
	resp = chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusBadGateway || e.Error.Code != "delivery_failed" {
		t.Errorf("envio falho deu %d/%s, queria 502/delivery_failed", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()

	a.discord.mu.Lock()
	a.discord.err = nil
	a.discord.mu.Unlock()
	var criado canalJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste}), &criado)
	resp = chamar(t, a, http.MethodPost, "/api/v1/channels/"+criado.ID+"/verify/resend", tok, nil)
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusConflict || e.Error.Code != "channel_not_pending" {
		t.Errorf("reenviar Discord deu %d/%s, queria 409/channel_not_pending", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()
}

func TestChannelLimitIs409(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	ctx := context.Background()
	agora := a.clk.Now()
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		for range notify.MaxChannels {
			if err := q.InsertChannel(ctx, db.InsertChannelParams{
				ID: uuid.Must(uuid.NewV7()), UserID: a.userID, Kind: "discord", ConfigEnc: []byte{1},
				VerifiedAt: &agora, CreatedAt: agora,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID), map[string]string{"kind": "discord", "url": webhookDeTeste})
	if e := decodeEnvelope(t, resp); resp.StatusCode != http.StatusConflict || e.Error.Code != "channel_limit_reached" {
		t.Errorf("canal além do limite deu %d/%s", resp.StatusCode, e.Error.Code)
	}
	resp.Body.Close()
}

// O serviço sem Resend recusa o e-mail na validação, antes de tocar no
// banco: por isso o pool nil. Se a ordem mudar, o teste cai com panic, alto.
func TestEmailChannelIsUnavailableWithoutResend(t *testing.T) {
	t.Parallel()
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Channels = notify.NewChannelService(nil, nil, d.Clock, &discordFalso{}, nil, "")
	})
	var env envelopeComCampos
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID), map[string]string{"kind": "email", "email": "p@x.com"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
		env.Error.Fields[0].Field != "kind" || env.Error.Fields[0].Reason != "unavailable" {
		t.Errorf("e-mail sem Resend deu %d/%+v, queria 400 kind/unavailable", resp.StatusCode, env.Error)
	}
}

func TestAnotherUsersChannelIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	var criado canalJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID),
		map[string]string{"kind": "email", "email": "p@x.com"}), &criado)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodDelete, "/api/v1/channels/" + criado.ID},
		{http.MethodPost, "/api/v1/channels/" + criado.ID + "/verify/resend"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, intruso, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s pelo intruso deu %d, queria 404", r.metodo, r.caminho, resp.StatusCode)
		}
	}
	if cs := canaisDe(t, a, intruso); len(cs) != 0 {
		t.Errorf("o intruso listou %d canais", len(cs))
	}
}

// Criar canal e reenviar verificação mandam mensagem para fora: o balde é
// de 5 envios, e a sexta tentativa leva 429 mesmo com o limite por minuto
// (burst 20) folgado. Listar não gasta desse balde.
func TestChannelSendsHaveTheirOwnRateLimit(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	for i := 1; i <= 5; i++ {
		resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "slack"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("tentativa %d deu %d, queria 400", i, resp.StatusCode)
		}
	}
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels/"+uuid.NewString()+"/verify/resend", tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("sexto envio (reenvio) deu %d, queria 429: o balde é compartilhado pelas rotas de envio", resp.StatusCode)
	}
	canaisDe(t, a, tok) // falha se o GET também estiver no balde de envio
}

func TestChannelRoutesRequireAuthentication(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	id := uuid.NewString()
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/channels"},
		{http.MethodPost, "/api/v1/channels"},
		{http.MethodDelete, "/api/v1/channels/" + id},
		{http.MethodPatch, "/api/v1/channels/" + id},
		{http.MethodPost, "/api/v1/channels/" + id + "/verify/resend"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}

// O relógio da API é falso; o link vence pelo relógio dela.
func TestExpiredVerifyLinkIs404(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	chamar(t, a, http.MethodPost, "/api/v1/channels", tokenDe(t, a, a.userID),
		map[string]string{"kind": "email", "email": "p@x.com"}).Body.Close()
	link := a.srv.URL + "/verify/" + a.email.token(t)
	a.clk.Advance(notify.VerifyTTL + time.Second)

	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("link vencido deu %d, queria 404", resp.StatusCode)
	}
}
