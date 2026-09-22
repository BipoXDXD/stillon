package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/config"
	"github.com/BipoXDXD/sentinela/internal/httpapi"
	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

const (
	testEmail = "pessoa@exemplo.com"
	testPass  = "uma-senha-bem-comprida"
)

// As duas chaves têm exatamente 32 bytes: auth.NewManager entra em panic com
// menos que isso (R27), e aqui o Config é construído à mão, sem passar por
// config.Load.
var (
	testJWTSecret = []byte("segredo-de-teste-com-32-bytes!!!")
	testIPHashKey = []byte("chave-de-teste-com-32-bytes-ok!!")
)

type api struct {
	srv    *httptest.Server
	pool   *pgxpool.Pool
	clk    *clock.Fake
	userID uuid.UUID
}

func newAPI(t *testing.T) *api {
	t.Helper()
	return newAPIWith(t, nil)
}

// newAPIWith sobe a API real (rotas, middlewares e Postgres) com um usuário
// já cadastrado. O ajuste opcional permite que um teste mexa na configuração
// antes de as rotas serem montadas.
func newAPIWith(t *testing.T, ajustar func(*config.Config)) *api {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))

	hash, err := auth.HashPassword(testPass)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		userID, testEmail, hash); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		CORSOrigin: "https://app.exemplo.com",
		JWTSecret:  testJWTSecret,
		IPHashKey:  testIPHashKey,
	}
	if ajustar != nil {
		ajustar(cfg)
	}

	// O contexto de vida do servidor: é ele que encerra a varredura de
	// buckets do RateLimit quando o teste termina (R32).
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := auth.NewPGStore(pool)
	mgr := auth.NewManager(store, clk, cfg.IPHashKey)

	srv := httptest.NewServer(httpapi.Routes(ctx, cfg, store, mgr, clk))
	t.Cleanup(srv.Close)
	return &api{srv: srv, pool: pool, clk: clk, userID: userID}
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "sentinela")

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func clientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	TraceID string `json:"trace_id"`
}

func decodeEnvelope(t *testing.T, resp *http.Response) envelope {
	t.Helper()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("resposta não é o envelope de erro: %v", err)
	}
	return env
}

func refreshCookie(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == "sentinela_refresh" {
			return ck
		}
	}
	return nil
}

func login(t *testing.T, a *api, c *http.Client, email, senha string) *http.Response {
	t.Helper()
	return post(t, c, a.srv.URL+"/api/v1/auth/login",
		map[string]string{"email": email, "password": senha})
}

func TestFullAuthFlow(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	// Login.
	resp := login(t, a, c, testEmail, testPass)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" {
		t.Fatal("login não devolveu access token")
	}
	if body.ExpiresIn != int(auth.AccessTTL.Seconds()) {
		t.Errorf("expires_in = %d, queria %d", body.ExpiresIn, int(auth.AccessTTL.Seconds()))
	}

	// O cookie de refresh precisa estar blindado (spec §5.3).
	refresh := refreshCookie(resp)
	if refresh == nil {
		t.Fatal("login não devolveu cookie de refresh")
	}
	if !refresh.HttpOnly {
		t.Error("cookie sem HttpOnly: XSS conseguiria lê-lo")
	}
	if refresh.SameSite != http.SameSiteStrictMode {
		t.Error("cookie sem SameSite=Strict")
	}
	if refresh.Path != "/api/v1/auth" {
		t.Errorf("Path do cookie = %q, queria /api/v1/auth", refresh.Path)
	}

	// Rota protegida com o access token.
	req, err := http.NewRequest(http.MethodGet, a.srv.URL+"/api/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+body.AccessToken)
	me, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatalf("/me devolveu %d, queria 200", me.StatusCode)
	}
	var quem struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(me.Body).Decode(&quem); err != nil {
		t.Fatal(err)
	}
	if quem.ID != a.userID.String() {
		t.Errorf("/me devolveu id %q, queria %q", quem.ID, a.userID)
	}

	// Refresh: o cookie vai sozinho pelo jar.
	r2 := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("refresh devolveu %d", r2.StatusCode)
	}
	if novo := refreshCookie(r2); novo == nil || novo.Value == refresh.Value {
		t.Error("o refresh não rotacionou o cookie")
	}

	// Logout.
	r3 := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	defer r3.Body.Close()
	if r3.StatusCode != http.StatusNoContent {
		t.Errorf("logout devolveu %d, queria 204", r3.StatusCode)
	}

	// Depois do logout o refresh não vale mais.
	r4 := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer r4.Body.Close()
	if r4.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh depois do logout devolveu %d, queria 401", r4.StatusCode)
	}
}

// E-mail inexistente e senha errada precisam ser indistinguíveis na resposta,
// senão o endpoint vira oráculo de quais contas existem (spec §5.2).
//
// Este teste sozinho passaria se o login SEMPRE devolvesse 401 — inclusive
// para a senha certa. Quem morde na direção oposta é TestFullAuthFlow, que
// exige 200 e um access token para a credencial correta; os dois juntos é que
// prendem o comportamento.
func TestLoginDoesNotRevealWhetherAccountExists(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	inexistente := login(t, a, c, "ninguem@exemplo.com", testPass)
	defer inexistente.Body.Close()

	senhaErrada := login(t, a, c, testEmail, "errada")
	defer senhaErrada.Body.Close()

	if inexistente.StatusCode != senhaErrada.StatusCode {
		t.Errorf("status diferentes: %d vs %d", inexistente.StatusCode, senhaErrada.StatusCode)
	}
	if inexistente.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, queria 401", inexistente.StatusCode)
	}

	ea := decodeEnvelope(t, inexistente).Error
	eb := decodeEnvelope(t, senhaErrada).Error
	if ea != eb {
		t.Errorf("respostas distinguíveis: %+v vs %+v", ea, eb)
	}
	if ea.Code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", ea.Code)
	}
}

// O corpo da resposta não é o único canal: sem VerifyDummy, o login de um
// e-mail inexistente volta em microssegundos e o de senha errada em ~100 ms
// de Argon2id, e essa diferença enumera as contas (spec §5.2).
//
// A comparação é relativa ao custo medido de uma verificação real, em vez de
// um limiar absoluto, para não depender da velocidade da máquina.
func TestLoginSpendsTheSameCPUWhetherOrNotTheAccountExists(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	medir := func(email, senha string) time.Duration {
		t.Helper()
		inicio := time.Now()
		resp := login(t, a, c, email, senha)
		resp.Body.Close()
		return time.Since(inicio)
	}

	// Referência: a conta existe e a senha está errada, então o Argon2id roda
	// de verdade.
	real := medir(testEmail, "errada")

	inexistente := medir("ninguem@exemplo.com", testPass)
	if inexistente < real/2 {
		t.Errorf("e-mail inexistente custou %v contra %v da verificação real: "+
			"o tempo de resposta enumera as contas (falta VerifyDummy)", inexistente, real)
	}

	// Conta bloqueada é o terceiro caminho de falha, e também precisa pagar.
	bloquearConta(t, a)
	bloqueada := medir(testEmail, testPass)
	if bloqueada < real/2 {
		t.Errorf("conta bloqueada custou %v contra %v da verificação real: "+
			"falta VerifyDummy no caminho de bloqueio", bloqueada, real)
	}
}

func bloquearConta(t *testing.T, a *api) {
	t.Helper()
	ate := a.clk.Now().Add(time.Hour)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET failed_attempts = $2, locked_until = $3 WHERE id = $1`,
		a.userID, auth.LockThreshold, ate); err != nil {
		t.Fatal(err)
	}
}

// Conta bloqueada recusa até a senha correta, e volta a aceitar quando o
// bloqueio expira.
func TestLockedAccountIsRejectedUntilTheLockExpires(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	bloquearConta(t, a)

	bloqueado := login(t, a, c, testEmail, testPass)
	defer bloqueado.Body.Close()
	if bloqueado.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login em conta bloqueada devolveu %d, queria 401", bloqueado.StatusCode)
	}
	if code := decodeEnvelope(t, bloqueado).Error.Code; code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", code)
	}

	// Passado o bloqueio, a mesma credencial entra.
	a.clk.Advance(2 * time.Hour)
	liberado := login(t, a, c, testEmail, testPass)
	defer liberado.Body.Close()
	if liberado.StatusCode != http.StatusOK {
		t.Errorf("login depois do bloqueio expirar devolveu %d, queria 200", liberado.StatusCode)
	}
}

// O bloqueio não pode se anunciar (R34, spec §5.2).
//
// Se a conta bloqueada tivesse resposta própria, o VerifyDummy inteiro seria
// anulado: bastaria ao atacante mandar seis senhas erradas por e-mail. Onde
// viesse a resposta distinta, a conta existe; onde viesse sempre 401, não
// existe. As contas ficariam enumeradas apesar de todo o custo de CPU
// igualado — por isso o caminho de bloqueio devolve exatamente o mesmo
// status e o mesmo corpo de um e-mail que nunca existiu.
func TestLockedAccountIsIndistinguishableFromUnknownAccount(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	bloquearConta(t, a)

	bloqueada := login(t, a, c, testEmail, testPass)
	defer bloqueada.Body.Close()
	inexistente := login(t, a, c, "ninguem@exemplo.com", testPass)
	defer inexistente.Body.Close()

	if bloqueada.StatusCode != inexistente.StatusCode {
		t.Errorf("status diferentes: conta bloqueada %d, e-mail inexistente %d — "+
			"seis senhas erradas por e-mail enumeram as contas",
			bloqueada.StatusCode, inexistente.StatusCode)
	}

	ea := decodeEnvelope(t, bloqueada).Error
	eb := decodeEnvelope(t, inexistente).Error
	if ea != eb {
		t.Errorf("corpos distinguíveis: conta bloqueada %+v, e-mail inexistente %+v", ea, eb)
	}
	if ea.Code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", ea.Code)
	}
}

// sessoesDoUsuario devolve quantas sessões existem e quantas estão revogadas.
func sessoesDoUsuario(t *testing.T, a *api) (total, revogadas int) {
	t.Helper()
	err := a.pool.QueryRow(context.Background(),
		`SELECT count(*), count(revoked_at) FROM sessions WHERE user_id = $1`, a.userID).
		Scan(&total, &revogadas)
	if err != nil {
		t.Fatal(err)
	}
	return total, revogadas
}

// Detecção de reúso percorrida pelo caminho que um atacante percorre: HTTP
// inteiro, do cookie ao banco (R36, spec §5.2).
//
// Um refresh já rotacionado que reaparece só tem uma explicação: cópia. Não
// há como saber se quem chegou agora é o ladrão ou o dono, então a família
// inteira cai e ambos fazem login de novo.
//
// A asserção que importa é a do banco. Devolver 401 é fácil — o ramo de
// ErrInvalidSession devolve o mesmo 401 e o mesmo cookie limpo, então a
// resposta sozinha não distingue "recusou" de "revogou de verdade". Foi
// exatamente essa distinção que escondeu o bug do Rotate na Task 6.
func TestReusedRefreshCookieRevokesTheWholeFamily(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	entrada := login(t, a, c, testEmail, testPass)
	entrada.Body.Close()
	if entrada.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", entrada.StatusCode)
	}
	antigo := refreshCookie(entrada)
	if antigo == nil {
		t.Fatal("login não devolveu cookie de refresh")
	}

	// Rotaciona: o cookie capturado acima passa a ser um token já consumido.
	rotacao := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	rotacao.Body.Close()
	if rotacao.StatusCode != http.StatusOK {
		t.Fatalf("refresh devolveu %d", rotacao.StatusCode)
	}
	if novo := refreshCookie(rotacao); novo == nil || novo.Value == antigo.Value {
		t.Fatal("o refresh não rotacionou o cookie; não há reúso a testar")
	}

	// A asserção do banco só tem força se a família ainda estiver viva aqui.
	total, revogadas := sessoesDoUsuario(t, a)
	if total < 2 {
		t.Fatalf("esperava ao menos 2 sessões na família, achei %d", total)
	}
	if revogadas != 0 {
		t.Fatalf("a família já estava revogada antes do reúso (%d de %d): "+
			"a asserção final seria verdadeira por acidente", revogadas, total)
	}

	// O ladrão reenvia o token velho, fora do jar.
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "sentinela")
	req.AddCookie(&http.Cookie{Name: "sentinela_refresh", Value: antigo.Value})

	reuso, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer reuso.Body.Close()

	if reuso.StatusCode != http.StatusUnauthorized {
		t.Errorf("reúso devolveu %d, queria 401", reuso.StatusCode)
	}
	if code := decodeEnvelope(t, reuso).Error.Code; code != "invalid_session" {
		t.Errorf("code = %q, queria invalid_session", code)
	}

	// O cookie morto precisa ser limpo, senão o navegador continua reenviando.
	limpo := refreshCookie(reuso)
	if limpo == nil {
		t.Error("o reúso não limpou o cookie")
	} else if limpo.Value != "" || limpo.MaxAge >= 0 {
		t.Errorf("cookie não foi limpo: valor %q, MaxAge %d", limpo.Value, limpo.MaxAge)
	}

	// E a família inteira cai — é isto que separa "recusou" de "revogou".
	total, revogadas = sessoesDoUsuario(t, a)
	if revogadas != total {
		t.Errorf("%d de %d sessões revogadas: a família sobreviveu ao reúso, "+
			"o token roubado continua valendo", revogadas, total)
	}

	// Consequência prática: o cookie legítimo, que ainda está no jar, também
	// morreu. O dono tem de fazer login de novo.
	depois := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer depois.Body.Close()
	if depois.StatusCode != http.StatusUnauthorized {
		t.Errorf("o cookie legítimo continuou valendo depois do reúso: %d", depois.StatusCode)
	}
}

// O 401 tem de vir da ausência do cookie, não de o cookie não alcançar a
// rota: o Path é /api/v1/auth e a rota é /api/v1/auth/refresh, então o cookie
// de uma sessão real chega — é o que a segunda metade deste teste prova.
func TestRefreshWithoutCookieIsRejected(t *testing.T) {
	a := newAPI(t)

	semCookie := post(t, clientWithJar(t), a.srv.URL+"/api/v1/auth/refresh", nil)
	defer semCookie.Body.Close()
	if semCookie.StatusCode != http.StatusUnauthorized {
		t.Errorf("sem cookie: status = %d, queria 401", semCookie.StatusCode)
	}
	if code := decodeEnvelope(t, semCookie).Error.Code; code != "invalid_session" {
		t.Errorf("code = %q, queria invalid_session", code)
	}

	// Com cookie de sessão real, a mesma requisição passa.
	c := clientWithJar(t)
	entrada := login(t, a, c, testEmail, testPass)
	entrada.Body.Close()
	if entrada.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", entrada.StatusCode)
	}
	comCookie := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer comCookie.Body.Close()
	if comCookie.StatusCode != http.StatusOK {
		t.Fatalf("com cookie: status = %d, queria 200 — o cookie não chegou à rota", comCookie.StatusCode)
	}
}

// Um refresh forjado é recusado sem revelar nada além de sessão inválida.
func TestRefreshWithUnknownCookieIsRejected(t *testing.T) {
	a := newAPI(t)

	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "sentinela")
	req.AddCookie(&http.Cookie{Name: "sentinela_refresh", Value: "token-inventado"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, queria 401", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "invalid_session" {
		t.Errorf("code = %q, queria invalid_session", code)
	}
}

// O 401 precisa vir do Authn, não do CORS nem do header customizado: por isso
// o teste confere o código do envelope (ambos os outros devolveriam
// "forbidden") e exige que um token válido passe pela mesma rota.
func TestProtectedRouteRequiresToken(t *testing.T) {
	a := newAPI(t)

	semToken, err := http.Get(a.srv.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	defer semToken.Body.Close()
	if semToken.StatusCode != http.StatusUnauthorized {
		t.Errorf("sem token: status = %d, queria 401", semToken.StatusCode)
	}
	if code := decodeEnvelope(t, semToken).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized — o 401 não veio do Authn", code)
	}

	// Token com assinatura de outro segredo também não entra.
	outro, err := auth.NewAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), a.userID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := statusComToken(t, a, outro); got != http.StatusUnauthorized {
		t.Errorf("token de outro segredo: status = %d, queria 401", got)
	}

	// E o token legítimo passa, o que descarta que o 401 venha de qualquer
	// outra camada da cadeia.
	bom, err := auth.NewAccessToken(testJWTSecret, a.userID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := statusComToken(t, a, bom); got != http.StatusOK {
		t.Errorf("token válido: status = %d, queria 200", got)
	}
}

func statusComToken(t *testing.T, a *api, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, a.srv.URL+"/api/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// O 403 tem de vir do RequireCustomHeader: o mesmo POST com o header sai do
// 403 (vira 401 de credencial inválida), o que descarta que o 403 seja uma
// propriedade da rota ou de outra camada.
func TestMutationWithoutCustomHeaderIsRejected(t *testing.T) {
	a := newAPI(t)

	body, err := json.Marshal(map[string]string{"email": testEmail, "password": "errada"})
	if err != nil {
		t.Fatal(err)
	}
	semHeader, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer semHeader.Body.Close()
	if semHeader.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, queria 403", semHeader.StatusCode)
	}
	if code := decodeEnvelope(t, semHeader).Error.Code; code != "forbidden" {
		t.Errorf("code = %q, queria forbidden", code)
	}

	comHeader := login(t, a, http.DefaultClient, testEmail, "errada")
	defer comHeader.Body.Close()
	if comHeader.StatusCode == http.StatusForbidden {
		t.Error("o mesmo POST com o header customizado continuou 403: o 403 não vem do middleware")
	}
}

// O cookie só pode sair sem Secure quando a aplicação roda em http — o que
// nos testes é proposital, para o cookiejar funcionar. Em https ele precisa
// ser Secure, senão o refresh trafega em claro num downgrade de protocolo.
func TestRefreshCookieIsSecureWhenPublicBaseURLIsHTTPS(t *testing.T) {
	a := newAPIWith(t, func(cfg *config.Config) {
		cfg.PublicBaseURL = "https://sentinela.exemplo.com"
	})

	resp := login(t, a, http.DefaultClient, testEmail, testPass)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}

	ck := refreshCookie(resp)
	if ck == nil {
		t.Fatal("login não devolveu cookie de refresh")
	}
	if !ck.Secure {
		t.Error("cookie sem Secure com PUBLIC_BASE_URL https: o refresh trafegaria em claro")
	}
}

// RequestID precisa ser o middleware MAIS EXTERNO da cadeia montada por
// Routes. Se não for, apierr.WriteError lê um contexto sem trace e todo
// envelope de erro sai com trace_id vazio — o erro visto pelo cliente deixa
// de ser correlacionável com o log do servidor (spec §9.1).
//
// Os testes da Task 7 não pegam isso porque montam as cadeias à mão e sempre
// põem RequestID na frente; a ordem real vive em routes.go. Por isso este
// teste força erros em três profundidades diferentes da cadeia real, sendo a
// mais externa o 429 do RateLimit — o primeiro middleware depois de
// RequestID que responde sozinho.
func TestEveryErrorEnvelopeCarriesTheTraceID(t *testing.T) {
	a := newAPI(t)

	conferir := func(nome string, resp *http.Response) {
		t.Helper()
		defer resp.Body.Close()
		header := resp.Header.Get("X-Trace-Id")
		env := decodeEnvelope(t, resp)
		if env.TraceID == "" {
			t.Errorf("%s: trace_id vazio no envelope — RequestID não é o middleware mais externo", nome)
		}
		if header == "" {
			t.Errorf("%s: resposta sem X-Trace-Id", nome)
		}
		if env.TraceID != header {
			t.Errorf("%s: trace_id %q difere do header %q", nome, env.TraceID, header)
		}
	}

	// Mais fundo: o 401 sai do próprio handler, no fim da cadeia.
	semToken, err := http.Get(a.srv.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	conferir("401 do handler", semToken)

	// No meio: o 403 sai do RequireCustomHeader.
	semHeader, err := http.Post(a.srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	conferir("403 do RequireCustomHeader", semHeader)

	// Mais externo: o 429 sai do RateLimit global, antes de qualquer outra
	// coisa da cadeia rodar.
	var limitado *http.Response
	for i := 0; i < 200 && limitado == nil; i++ {
		resp, err := http.Get(a.srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			limitado = resp
			break
		}
		resp.Body.Close()
	}
	if limitado == nil {
		t.Fatal("o rate limit global não disparou em 200 requisições")
	}
	conferir("429 do RateLimit", limitado)
}
