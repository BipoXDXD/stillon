package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
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
	err   error
}

func (f *carteiroFalso) SendVerification(_ context.Context, _ crypto.Secret, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
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

// O canal que falhou de vez aparece como failing, com o motivo e a hora; antes
// da falha, os dois campos são null.
func TestFailingChannelShowsTheLastError(t *testing.T) {
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
	a := newAPI(t)
	sessao, token := criarCanalPendente(t, a)

	rec := confirmarComCorpo(a, token, iotest.ErrReader(io.ErrUnexpectedEOF))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("falha de leitura deu %d, queria 400", rec.Code)
	}
	exigirStatusDoCanal(t, a, sessao, "pending")
}

func TestUnknownVerifyTokenIs404(t *testing.T) {
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

func apiComTetoDeVerificacao(t *testing.T, limite int) *api {
	t.Helper()
	a := newAPI(t)
	box, err := crypto.NewBox(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	teto := storage.NewTetoDeEmailsDeConta(a.pool, a.clk, limite)
	enviador := contaemail.New(&transporteContado{}, teto, a.deps.LimitesIP, testIPHashKey, slog.New(slog.DiscardHandler))
	a.deps.Channels = notify.NewChannelService(a.pool, box, a.clk, a.discord,
		contaemail.NovaVerificacaoComTeto(enviador, a.email), a.cfg.PublicBaseURL)
	a.deps.CadastroPublico = httpapi.ConfigCadastro{}
	a.reiniciar(t)
	return a
}

func TestVerificacaoDeCanalSuprimidaPreservaEstado(t *testing.T) {
	a := apiComTetoDeVerificacao(t, 1)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	legado := "/api/v1/channels"
	projeto := "/api/v1/projects/" + testdb.ProjetoPadrao(t, a.userID).String() + "/channels"
	criar := func(caminho, email string) canalJSON {
		t.Helper()
		resp := chamar(t, a, http.MethodPost, caminho, tok, map[string]string{"kind": "email", "email": email})
		var criado canalJSON
		corpo := lerCorpo(t, resp, &criado)
		if resp.StatusCode != http.StatusCreated || criado.Status != "pending" {
			t.Fatalf("criar canal: %d %s, queria 201 com status pending", resp.StatusCode, corpo)
		}
		return criado
	}
	lerCanal := func(id string) db.NotifChannel {
		t.Helper()
		canalID, err := uuid.Parse(id)
		if err != nil {
			t.Fatal(err)
		}
		var row db.NotifChannel
		err = testdb.NoProjetoPadrao(t, a.pool, a.userID, func(q *db.Queries, projeto uuid.UUID) error {
			var err error
			row, err = q.GetChannel(ctx, db.GetChannelParams{ProjectID: projeto, ID: canalID})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	reenviar := func(caminho string) {
		t.Helper()
		if status, corpo := lerCru(t, a, http.MethodPost, caminho+"/verify/resend", tok, nil); status != http.StatusNoContent {
			t.Fatalf("reenvio: %d %s, queria 204", status, corpo)
		}
	}

	comLink := criar(legado, "com-link@exemplo.com")
	linkAnterior := a.email.token(t)
	antes := lerCanal(comLink.ID)
	a.clk.Advance(time.Minute)

	semLink := criar(projeto, "sem-link@exemplo.com")
	if row := lerCanal(semLink.ID); row.VerifyTokenHash != nil || row.VerifyExpiresAt != nil {
		t.Error("criação suprimida gravou hash ou validade sem entregar um link")
	}

	reenviar(projeto + "/" + comLink.ID)
	depois := lerCanal(comLink.ID)
	if !bytes.Equal(antes.VerifyTokenHash, depois.VerifyTokenHash) ||
		antes.VerifyExpiresAt == nil || depois.VerifyExpiresAt == nil ||
		!depois.VerifyExpiresAt.Equal(*antes.VerifyExpiresAt) {
		t.Error("reenvio suprimido mudou o hash ou a validade do link anterior")
	}
	if st, err := a.deps.Channels.CheckToken(ctx, linkAnterior); err != nil || st != notify.VerifyPending {
		t.Errorf("link anterior após reenvio suprimido: %v, %v, queria pendente", st, err)
	}
	if a.email.token(t) != linkAnterior {
		t.Error("outro link foi enviado com o teto esgotado")
	}

	a.clk.Advance(24 * time.Hour)
	tok = tokenDe(t, a, a.userID)
	reenviar(legado + "/" + semLink.ID)
	novo := a.email.token(t)
	if novo == linkAnterior {
		t.Fatal("reenvio do canal sem token após a renovação do teto não gerou um link")
	}
	if err := a.deps.Channels.ConfirmToken(ctx, novo); err != nil {
		t.Errorf("confirmar o link do canal que nasceu suprimido: %v", err)
	}
}

func TestReenvioComFalhaDoProvedorPreservaLink(t *testing.T) {
	a := apiComTetoDeVerificacao(t, 2)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok,
		map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	var criado canalJSON
	corpo := lerCorpo(t, resp, &criado)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar canal: %d %s, queria 201", resp.StatusCode, corpo)
	}
	link := a.email.token(t)
	a.email.mu.Lock()
	a.email.err = &notify.SendError{Reason: notify.ReasonRateLimited}
	a.email.mu.Unlock()

	resp = chamar(t, a, http.MethodPost, "/api/v1/channels/"+criado.ID+"/verify/resend", tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("reenvio: %d, queria 502", resp.StatusCode)
	} else if e := decodeEnvelope(t, resp); e.Error.Code != "delivery_failed" {
		t.Errorf("código de erro no reenvio: %s, queria delivery_failed", e.Error.Code)
	}
	if err := a.deps.Channels.ConfirmToken(context.Background(), link); err != nil {
		t.Errorf("link anterior após falha do provedor: %v", err)
	}
}

// Um canal de e-mail pendente cujo destino não abre (config_enc corrompido,
// ou selado com outra chave quando algum selo recente abre; a MASTER_KEY toda
// errada nem sobe, por notify.CheckMasterKey) não tem para onde reenviar: o
// reenvio responde 500 internal_error, sem mandar e-mail e sem pôr no corpo o
// erro de decifrar.
func TestResendWithUnreadableDestinationIsInternalError(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	agora := a.clk.Now()
	expira := agora.Add(notify.VerifyTTL)
	id := uuid.Must(uuid.NewV7())
	err := testdb.NoProjetoPadrao(t, a.pool, a.userID, func(q *db.Queries, projeto uuid.UUID) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: id, UserID: a.userID, ProjectID: projeto, Kind: string(notify.KindEmail), ConfigEnc: []byte("destino-ilegivel"),
			VerifyTokenHash: []byte("hash-antigo"), VerifyExpiresAt: &expira, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, a, http.MethodPost, "/api/v1/channels/"+id.String()+"/verify/resend", tokenDe(t, a, a.userID), nil)
	corpo := string(lerCorpo(t, resp, nil))
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(corpo, `"internal_error"`) {
		t.Errorf("reenvio com destino ilegível deu %d: %s; queria 500 internal_error", resp.StatusCode, corpo)
	}
	for _, proibido := range append([]string{"decifrar", "destino do canal", "cipher", "message authentication"}, marcasDeDetalheInterno...) {
		if strings.Contains(corpo, proibido) {
			t.Errorf("corpo do reenvio vaza detalhe interno %q: %s", proibido, corpo)
		}
	}
	a.email.mu.Lock()
	enviados := len(a.email.links)
	a.email.mu.Unlock()
	if enviados != 0 {
		t.Errorf("reenvio com destino ilegível mandou %d e-mail(s), queria nenhum", enviados)
	}
}

func TestChannelLimitIs409(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	agora := a.clk.Now()
	err := testdb.NoProjetoPadrao(t, a.pool, a.userID, func(q *db.Queries, projeto uuid.UUID) error {
		for range notify.MaxChannels {
			if err := q.InsertChannel(ctx, db.InsertChannelParams{
				ID: uuid.Must(uuid.NewV7()), UserID: a.userID, ProjectID: projeto, Kind: "discord", ConfigEnc: []byte{1},
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

// As gêmeas de envio gastam o mesmo balde das rotas antigas: com o relógio
// parado, 3 criações e 2 reenvios pelo projeto esgotam a rajada, o sexto envio
// e a criação pela rota antiga dão 429, e a leitura do projeto fica de fora.
func TestProjectChannelSendsShareTheSendBucket(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	noProjeto := "/api/v1/projects/" + testdb.ProjetoPadrao(t, a.userID).String() + "/channels"
	for i := 1; i <= 3; i++ {
		status, corpo := lerCru(t, a, http.MethodPost, noProjeto, tok,
			map[string]string{"kind": "discord", "url": webhookDeTeste})
		if status == http.StatusTooManyRequests {
			t.Fatalf("criação %d pelo projeto deu 429 antes da rajada: %s", i, corpo)
		}
	}
	reenvio := noProjeto + "/" + uuid.NewString() + "/verify/resend"
	for i := 1; i <= 2; i++ {
		status, corpo := lerCru(t, a, http.MethodPost, reenvio, tok, nil)
		if status == http.StatusTooManyRequests {
			t.Fatalf("reenvio %d pelo projeto deu 429 antes da rajada: %s", i, corpo)
		}
	}
	status, corpo := lerCru(t, a, http.MethodPost, reenvio, tok, nil)
	if erro, _ := decodificar(t, corpo)["error"].(map[string]any); status != http.StatusTooManyRequests || erro["code"] != "rate_limited" {
		t.Fatalf("sexto envio (reenvio pelo projeto) deu %d, queria 429 rate_limited: %s", status, corpo)
	}
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("criação pela rota antiga deu %d, queria 429: o balde de envio é um só", resp.StatusCode)
	}
	if status, corpo := lerCru(t, a, http.MethodGet, noProjeto, tok, nil); status != http.StatusOK {
		t.Errorf("GET %s deu %d, queria 200: a leitura não gasta o balde de envio: %s", noProjeto, status, corpo)
	}
}

func TestChannelRoutesRequireAuthentication(t *testing.T) {
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
