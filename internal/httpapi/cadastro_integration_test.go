package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/turnstile"
	"github.com/BipoXDXD/stillon/internal/turnstile/turnstiletest"
)

// linksDoPainel é a base dos links dos e-mails de conta nos testes.
var linksDoPainel = account.Links{Base: "https://app.exemplo.com"}

// emailDeConta é uma chamada ao mailer de conta falso.
type emailDeConta struct{ para, assunto, corpo string }

// mailerDeContaFalso grava os e-mails de conta, em vez de enfileirá-los.
type mailerDeContaFalso struct {
	mu       sync.Mutex
	enviados []emailDeConta
}

func (m *mailerDeContaFalso) SendAccountEmail(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enviados = append(m.enviados, emailDeConta{to, subject, body})
	return nil
}

func (m *mailerDeContaFalso) todos() []emailDeConta {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.enviados)
}

// tokenDo tira o token do link (fragmento #token=) no corpo do e-mail.
func tokenDo(t *testing.T, e emailDeConta) string {
	t.Helper()
	_, depois, ok := strings.Cut(e.corpo, "#token=")
	if !ok {
		t.Fatalf("e-mail sem link com token: %q", e.corpo)
	}
	token, _, _ := strings.Cut(depois, "\n")
	return strings.TrimSpace(token)
}

// tokenDoUltimo é o token do último e-mail de conta enviado.
func (m *mailerDeContaFalso) tokenDoUltimo(t *testing.T) string {
	t.Helper()
	todos := m.todos()
	if len(todos) == 0 {
		t.Fatal("nenhum e-mail de conta enviado")
	}
	return tokenDo(t, todos[len(todos)-1])
}

// respostaCrua é o que dá para comparar entre duas respostas: status,
// cabeçalhos (sem os que mudam a cada pedido) e corpo.
type respostaCrua struct {
	status     int
	cabecalhos http.Header
	corpo      string
}

func pedirPublico(t *testing.T, a *api, metodo, caminho string, corpo any) respostaCrua {
	t.Helper()
	resp := chamar(t, a, metodo, caminho, "", corpo)
	defer resp.Body.Close()
	lido, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	h := resp.Header.Clone()
	for _, variavel := range []string{"Date", "X-Trace-Id", "X-Request-Id", "Traceparent"} {
		h.Del(variavel)
	}
	return respostaCrua{status: resp.StatusCode, cabecalhos: h, corpo: string(lido)}
}

func exigirIguais(t *testing.T, nome string, a, b respostaCrua) {
	t.Helper()
	if a.status != b.status || a.corpo != b.corpo || !maps.EqualFunc(a.cabecalhos, b.cabecalhos, slices.Equal) {
		t.Errorf("%s: respostas diferentes:\n%d %v %q\n%d %v %q", nome,
			a.status, a.cabecalhos, a.corpo, b.status, b.cabecalhos, b.corpo)
	}
}

func exigir202SemCorpo(t *testing.T, nome string, r respostaCrua) {
	t.Helper()
	if r.status != http.StatusAccepted || r.corpo != "" {
		t.Errorf("%s: %d %q, queria 202 sem corpo", nome, r.status, r.corpo)
	}
}

// exigirErro confere status e código do envelope.
func exigirErro(t *testing.T, nome string, r respostaCrua, status int, codigo apierr.ErrorCode) {
	t.Helper()
	if r.status != status || !strings.Contains(r.corpo, `"code":"`+string(codigo)+`"`) {
		t.Errorf("%s: %d %s, queria %d %s", nome, r.status, r.corpo, status, codigo)
	}
}

func corpoSignup(email, senha string) map[string]string {
	return map[string]string{"email": email, "password": senha, "turnstile_token": turnstiletest.TokenFalso}
}

func corpoForgot(email string) map[string]string {
	return map[string]string{"email": email, "turnstile_token": turnstiletest.TokenFalso}
}

func signup(t *testing.T, a *api, email string) respostaCrua {
	t.Helper()
	return pedirPublico(t, a, http.MethodPost, "/api/v1/auth/signup", corpoSignup(email, testPass))
}

func forgot(t *testing.T, a *api, email string) respostaCrua {
	t.Helper()
	return pedirPublico(t, a, http.MethodPost, "/api/v1/auth/password/forgot", corpoForgot(email))
}

func confirmar(t *testing.T, a *api, token string) respostaCrua {
	t.Helper()
	return pedirPublico(t, a, http.MethodPost, "/api/v1/auth/email/confirm", map[string]string{"token": token})
}

func redefinir(t *testing.T, a *api, token, senha string) respostaCrua {
	t.Helper()
	return pedirPublico(t, a, http.MethodPost, "/api/v1/auth/password/reset",
		map[string]string{"token": token, "new_password": senha})
}

// trocarMailerDeConta sobe um servidor novo, sobre o mesmo banco, com o
// Cadastro e a Recuperacao mandando os e-mails de conta para m.
func (a *api) trocarMailerDeConta(t *testing.T, m account.AccountMailer) {
	t.Helper()
	a.srv.Close()
	a.deps.Cadastro = account.NewCadastro(a.pool, a.clk, m, linksDoPainel)
	a.deps.Recuperacao = account.NewRecuperacao(a.pool, a.clk, m, linksDoPainel)
	a.srv = httptest.NewServer(httpapi.Routes(a.ctx, a.cfg, a.deps))
	t.Cleanup(a.srv.Close)
}

func contarUsuarios(t *testing.T, a *api) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// emailVerificadoNoMe lê o email_verified do GET /me.
func emailVerificadoNoMe(t *testing.T, a *api, token string) bool {
	t.Helper()
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /me: %d %s", status, corpo)
	}
	var me struct {
		EmailVerified *bool `json:"email_verified"`
	}
	if err := json.Unmarshal([]byte(corpo), &me); err != nil || me.EmailVerified == nil {
		t.Fatalf("/me sem email_verified: %v %s", err, corpo)
	}
	return *me.EmailVerified
}

// O signup responde byte a byte igual com e-mail novo e com e-mail que já tem
// conta: 202, corpo vazio, os mesmos cabeçalhos. Os dois mandam um e-mail.
func TestSignupContaNovaEExistenteRespondemIgual(t *testing.T) {
	a := newAPI(t)
	nova := signup(t, a, "nova@exemplo.com")
	existente := signup(t, a, testEmail)

	exigir202SemCorpo(t, "conta nova", nova)
	exigirIguais(t, "conta nova × existente", nova, existente)
	if n := len(a.conta.todos()); n != 2 {
		t.Errorf("%d e-mails de conta, queria 2 (um por ramo)", n)
	}
}

// O caminho inteiro: signup, link do e-mail, login com a conta não
// confirmada (que não cria), confirmação e a conta que passa a criar.
func TestSignupFluxoCompleto(t *testing.T) {
	a := newAPI(t)
	const email = "fulano@exemplo.com"
	exigir202SemCorpo(t, "signup", signup(t, a, email))
	token := a.conta.tokenDoUltimo(t)

	c := clientWithJar(t)
	resp := login(t, a, c, email, testPass)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("login da conta nova: %d", resp.StatusCode)
	}
	acesso := accessDe(t, resp)
	resp.Body.Close()
	if emailVerificadoNoMe(t, a, acesso) {
		t.Fatal("/me da conta recém-cadastrada trouxe email_verified true")
	}
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/targets", acesso, alvoValido); status != http.StatusForbidden {
		t.Fatalf("POST /targets antes da confirmação: %d %s, queria 403", status, corpo)
	}

	if r := confirmar(t, a, token); r.status != http.StatusNoContent {
		t.Fatalf("confirmar: %d %s, queria 204", r.status, r.corpo)
	}
	if !emailVerificadoNoMe(t, a, acesso) {
		t.Fatal("/me depois da confirmação trouxe email_verified false")
	}
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/targets", acesso, alvoValido); status != http.StatusCreated {
		t.Fatalf("POST /targets depois da confirmação: %d %s, queria 201", status, corpo)
	}
}

// O token usado e o token lixo dão o mesmo 400 token_invalid.
func TestConfirmarTokenReusadoOuLixo(t *testing.T) {
	a := newAPI(t)
	exigir202SemCorpo(t, "signup", signup(t, a, "fulano@exemplo.com"))
	token := a.conta.tokenDoUltimo(t)
	if r := confirmar(t, a, token); r.status != http.StatusNoContent {
		t.Fatalf("primeira confirmação: %d %s", r.status, r.corpo)
	}
	exigirErro(t, "token reusado", confirmar(t, a, token), http.StatusBadRequest, apierr.CodeTokenInvalid)
	exigirErro(t, "token lixo", confirmar(t, a, "x"), http.StatusBadRequest, apierr.CodeTokenInvalid)
	exigirErro(t, "token comprido demais", confirmar(t, a, strings.Repeat("A", 129)), http.StatusBadRequest, apierr.CodeTokenInvalid)
	exigirErro(t, "token vazio", confirmar(t, a, ""), http.StatusBadRequest, apierr.CodeTokenInvalid)
}

// Turnstile recusado: 400 turnstile_failed, e nada acontece.
func TestSignupTurnstileRecusado(t *testing.T) {
	a := newAPICom(t, nil, func(d *httpapi.Deps) { d.Turnstile = turnstiletest.Verificador(t, turnstiletest.SecretFalha) })
	exigirErro(t, "signup", signup(t, a, "fulano@exemplo.com"), http.StatusBadRequest, apierr.CodeTurnstileFailed)
	exigirErro(t, "forgot", forgot(t, a, testEmail), http.StatusBadRequest, apierr.CodeTurnstileFailed)
	if n := contarUsuarios(t, a); n != 1 {
		t.Errorf("%d usuários, queria só o da fixture", n)
	}
	if n := len(a.conta.todos()); n != 0 {
		t.Errorf("%d e-mails, queria 0", n)
	}
}

// O formato é conferido antes do Turnstile: um formulário errado não gasta o
// token de uso único, e o siteverify não é chamado.
func TestSignupValidaAntesDoTurnstile(t *testing.T) {
	a := newAPI(t)
	curta := pedirPublico(t, a, http.MethodPost, "/api/v1/auth/signup", corpoSignup("fulano@exemplo.com", strings.Repeat("a", 14)))
	exigirErro(t, "senha de 14", curta, http.StatusBadRequest, apierr.CodeValidation)
	if !strings.Contains(curta.corpo, `{"field":"password","reason":"too_short"}`) {
		t.Errorf("senha de 14 sem o campo password/too_short: %s", curta.corpo)
	}
	semEmail := pedirPublico(t, a, http.MethodPost, "/api/v1/auth/signup", corpoSignup("não é e-mail", testPass))
	if !strings.Contains(semEmail.corpo, `{"field":"email","reason":"invalid"}`) {
		t.Errorf("e-mail inválido sem o campo email/invalid: %d %s", semEmail.status, semEmail.corpo)
	}
	forgotSemEmail := pedirPublico(t, a, http.MethodPost, "/api/v1/auth/password/forgot", corpoForgot("x"))
	if !strings.Contains(forgotSemEmail.corpo, `{"field":"email","reason":"invalid"}`) {
		t.Errorf("forgot com e-mail inválido sem o campo email/invalid: %d %s", forgotSemEmail.status, forgotSemEmail.corpo)
	}
	if n := len(turnstiletest.Recebidos(a.turnstile)); n != 0 {
		t.Errorf("o siteverify recebeu %d chamadas, queria 0", n)
	}
}

// siteverify fora do ar: 503 unavailable (falha fechada), e ninguém entra.
func TestSignupTurnstileForaDoAr(t *testing.T) {
	foraDoAr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(foraDoAr.Close)
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Turnstile = turnstile.NewComEndpoint(crypto.Secret(turnstiletest.SecretPassa), "", foraDoAr.URL, foraDoAr.Client())
	})
	exigirErro(t, "signup", signup(t, a, "fulano@exemplo.com"), http.StatusServiceUnavailable, apierr.CodeUnavailable)
	exigirErro(t, "forgot", forgot(t, a, testEmail), http.StatusServiceUnavailable, apierr.CodeUnavailable)
	if n := contarUsuarios(t, a); n != 1 {
		t.Errorf("%d usuários, queria só o da fixture", n)
	}
}

// Desligado: signup e forgot são 404, e a configuração pública sai sem a
// chave (mesmo com uma configurada).
func TestSignupEForgotDesligados(t *testing.T) {
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.CadastroPublico = httpapi.ConfigCadastro{Habilitado: false, SiteKey: turnstiletest.SiteKeyPassa}
	})
	exigirErro(t, "signup", signup(t, a, "fulano@exemplo.com"), http.StatusNotFound, apierr.CodeNotFound)
	exigirErro(t, "forgot", forgot(t, a, testEmail), http.StatusNotFound, apierr.CodeNotFound)
	conferirConfig(t, a, false, "")
}

func TestSignupConfigLigado(t *testing.T) {
	a := newAPI(t)
	conferirConfig(t, a, true, "1x00000000000000000000AA")
}

func conferirConfig(t *testing.T, a *api, ligado bool, chave string) {
	t.Helper()
	r := pedirPublico(t, a, http.MethodGet, "/api/v1/auth/signup-config", nil)
	if r.status != http.StatusOK {
		t.Fatalf("signup-config: %d %s", r.status, r.corpo)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(r.corpo), &cfg); err != nil {
		t.Fatal(err)
	}
	quer := map[string]any{"enabled": ligado, "turnstile_site_key": chave}
	if !maps.Equal(cfg, quer) {
		t.Errorf("signup-config = %v, queria %v", cfg, quer)
	}
}

// O forgot responde byte a byte igual com conta e sem conta; só o com conta
// manda e-mail.
func TestForgotComESemContaRespondemIgual(t *testing.T) {
	a := newAPI(t)
	com := forgot(t, a, testEmail)
	sem := forgot(t, a, "ninguem@exemplo.com")
	exigir202SemCorpo(t, "com conta", com)
	exigirIguais(t, "com × sem conta", com, sem)
	if e := a.conta.todos(); len(e) != 1 || e[0].para != testEmail {
		t.Errorf("e-mails = %v, queria só um, para %s", e, testEmail)
	}
}

// A redefinição derruba a sessão aberta: o refresh antigo dá 401, e só a
// senha nova entra.
func TestResetDerrubaSessaoEExigeLoginNovo(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	resp := login(t, a, c, testEmail, testPass)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}

	exigir202SemCorpo(t, "forgot", forgot(t, a, testEmail))
	const senhaNova = "a-senha-nova-bem-comprida"
	if r := redefinir(t, a, a.conta.tokenDoUltimo(t), senhaNova); r.status != http.StatusNoContent {
		t.Fatalf("reset: %d %s, queria 204", r.status, r.corpo)
	}

	refresh := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	refresh.Body.Close()
	if refresh.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh com o cookie anterior ao reset: %d, queria 401", refresh.StatusCode)
	}
	nova := login(t, a, clientWithJar(t), testEmail, senhaNova)
	nova.Body.Close()
	if nova.StatusCode != http.StatusOK {
		t.Errorf("login com a senha nova: %d, queria 200", nova.StatusCode)
	}
}

// Só o último link vale: o primeiro de dois pedidos dá token_invalid.
func TestResetLinkAnteriorInvalido(t *testing.T) {
	a := newAPI(t)
	exigir202SemCorpo(t, "primeiro forgot", forgot(t, a, testEmail))
	primeiro := a.conta.tokenDoUltimo(t)
	exigir202SemCorpo(t, "segundo forgot", forgot(t, a, testEmail))
	segundo := a.conta.tokenDoUltimo(t)

	exigirErro(t, "primeiro link", redefinir(t, a, primeiro, "a-senha-nova-bem-comprida"), http.StatusBadRequest, apierr.CodeTokenInvalid)
	if r := redefinir(t, a, segundo, "a-senha-nova-bem-comprida"); r.status != http.StatusNoContent {
		t.Errorf("segundo link: %d %s, queria 204", r.status, r.corpo)
	}
}

// A senha nova fora da política é 400 no campo new_password, e não gasta o
// link: o mesmo token vale depois com uma senha boa.
func TestResetSenhaFracaNaoGastaLink(t *testing.T) {
	a := newAPI(t)
	exigir202SemCorpo(t, "forgot", forgot(t, a, testEmail))
	token := a.conta.tokenDoUltimo(t)
	fraca := redefinir(t, a, token, "curta")
	exigirErro(t, "senha fraca", fraca, http.StatusBadRequest, apierr.CodeValidation)
	if !strings.Contains(fraca.corpo, `{"field":"new_password","reason":"too_short"}`) {
		t.Errorf("senha fraca sem o campo new_password/too_short: %s", fraca.corpo)
	}
	if r := redefinir(t, a, token, "a-senha-nova-bem-comprida"); r.status != http.StatusNoContent {
		t.Errorf("o mesmo link com senha boa: %d %s, queria 204", r.status, r.corpo)
	}
}

// O reenvio exige sessão; com ela, 202 e um e-mail novo de confirmação.
func TestReenvioExigeSessao(t *testing.T) {
	a := newAPI(t)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET email_verified_at = NULL WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/auth/email/resend", "", nil); status != http.StatusUnauthorized {
		t.Errorf("reenvio sem sessão: %d %s, queria 401", status, corpo)
	}
	_, token := entrar(t, a)
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/auth/email/resend", token, nil); status != http.StatusAccepted || corpo != "" {
		t.Errorf("reenvio com sessão: %d %q, queria 202 sem corpo", status, corpo)
	}
	e := a.conta.todos()
	if len(e) != 1 || e[0].para != testEmail || !strings.Contains(e[0].corpo, linksDoPainel.ConfirmarEmail(tokenDo(t, e[0]))) {
		t.Errorf("e-mails = %v, queria um link de confirmação para %s", e, testEmail)
	}
}

// Já confirmado, o reenvio responde como o reenvio que mandou (202), e nada
// sai: o painel não precisa de um caso a mais.
func TestReenvioJaConfirmadoRespondeIgualSemEmail(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/auth/email/resend", token, nil); status != http.StatusAccepted || corpo != "" {
		t.Errorf("reenvio já confirmado: %d %q, queria 202 sem corpo", status, corpo)
	}
	if n := len(a.conta.todos()); n != 0 {
		t.Errorf("%d e-mails, queria 0", n)
	}
}

// transporteContado conta as entregas que chegaram ao provedor.
type transporteContado struct{ entregas atomic.Int32 }

func (tr *transporteContado) SendAccount(context.Context, crypto.Secret, string, string) error {
	tr.entregas.Add(1)
	return nil
}

// tetoFalso libera ou recusa toda reserva, e conta as consultas.
type tetoFalso struct {
	libera    bool
	consultas atomic.Int32
}

func (tf *tetoFalso) Reservar(context.Context) (bool, error) {
	tf.consultas.Add(1)
	return tf.libera, nil
}

// baldeQueLibera é o balde do destinatário sempre com ficha.
type baldeQueLibera struct{}

func (baldeQueLibera) Gastar(context.Context, string, []byte, time.Duration, int) (time.Duration, bool, error) {
	return 0, true, nil
}

// cenarioDeTeto sobe a API com o contaemail.Enviador real atrás do Cadastro e
// da Recuperacao, com o teto dado, e devolve as respostas de signup e forgot,
// com e sem conta, e as entregas ao transporte.
func cenarioDeTeto(t *testing.T, libera bool) (respostas []respostaCrua, entregas int32) {
	t.Helper()
	a := newAPI(t)
	transporte, teto := &transporteContado{}, &tetoFalso{libera: libera}
	enviador := contaemail.New(transporte, teto, baldeQueLibera{}, testIPHashKey, slog.New(slog.DiscardHandler))
	ctx, parar := context.WithCancel(context.Background())
	rodando := make(chan struct{})
	go func() { defer close(rodando); enviador.Rodar(ctx) }()
	t.Cleanup(func() { parar(); <-rodando })
	a.trocarMailerDeConta(t, enviador)

	respostas = []respostaCrua{
		signup(t, a, "nova@exemplo.com"),
		signup(t, a, testEmail),
		forgot(t, a, testEmail),
		forgot(t, a, "ninguem@exemplo.com"),
	}
	// Três e-mails na fila (os dois do signup e o forgot com conta): espera o
	// enviador consultar o teto para os três antes de contar as entregas.
	prazo := time.Now().Add(10 * time.Second)
	for teto.consultas.Load() < 3 {
		if time.Now().After(prazo) {
			t.Fatalf("o enviador consultou o teto %d vezes, queria 3", teto.consultas.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	parar()
	<-rodando
	return respostas, transporte.entregas.Load()
}

// Review Focus 4: com o teto esgotado, signup e forgot respondem byte a byte
// como com o teto livre, com conta e sem conta, e nada chega ao transporte.
func TestRespostasIdenticasComTetoEsgotado(t *testing.T) {
	// Um subteste por cenário: o banco de teste é um só, restaurado no fim
	// de cada subteste, e a fixture da conta existente nasce de novo.
	var livre, esgotado []respostaCrua
	var entregasLivre, entregasEsgotado int32
	t.Run("teto livre", func(t *testing.T) { livre, entregasLivre = cenarioDeTeto(t, true) })
	t.Run("teto esgotado", func(t *testing.T) { esgotado, entregasEsgotado = cenarioDeTeto(t, false) })
	if len(livre) != 4 || len(esgotado) != 4 {
		t.Fatal("um dos cenários não chegou ao fim")
	}
	if entregasLivre != 3 {
		t.Fatalf("com o teto livre, %d entregas; queria 3 (o cenário não exercita o envio)", entregasLivre)
	}
	if entregasEsgotado != 0 {
		t.Errorf("com o teto esgotado, %d entregas; queria 0", entregasEsgotado)
	}
	nomes := []string{"signup conta nova", "signup conta existente", "forgot com conta", "forgot sem conta"}
	for i, nome := range nomes {
		exigir202SemCorpo(t, nome+" (teto livre)", livre[i])
		exigirIguais(t, nome+": teto livre × esgotado", livre[i], esgotado[i])
	}
	exigirIguais(t, "signup com teto esgotado: nova × existente", esgotado[0], esgotado[1])
	exigirIguais(t, "forgot com teto esgotado: com × sem conta", esgotado[2], esgotado[3])
}

// O balde de signup por IP: rajada de 5, e o 6º seguido do mesmo IP é 429.
// Os pedidos têm e-mail inválido (400 sem Argon2id): o limite vem antes do
// handler e conta todos.
func TestSignupLimitePorIP(t *testing.T) {
	a := newAPI(t)
	invalido := corpoSignup("sem-arroba", testPass)
	for i := range 5 {
		if r := pedirPublico(t, a, http.MethodPost, "/api/v1/auth/signup", invalido); r.status != http.StatusBadRequest {
			t.Fatalf("pedido %d da rajada: %d %s, queria 400", i+1, r.status, r.corpo)
		}
	}
	exigirErro(t, "6º signup", signup(t, a, "fulano@exemplo.com"), http.StatusTooManyRequests, apierr.CodeRateLimited)
	if n := contarUsuarios(t, a); n != 1 {
		t.Errorf("%d usuários, queria só o da fixture", n)
	}
}
