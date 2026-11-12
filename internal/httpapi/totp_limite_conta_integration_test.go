package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/config"
)

// O limite por CONTA das rotas autenticadas do 2FA (enroll, confirm, disable,
// rotate e rotate/confirm) e da troca de senha: 10 por minuto, rajada de 5, com
// a chave sendo o usuário que authn põe no contexto. Não é o IP (NAT e terceiros
// sem credencial não gastam o balde de ninguém) nem a sessão (logar de novo não
// multiplica o orçamento). A cadeia é authn, sessão viva, limite da conta,
// handler: um token de sessão revogada é recusado pelo viva sem gastar ficha. O login e o /totp/verify seguem por IP, e o contador de provas por
// família segue como o que derruba a sessão. Decisão: docs/adr/0002.

const (
	// rajadaPorConta e umaFichaPorConta são o contrato (10 por minuto, rajada
	// de 5), escritos como número para o teste não seguir o código se mudar.
	rajadaPorConta   = 5
	umaFichaPorConta = 6 * time.Second
	// rajadaPorIP e umaFichaPorIP são o mesmo contrato no balde por IP do login.
	rajadaPorIP   = 5
	umaFichaPorIP = 6 * time.Second

	rotaSenha = "/api/v1/auth/password"
)

var rotasTOTPAutenticadas = []string{
	"/api/v1/auth/totp/enroll",
	"/api/v1/auth/totp/confirm",
	"/api/v1/auth/totp/disable",
	"/api/v1/auth/totp/rotate",
	"/api/v1/auth/totp/rotate/confirm",
}

// rotasDaConta são as seis que dividem o balde por conta.
var rotasDaConta = append(slices.Clone(rotasTOTPAutenticadas), rotaSenha)

// pedirSemProva manda um corpo com campo desconhecido: o DTO estrito o recusa
// com 400 antes de reservar qualquer prova, então o pedido só gasta ficha do
// limite da conta e nunca toca o contador da família nem roda Argon2id. Sem
// token, sai sem Authorization. De origem, o IP da conexão; com ip, o cabeçalho
// do proxy confiável (ver apiAtrasDeProxy, de dispositivo_integration_test.go).
func pedirSemProva(t *testing.T, a *api, rota, token, ip string) (int, http.Header, string) {
	t.Helper()
	st, h, corpo := pedirComCorpo(t, a, rota, token, ip)
	var env envelope
	if err := json.Unmarshal([]byte(corpo), &env); err != nil {
		return st, h, "(sem envelope)"
	}
	return st, h, env.Error.Code
}

// pedirComCorpo é o pedirSemProva que devolve o corpo bruto da resposta, para o
// teste que afirma também que ele não traz dado alheio.
func pedirComCorpo(t *testing.T, a *api, rota, token, ip string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+rota, jsonBody(t, map[string]any{"campo_que_nao_existe": true}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	bruto, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(bruto)
}

// exigirSemDadoAlheio afirma que o corpo e os cabeçalhos de uma recusa não
// trazem nenhum dos valores que pertencem a outra conta (ou ao dono do balde).
func exigirSemDadoAlheio(t *testing.T, quem string, h http.Header, corpo string, alheios ...string) {
	t.Helper()
	conteudo := strings.ToLower(corpo)
	for k, vs := range h {
		conteudo += " " + strings.ToLower(k+"="+strings.Join(vs, ","))
	}
	for _, alheio := range alheios {
		if strings.Contains(conteudo, strings.ToLower(alheio)) {
			t.Errorf("%s: a resposta traz %q: %s", quem, alheio, corpo)
		}
	}
}

// gastarFichas manda n pedidos sem prova da conta, todos esperados como 400.
func gastarFichas(t *testing.T, a *api, rota, token string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if st, _, codigo := pedirSemProva(t, a, rota, token, ""); st != http.StatusBadRequest || codigo != "validation_failed" {
			t.Fatalf("pedido %d de %d: %d %s, queria 400 validation_failed", i, n, st, codigo)
		}
	}
}

func exigirRecusaPorConta(t *testing.T, st int, h http.Header, codigo string) {
	t.Helper()
	if st != http.StatusTooManyRequests || codigo != "rate_limited" {
		t.Fatalf("depois da rajada: %d %s, queria 429 rate_limited", st, codigo)
	}
	espera, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || espera <= 0 || time.Duration(espera)*time.Second > umaFichaPorConta+time.Second {
		t.Errorf("Retry-After = %q, queria uns segundos positivos (até %v)", h.Get("Retry-After"), umaFichaPorConta)
	}
}

func exigirPassa(t *testing.T, quem string, st int, codigo string) {
	t.Helper()
	if st != http.StatusBadRequest || codigo != "validation_failed" {
		t.Errorf("%s: %d %s, queria o 400 normal da rota", quem, st, codigo)
	}
}

// segundoUsuario cadastra outra conta, na mesma base, e devolve o id.
func segundoUsuario(t *testing.T, a *api) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		id, "vizinho@exemplo.com", hashDaFixture()); err != nil {
		t.Fatal(err)
	}
	return id
}

// apiComFichaPorPedido é a API com o relógio andando uma ficha da conta (6 s) a
// cada pedido de a.cliente(). É para o teste que mede OUTRA coisa (o contador de
// provas por família, o erro interno de uma rota) e manda mais pedidos do que a
// rajada da conta: o limite da conta não se mete no que ele mede.
func apiComFichaPorPedido(t *testing.T) *api {
	t.Helper()
	return apiComFichaPorPedidoCom(t, nil)
}

func apiComFichaPorPedidoCom(t *testing.T, ajustar func(*config.Config)) *api {
	t.Helper()
	a := newAPIWith(t, ajustar)
	a.fichaPorPedido = true
	return a
}

// fichaPorPedido avança o relógio falso antes de cada pedido.
type fichaPorPedido struct{ a *api }

func (f fichaPorPedido) RoundTrip(r *http.Request) (*http.Response, error) {
	f.a.clk.Advance(umaFichaPorConta)
	return http.DefaultTransport.RoundTrip(r)
}

// cliente é o http.DefaultClient, ou o que gasta uma ficha por pedido na
// apiComFichaPorPedido.
func (a *api) cliente() *http.Client {
	if a.fichaPorPedido {
		return &http.Client{Transport: fichaPorPedido{a}}
	}
	return http.DefaultClient
}

// esperarFichaDoIP devolve uma ficha aos limites por IP (o do login e o piso
// global, 10 e 120 por minuto). É para o teste sequencial que passa da rajada do
// login sem querer medi-la.
func esperarFichaDoIP(a *api) { a.clk.Advance(umaFichaPorIP) }

// apiAtrasDeProxyComProvas é a API atrás do proxy confiável: o teste escolhe a
// origem de cada pedido pelo X-Forwarded-For.
func apiAtrasDeProxyComProvas(t *testing.T) *api {
	t.Helper()
	return newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	})
}

// Em cada rota, a rajada passa (o status é o da rota: 400 do DTO estrito) e o
// pedido seguinte, da mesma conta, é 429 rate_limited com Retry-After.
func TestAccountRoutesAnswer429PastTheAccountBurst(t *testing.T) {
	for _, rota := range rotasDaConta {
		t.Run(rota, func(t *testing.T) {
			a := newAPI(t)
			token := tokenDe(t, a, a.userID)
			gastarFichas(t, a, rota, token, rajadaPorConta)
			st, h, codigo := pedirSemProva(t, a, rota, token, "")
			exigirRecusaPorConta(t, st, h, codigo)
		})
	}
}

// As seis rotas dividem um balde só: com uma instância por rota, quem chuta
// provas alternando as rotas multiplicaria o orçamento por seis.
func TestAccountRoutesShareOneBucket(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	for i := range rajadaPorConta {
		if st, _, codigo := pedirSemProva(t, a, rotasDaConta[i], token, ""); st != http.StatusBadRequest {
			t.Fatalf("pedido %d: %d %s, queria 400", i+1, st, codigo)
		}
	}
	for _, rota := range rotasDaConta {
		st, h, codigo := pedirSemProva(t, a, rota, token, "")
		exigirRecusaPorConta(t, st, h, codigo)
	}
}

// A chave é a CONTA, e não a sessão: duas sessões da mesma conta (dois logins),
// de IPs diferentes, dividem o balde, e o gasto se soma entre rotas e sessões.
// Logar de novo não rende ficha.
func TestAccountBucketIsSharedByTwoSessionsOfTheSameAccount(t *testing.T) {
	a := apiAtrasDeProxyComProvas(t)
	_, tokenA := entrar(t, a)
	_, tokenB := entrar(t, a)
	if familiaDe(t, tokenA, a) == familiaDe(t, tokenB, a) {
		t.Fatal("o teste precisa de duas famílias distintas")
	}
	ipA, ipB := novaOrigem(), novaOrigem()
	// Três fichas na sessão A (três rotas) e duas na B (outras duas): cinco no total.
	gastos := []struct {
		token, ip, rota string
	}{
		{tokenA, ipA, rotasDaConta[0]}, {tokenA, ipA, rotasDaConta[1]}, {tokenA, ipA, rotasDaConta[2]},
		{tokenB, ipB, rotasDaConta[3]}, {tokenB, ipB, rotaSenha},
	}
	for i, g := range gastos {
		st, _, codigo := pedirSemProva(t, a, g.rota, g.token, g.ip)
		exigirPassa(t, "pedido "+strconv.Itoa(i+1)+" em "+g.rota, st, codigo)
	}
	for _, rota := range rotasDaConta {
		for _, quem := range []struct{ token, ip string }{{tokenA, ipA}, {tokenB, ipB}} {
			st, h, codigo := pedirSemProva(t, a, rota, quem.token, quem.ip)
			exigirRecusaPorConta(t, st, h, codigo)
		}
	}
}

// NAT: dois usuários no mesmo IP. Esgotar o balde de um não gera 429 para o
// outro em nenhuma das rotas, nem os faz disputar fichas, e nenhuma resposta traz
// dado do outro.
func TestAccountBucketDoesNotAffectAnotherUserBehindTheSameIP(t *testing.T) {
	a := newAPI(t)
	idB := segundoUsuario(t, a)
	tokenA := tokenDe(t, a, a.userID)
	tokenB := tokenDe(t, a, idB)
	familiaA, familiaB := familiaDe(t, tokenA, a), familiaDe(t, tokenB, a)
	gastarFichas(t, a, rotasDaConta[3], tokenA, rajadaPorConta)
	st, h, corpo := pedirComCorpo(t, a, rotasDaConta[3], tokenA, "")
	exigirRecusaPorConta(t, st, h, codigoDoCorpo(t, corpo))
	exigirSemDadoAlheio(t, "429 da conta A", h, corpo, idB.String(), familiaB.String(), "vizinho@exemplo.com")
	for _, rota := range rotasTOTPAutenticadas {
		st, h, corpo := pedirComCorpo(t, a, rota, tokenB, "")
		exigirPassa(t, "o vizinho em "+rota, st, codigoDoCorpo(t, corpo))
		exigirSemDadoAlheio(t, "o vizinho em "+rota, h, corpo, a.userID.String(), familiaA.String(), testEmail)
	}
}

// codigoDoCorpo lê o code do envelope de um corpo já lido.
func codigoDoCorpo(t *testing.T, corpo string) string {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(corpo), &env); err != nil {
		return "(sem envelope)"
	}
	return env.Error.Code
}

// Duas contas atrás de IPs diferentes não se afetam: com um balde global (ou
// qualquer chave que não seja a conta), o abuso de uma esvaziaria o da outra.
func TestAccountBucketsOfDifferentAccountsAreIndependentAcrossIPs(t *testing.T) {
	a := apiAtrasDeProxyComProvas(t)
	tokenA := tokenDe(t, a, a.userID)
	tokenB := tokenDe(t, a, segundoUsuario(t, a))
	ipA, ipB := novaOrigem(), novaOrigem()
	for i, rota := range rotasDaConta[:rajadaPorConta] {
		st, _, codigo := pedirSemProva(t, a, rota, tokenA, ipA)
		exigirPassa(t, "conta A, pedido "+strconv.Itoa(i+1), st, codigo)
	}
	st, h, codigo := pedirSemProva(t, a, rotaSenha, tokenA, ipA)
	exigirRecusaPorConta(t, st, h, codigo)
	for i, rota := range rotasDaConta[:rajadaPorConta] {
		st, _, codigo := pedirSemProva(t, a, rota, tokenB, ipB)
		exigirPassa(t, "conta B, pedido "+strconv.Itoa(i+1), st, codigo)
	}
	st, h, codigo = pedirSemProva(t, a, rotaSenha, tokenB, ipB)
	exigirRecusaPorConta(t, st, h, codigo)
}

// Quem não tem credencial válida não gasta ficha de ninguém: pedido sem Bearer,
// com token forjado ou vencido é 401 e deixa o balde do dono intacto, mesmo
// vindo do mesmo IP. A próxima rajada do dono passa inteira.
func TestRequestsWithoutAValidTokenDoNotSpendTheAccountBucket(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	familia := familiaDe(t, token, a)
	forjado, err := auth.NewAccessToken([]byte("outro-segredo-com-mais-de-32-bytes!!"), a.userID, familia, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	vencido, err := auth.NewAccessToken(testJWTSecret, a.userID, familia, a.clk.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i, intruso := range []string{"", forjado, vencido, "", forjado, vencido} {
		rota := rotasDaConta[i]
		st, h, corpo := pedirComCorpo(t, a, rota, intruso, "")
		if codigo := codigoDoCorpo(t, corpo); st != http.StatusUnauthorized || codigo != "unauthorized" {
			t.Fatalf("intruso %d em %s: %d %s, queria 401 unauthorized", i+1, rota, st, codigo)
		}
		exigirSemDadoAlheio(t, "intruso "+strconv.Itoa(i+1), h, corpo, a.userID.String(), familia.String(), testEmail, "127.0.0.1")
	}
	gastarFichas(t, a, rotasDaConta[0], token, rajadaPorConta)
	st, h, codigo := pedirSemProva(t, a, rotasDaConta[0], token, "")
	exigirRecusaPorConta(t, st, h, codigo)
}

// O caso do e2e: login, enroll, confirm, rotate, rotate/confirm, disable e login
// de novo, do mesmo IP, sem 429. O login gasta ficha do IP, o resto gasta da
// conta: nenhum balde disputa com o outro. A troca e o desligamento usam código
// de recuperação, porque o código do app já foi usado neste passo de 30 s e
// esperar o próximo devolveria fichas.
func TestLoginAndTheTOTPFlowDoNotCompeteForTheSameBucket(t *testing.T) {
	a := newAPI(t)
	entrada := login(t, a, clientWithJar(t), testEmail, testPass)
	token := accessDe(t, entrada)
	entrada.Body.Close()

	enviar := func(rota string, corpo map[string]string) *http.Response {
		return postComToken(t, http.DefaultClient, a.srv.URL+rota, token, corpo)
	}
	resp := enviar(rotasTOTPAutenticadas[0], map[string]string{"password": testPass})
	var enroll struct {
		URI string `json:"otpauth_uri"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&enroll) != nil {
		t.Fatalf("enroll: %d, queria 200", resp.StatusCode)
	}
	resp.Body.Close()
	chave, err := otp.NewKeyFromURL(enroll.URI)
	if err != nil {
		t.Fatal(err)
	}
	codigo, err := totp.GenerateCode(chave.Secret(), a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp = enviar(rotasTOTPAutenticadas[1], map[string]string{"code": codigo})
	var confirmado struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&confirmado) != nil || len(confirmado.RecoveryCodes) < 2 {
		t.Fatalf("confirm: %d, queria 200 com códigos de recuperação", resp.StatusCode)
	}
	resp.Body.Close()

	st, segredoNovo := iniciarTrocaHTTP(t, a, token,
		map[string]string{"password": testPass, "code": confirmado.RecoveryCodes[0]})
	if st != http.StatusOK {
		t.Fatalf("rotate: %d, queria 200", st)
	}
	st, erro, novos := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, segredoNovo))
	if st != http.StatusOK || len(novos) < 1 {
		t.Fatalf("rotate/confirm: %d %s, queria 200", st, erro)
	}
	resp = enviar(rotasTOTPAutenticadas[2], map[string]string{"password": testPass, "code": novos[0]})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable: %d, queria 200 ou 204", resp.StatusCode)
	}

	denovo := login(t, a, clientWithJar(t), testEmail, testPass)
	denovo.Body.Close()
	if denovo.StatusCode != http.StatusOK {
		t.Errorf("login depois do fluxo: %d, queria 200", denovo.StatusCode)
	}
}

// Passada a janela, no relógio injetado, a ficha volta: um instante antes
// continua 429, e na hora a conta passa de novo.
func TestAccountBucketRefillsOnTheInjectedClock(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	rota := rotasDaConta[0]
	gastarFichas(t, a, rota, token, rajadaPorConta)
	a.clk.Advance(umaFichaPorConta - time.Millisecond)
	if st, _, _ := pedirSemProva(t, a, rota, token, ""); st != http.StatusTooManyRequests {
		t.Errorf("um instante antes da ficha: %d, queria 429", st)
	}
	a.clk.Advance(time.Millisecond)
	st, _, codigo := pedirSemProva(t, a, rota, token, "")
	exigirPassa(t, "com a ficha de volta", st, codigo)
}

// Com o balde esgotado, a rota nem chega à senha: nenhum Argon2id roda para um
// pedido barrado, mesmo com o corpo que o rodaria.
func TestExhaustedAccountBucketDoesNotRunArgon2(t *testing.T) {
	corpos := map[string]map[string]string{
		rotasTOTPAutenticadas[0]: {"password": testPass},
		rotasTOTPAutenticadas[2]: {"password": testPass, "code": "000000"},
		rotasTOTPAutenticadas[3]: {"password": testPass, "code": "000000"},
		rotaSenha:                {"current_password": testPass, "new_password": senhaNova},
	}
	for rota, corpo := range corpos {
		t.Run(rota, func(t *testing.T) {
			a := newAPI(t)
			token := tokenDe(t, a, a.userID)
			gastarFichas(t, a, rota, token, rajadaPorConta)
			antes := auth.Argon2Verifications()
			resp := postComToken(t, http.DefaultClient, a.srv.URL+rota, token, corpo)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("status %d, queria 429", resp.StatusCode)
			}
			if n := auth.Argon2Verifications() - antes; n != 0 {
				t.Errorf("%d Argon2id num pedido barrado, queria 0", n)
			}
		})
	}
}

// O 429 não carrega nada além do código e do trace_id: nem o IP, nem o usuário,
// nem o e-mail, nem a família, nem o nome do balde, no corpo ou nos cabeçalhos.
func TestAccountLimitResponseLeaksNothing(t *testing.T) {
	a := newAPI(t)
	token, _, familia := familiaComRefresh(t, a)
	gastarFichas(t, a, rotasDaConta[0], token, rajadaPorConta)
	resp := postComToken(t, http.DefaultClient, a.srv.URL+rotasDaConta[0], token,
		map[string]any{"campo_que_nao_existe": true})
	defer resp.Body.Close()
	bruto, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, queria 429", resp.StatusCode)
	}
	conteudo := strings.ToLower(string(bruto))
	for k, vs := range resp.Header {
		conteudo += " " + strings.ToLower(k+"="+strings.Join(vs, ","))
	}
	for _, proibido := range []string{
		"127.0.0.1", a.userID.String(), familia.String(), testEmail, "bucket", "limiter", "totp", "family", "familia", "proof", "prova",
	} {
		if strings.Contains(conteudo, strings.ToLower(proibido)) {
			t.Errorf("o 429 traz %q: %s", proibido, conteudo)
		}
	}
	for _, h := range []string{"X-Ratelimit-Remaining", "X-Ratelimit-Limit"} {
		if v := resp.Header.Get(h); v != "" {
			t.Errorf("cabeçalho %s = %q no 429", h, v)
		}
	}
}

// O 429 sai ANTES do handler: não reserva nem conta prova na família. Quatro
// provas erradas, a rajada esgotada e três pedidos barrados; depois da janela, a
// família ainda está viva e é a QUINTA prova errada (e não a oitava) que a
// derruba. O contador de provas segue como era.
func TestAccountLimitDoesNotCountRefusedRequestsAsWrongProofs(t *testing.T) {
	a := newAPI(t)
	token, _, familia := familiaComRefresh(t, a)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Minute) // o cadastro gastou fichas da mesma conta

	errada := func() (int, string) {
		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/disable", token,
			map[string]string{"password": testPass, "code": codigoErradoPara(t, a, segredo)})
		defer resp.Body.Close()
		return resp.StatusCode, codigoDoErro(t, resp)
	}
	for i := 1; i <= provasPorFamilia-1; i++ {
		if st, codigo := errada(); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("prova errada %d: %d %s, queria 401 invalid_credentials", i, st, codigo)
		}
	}
	// A rajada tem uma ficha sobrando; este pedido a gasta sem tocar a família.
	if st, _, _ := pedirSemProva(t, a, rotasTOTPAutenticadas[2], token, ""); st != http.StatusBadRequest {
		t.Fatalf("pedido que esgota a rajada: %d, queria 400", st)
	}
	for i := range 3 {
		if st, codigo := errada(); st != http.StatusTooManyRequests || codigo != "rate_limited" {
			t.Fatalf("barrado %d: %d %s, queria 429 rate_limited", i+1, st, codigo)
		}
	}
	a.clk.Advance(time.Minute)
	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusOK {
		t.Fatalf("a família caiu por pedidos barrados (GET /sessions: %d)", st)
	}
	if st, codigo := errada(); st != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("quinta prova errada: %d %s, queria 401 unauthorized", st, codigo)
	}
	if st := statusDaListaDeSessoes(t, a, token); st != http.StatusUnauthorized {
		t.Errorf("GET /sessions depois da quinta: %d, queria 401", st)
	}
	if n := eventosDaFamilia(t, a, "session.proof_limit", familia); n != 1 {
		t.Errorf("%d eventos session.proof_limit, queria 1", n)
	}
}

// O piso global por IP (120 por minuto, rajada de 30) continua atingindo estas
// rotas: o pedido sem credencial não gasta o balde da conta, mas gasta o do IP.
func TestTheGlobalIPFloorStillReachesTheAccountRoutes(t *testing.T) {
	a := newAPI(t)
	const rajadaGlobal = 30
	for i := 1; i <= rajadaGlobal; i++ {
		if st, _, codigo := pedirSemProva(t, a, rotasTOTPAutenticadas[0], "", ""); st != http.StatusUnauthorized {
			t.Fatalf("pedido %d: %d %s, queria 401", i, st, codigo)
		}
	}
	st, _, codigo := pedirSemProva(t, a, rotasTOTPAutenticadas[0], "", "")
	if st != http.StatusTooManyRequests || codigo != "rate_limited" {
		t.Errorf("depois da rajada global: %d %s, queria 429 rate_limited", st, codigo)
	}
}

// O login e o /totp/verify seguem por IP, e o balde da conta é outro: esgotar um
// não gasta o outro, nos dois sentidos.
func TestLoginAndVerifyStayPerIPAndDoNotShareTheAccountBucket(t *testing.T) {
	loginFalho := func(t *testing.T, a *api) int {
		resp := login(t, a, clientWithJar(t), "ninguem@exemplo.com", testPass)
		resp.Body.Close()
		return resp.StatusCode
	}
	verifyFalho := func(t *testing.T, a *api) int {
		resp := post(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": "desafio-invalido", "code": "000000"})
		resp.Body.Close()
		return resp.StatusCode
	}
	t.Run("o IP esgotado no login não gasta a conta", func(t *testing.T) {
		a := newAPI(t)
		token := tokenDe(t, a, a.userID)
		for i := range rajadaPorIP {
			if st := loginFalho(t, a); st != http.StatusUnauthorized {
				t.Fatalf("login %d: %d, queria 401", i+1, st)
			}
		}
		if st := loginFalho(t, a); st != http.StatusTooManyRequests {
			t.Fatalf("login depois da rajada: %d, queria 429 (o login segue por IP)", st)
		}
		for _, rota := range rotasTOTPAutenticadas {
			st, _, codigo := pedirSemProva(t, a, rota, token, "")
			exigirPassa(t, "a conta com o IP esgotado em "+rota, st, codigo)
		}
	})
	t.Run("a conta esgotada não gasta o login nem o verify", func(t *testing.T) {
		a := newAPI(t)
		token := tokenDe(t, a, a.userID)
		gastarFichas(t, a, rotasDaConta[0], token, rajadaPorConta)
		st, h, codigo := pedirSemProva(t, a, rotasDaConta[0], token, "")
		exigirRecusaPorConta(t, st, h, codigo)
		for i := range rajadaPorIP {
			if st := loginFalho(t, a); st != http.StatusUnauthorized {
				t.Fatalf("login %d com a conta esgotada: %d, queria 401", i+1, st)
			}
		}
		for i := range rajadaPorIP {
			if st := verifyFalho(t, a); st != http.StatusUnauthorized {
				t.Fatalf("verify %d com a conta esgotada: %d, queria 401", i+1, st)
			}
		}
	})
}

// A ordem é authn, sessão viva, limite da conta, handler: um token de sessão
// revogada, ainda dentro do prazo, recebe a recusa do viva e NÃO gasta ficha. A
// prova é uma sessão viva da mesma conta, que continua com a rajada inteira.
func TestRevokedTokenIsRefusedBeforeTheAccountBucket(t *testing.T) {
	for _, rota := range rotasDaConta {
		t.Run(rota, func(t *testing.T) {
			a := newAPI(t)
			c, revogado := entrar(t, a)
			_, vivo := entrar(t, a)
			sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
			sair.Body.Close()
			for i := 1; i <= rajadaPorConta+3; i++ {
				st, _, codigo := pedirSemProva(t, a, rota, revogado, "")
				if st != http.StatusUnauthorized || codigo != "unauthorized" {
					t.Fatalf("pedido %d com a família revogada: %d %s, queria 401 unauthorized", i, st, codigo)
				}
			}
			gastarFichas(t, a, rota, vivo, rajadaPorConta)
			st, h, codigo := pedirSemProva(t, a, rota, vivo, "")
			exigirRecusaPorConta(t, st, h, codigo)
		})
	}
}

// Um token de sessão revogada não troca a senha, nem com a senha atual certa: o
// viva recusa antes do limite e do serviço, e a senha gravada não muda.
func TestRevokedTokenCannotChangeThePassword(t *testing.T) {
	a := newAPI(t)
	c, revogado := entrar(t, a)
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()
	resp := trocarSenha(t, a, revogado, map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if codigo := codigoDoErro(t, resp); resp.StatusCode != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("troca com a família revogada: %d %s, queria 401 unauthorized", resp.StatusCode, codigo)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a senha mudou com a sessão revogada")
	}
}

// A senha atual errada em /auth/password gasta ficha da CONTA, e trocar de IP
// não zera o balde: quatro senhas erradas e um pedido sem prova, cada um de uma
// origem diferente, esgotam a rajada, e a sexta, de uma origem nova, é 429.
func TestWrongCurrentPasswordSpendsTheAccountBucketAcrossIPs(t *testing.T) {
	a := apiAtrasDeProxyComProvas(t)
	token := tokenDe(t, a, a.userID)
	trocar := func(corpo any) (int, http.Header, string) {
		req, err := http.NewRequest(http.MethodPost, a.srv.URL+rotaSenha, jsonBody(t, corpo))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "stillon")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Forwarded-For", novaOrigem())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, resp.Header, codigoDoErro(t, resp)
	}
	errada := map[string]string{"current_password": "senha-que-nao-e-a-dele", "new_password": senhaNova}
	for i := 1; i <= provasPorFamilia-1; i++ {
		if st, _, codigo := trocar(errada); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Fatalf("senha errada %d: %d %s, queria 401 invalid_credentials", i, st, codigo)
		}
	}
	if st, _, codigo := trocar(map[string]any{"campo_que_nao_existe": true}); st != http.StatusBadRequest {
		t.Fatalf("pedido sem prova: %d %s, queria 400", st, codigo)
	}
	st, h, codigo := trocar(errada)
	exigirRecusaPorConta(t, st, h, codigo)
}
