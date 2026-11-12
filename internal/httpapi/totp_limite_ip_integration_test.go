package httpapi_test

import (
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/config"
)

// O limite por IP das rotas autenticadas do 2FA: enroll, confirm, disable,
// rotate e rotate/confirm. É o mesmo balde do login e da troca de senha (10 por
// minuto, rajada de 5), contado por IP e sem olhar a sessão: trocar de família,
// mesmo da mesma conta, não ganha um balde novo. O contador de provas erradas
// por família (provasDaFamilia) continua à parte, e é o que derruba a sessão.

// rajadaPorIP é a rajada do contrato, escrita como número para o teste não
// seguir o código se ela mudar.
const rajadaPorIP = 5

// umaFichaPorIP é o intervalo entre fichas do contrato (10 por minuto).
const umaFichaPorIP = 6 * time.Second

var rotasTOTPAutenticadas = []string{
	"/api/v1/auth/totp/enroll",
	"/api/v1/auth/totp/confirm",
	"/api/v1/auth/totp/disable",
	"/api/v1/auth/totp/rotate",
	"/api/v1/auth/totp/rotate/confirm",
}

// pedirSemProva manda um corpo com campo desconhecido: o DTO estrito o recusa
// com 400 antes de reservar qualquer prova, então o pedido só gasta ficha do
// limite por IP e nunca toca o contador da família. De origem, o IP da conexão;
// com ip, o cabeçalho do proxy confiável (ver apiAtrasDeProxy, de dispositivo_integration_test.go).
// Com token vazio, o pedido vai sem o cabeçalho Authorization.
func pedirSemProva(t *testing.T, a *api, rota, token, ip string) (int, http.Header, string) {
	t.Helper()
	corpo := jsonBody(t, map[string]any{"campo_que_nao_existe": true})
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+rota, corpo)
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
	return resp.StatusCode, resp.Header, codigoDoErro(t, resp)
}

// apiOrigemPorPedido é a API atrás de proxy confiável (127.0.0.0/8) com cada
// pedido de a.cliente() vindo de uma origem nova. É para o teste que mede OUTRA
// coisa (o contador de provas por família, o erro interno de uma rota) e manda
// mais pedidos do que a rajada do IP: o limitador por IP não se mete no que ele
// mede, como já faz o apiAtrasDeProxy com o novaOrigem() nos testes de
// dispositivo.
func apiOrigemPorPedido(t *testing.T) *api {
	t.Helper()
	return apiOrigemPorPedidoCom(t, nil)
}

func apiOrigemPorPedidoCom(t *testing.T, ajustar func(*config.Config)) *api {
	t.Helper()
	a := newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		if ajustar != nil {
			ajustar(cfg)
		}
	})
	a.origemPorPedido = true
	return a
}

// clienteOrigemPorPedido põe um X-Forwarded-For novo em cada pedido.
var clienteOrigemPorPedido = &http.Client{Transport: origemPorPedido{}}

type origemPorPedido struct{}

func (origemPorPedido) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Forwarded-For", novaOrigem())
	return http.DefaultTransport.RoundTrip(r)
}

// cliente é o http.DefaultClient, ou o de origem por pedido na apiOrigemPorPedido.
func (a *api) cliente() *http.Client {
	if a.origemPorPedido {
		return clienteOrigemPorPedido
	}
	return http.DefaultClient
}

// esperarFichaDoIP devolve uma ficha ao balde do IP (10 por minuto). É para o
// teste sequencial que passa da rajada sem querer medi-la.
func esperarFichaDoIP(a *api) { a.clk.Advance(umaFichaPorIP) }

func exigirRecusaPorIP(t *testing.T, st int, h http.Header, codigo string) {
	t.Helper()
	if st != http.StatusTooManyRequests || codigo != "rate_limited" {
		t.Fatalf("depois da rajada: %d %s, queria 429 rate_limited", st, codigo)
	}
	espera, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || espera <= 0 || time.Duration(espera)*time.Second > umaFichaPorIP+time.Second {
		t.Errorf("Retry-After = %q, queria uns segundos positivos (até %v)", h.Get("Retry-After"), umaFichaPorIP)
	}
}

// Em cada rota, a rajada passa (o status é o da rota: 400 do DTO estrito) e o
// pedido seguinte, do mesmo IP, é 429 rate_limited com Retry-After.
func TestTOTPRoutesAnswer429PastTheIPBurst(t *testing.T) {
	for _, rota := range rotasTOTPAutenticadas {
		t.Run(rota, func(t *testing.T) {
			a := newAPI(t)
			token, _, _ := familiaComRefresh(t, a)
			for i := 1; i <= rajadaPorIP; i++ {
				if st, _, codigo := pedirSemProva(t, a, rota, token, ""); st != http.StatusBadRequest || codigo != "validation_failed" {
					t.Fatalf("pedido %d da rajada: %d %s, queria 400 validation_failed", i, st, codigo)
				}
			}
			st, h, codigo := pedirSemProva(t, a, rota, token, "")
			exigirRecusaPorIP(t, st, h, codigo)
		})
	}
}

// As cinco rotas dividem um balde só: com uma instância por rota, quem chuta
// provas alternando as rotas multiplicaria o orçamento por cinco.
func TestTOTPRoutesShareOneIPBucket(t *testing.T) {
	a := newAPI(t)
	token, _, _ := familiaComRefresh(t, a)
	for i := range rajadaPorIP {
		rota := rotasTOTPAutenticadas[i%len(rotasTOTPAutenticadas)]
		if st, _, _ := pedirSemProva(t, a, rota, token, ""); st != http.StatusBadRequest {
			t.Fatalf("pedido %d: %d, queria 400", i+1, st)
		}
	}
	for _, rota := range rotasTOTPAutenticadas {
		st, h, codigo := pedirSemProva(t, a, rota, token, "")
		exigirRecusaPorIP(t, st, h, codigo)
	}
}

// O limite é do IP, não da sessão: outra família da MESMA conta, vinda do mesmo
// IP, cai no balde já esgotado.
func TestTOTPIPLimitIgnoresTheFamily(t *testing.T) {
	a := newAPI(t)
	tokenA, _, familiaA := familiaComRefresh(t, a)
	tokenB, _, familiaB := familiaComRefresh(t, a)
	if familiaA == familiaB {
		t.Fatal("o teste precisa de duas famílias distintas")
	}
	for i := 1; i <= rajadaPorIP; i++ {
		if st, _, _ := pedirSemProva(t, a, rotasTOTPAutenticadas[2], tokenA, ""); st != http.StatusBadRequest {
			t.Fatalf("pedido %d da família A: %d, queria 400", i, st)
		}
	}
	for _, rota := range rotasTOTPAutenticadas {
		st, h, codigo := pedirSemProva(t, a, rota, tokenB, "")
		exigirRecusaPorIP(t, st, h, codigo)
	}
}

// Outro IP não é afetado pelo balde esgotado do primeiro.
func TestTOTPIPLimitDoesNotAffectAnotherIP(t *testing.T) {
	a := apiAtrasDeProxy(t)
	token, _, _ := familiaComRefresh(t, a)
	const abusador, vizinho = "198.51.100.1", "198.51.100.2"
	for i := 1; i <= rajadaPorIP; i++ {
		if st, _, _ := pedirSemProva(t, a, rotasTOTPAutenticadas[3], token, abusador); st != http.StatusBadRequest {
			t.Fatalf("pedido %d do abusador: %d, queria 400", i, st)
		}
	}
	st, h, codigo := pedirSemProva(t, a, rotasTOTPAutenticadas[3], token, abusador)
	exigirRecusaPorIP(t, st, h, codigo)
	if st, _, codigo := pedirSemProva(t, a, rotasTOTPAutenticadas[3], token, vizinho); st != http.StatusBadRequest || codigo != "validation_failed" {
		t.Errorf("o outro IP: %d %s, queria o 400 normal da rota", st, codigo)
	}
}

// Passada a janela, no relógio injetado, a ficha volta: um instante antes
// continua 429, e na hora o IP passa de novo.
func TestTOTPIPLimitRefillsOnTheInjectedClock(t *testing.T) {
	a := newAPI(t)
	token, _, _ := familiaComRefresh(t, a)
	rota := rotasTOTPAutenticadas[0]
	for range rajadaPorIP {
		pedirSemProva(t, a, rota, token, "")
	}
	a.clk.Advance(umaFichaPorIP - time.Millisecond)
	if st, _, _ := pedirSemProva(t, a, rota, token, ""); st != http.StatusTooManyRequests {
		t.Errorf("um instante antes da ficha: %d, queria 429", st)
	}
	a.clk.Advance(time.Millisecond)
	if st, _, codigo := pedirSemProva(t, a, rota, token, ""); st != http.StatusBadRequest || codigo != "validation_failed" {
		t.Errorf("com a ficha de volta: %d %s, queria o 400 da rota", st, codigo)
	}
}

// O 429 não carrega nada além do código e do trace_id: nem o IP, nem o usuário,
// nem a família, nem o nome do balde.
func TestTOTPIPLimitResponseLeaksNothing(t *testing.T) {
	a := newAPI(t)
	token, _, familia := familiaComRefresh(t, a)
	for range rajadaPorIP {
		pedirSemProva(t, a, rotasTOTPAutenticadas[0], token, "")
	}
	resp := postComToken(t, http.DefaultClient, a.srv.URL+rotasTOTPAutenticadas[0], token,
		map[string]any{"campo_que_nao_existe": true})
	defer resp.Body.Close()
	bruto, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	corpo := string(bruto)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, queria 429", resp.StatusCode)
	}
	for _, proibido := range []string{
		"127.0.0.1", a.userID.String(), familia.String(), "bucket", "limiter", "totp", "family", "familia", "proof", "prova",
	} {
		if strings.Contains(strings.ToLower(corpo), strings.ToLower(proibido)) {
			t.Errorf("o corpo do 429 traz %q: %s", proibido, corpo)
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
func TestTOTPIPLimitDoesNotCountRefusedRequestsAsWrongProofs(t *testing.T) {
	a := newAPI(t)
	token, _, familia := familiaComRefresh(t, a)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Minute) // o cadastro gastou fichas do mesmo IP

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

// O limite por IP vem POR FORA de authn e da guarda de sessão viva: esgotada a
// rajada, o pedido sem token, com token forjado ou de uma família já encerrada
// recebe o mesmo 429 de quem tem token válido, e não o 401. Se o limite passar
// para dentro de authn, o 401 sai antes e o balde deixa de proteger justamente
// quem não tem credencial. O controle, de outra origem, confere que cada um
// desses tokens é de fato recusado (401) quando o balde não está esgotado.
func TestTOTPIPLimitRunsBeforeAuthentication(t *testing.T) {
	for _, rota := range rotasTOTPAutenticadas {
		t.Run(rota, func(t *testing.T) {
			a := apiAtrasDeProxy(t)
			valido, _, familia := familiaComRefresh(t, a)
			morto, refreshMorto, _ := familiaComRefresh(t, a)
			encerrarFamilia(t, a, refreshMorto)
			forjado, err := auth.NewAccessToken([]byte("outro-segredo-com-32-bytes-ou-+!"), a.userID, familia, a.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			casos := []struct{ nome, token string }{
				{"token ausente", ""},
				{"token forjado", forjado},
				{"família encerrada", morto},
			}

			vizinho := novaOrigem()
			for _, c := range casos {
				if st, _, codigo := pedirSemProva(t, a, rota, c.token, vizinho); st != http.StatusUnauthorized || codigo != "unauthorized" {
					t.Fatalf("controle, %s: %d %s, queria 401 unauthorized", c.nome, st, codigo)
				}
			}

			const origem = "198.51.100.77"
			for i := 1; i <= rajadaPorIP; i++ {
				if st, _, _ := pedirSemProva(t, a, rota, valido, origem); st != http.StatusBadRequest {
					t.Fatalf("pedido %d da rajada: %d, queria 400", i, st)
				}
			}
			for _, c := range casos {
				t.Run(c.nome, func(t *testing.T) {
					st, h, codigo := pedirSemProva(t, a, rota, c.token, origem)
					exigirRecusaPorIP(t, st, h, codigo)
				})
			}
		})
	}
}

// encerrarFamilia faz o logout com o refresh da família, que a deixa morta.
func encerrarFamilia(t *testing.T, a *api, refresh string) {
	t.Helper()
	c := clientWithJar(t)
	u, err := url.Parse(a.srv.URL + "/api/v1/auth")
	if err != nil {
		t.Fatal(err)
	}
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "stillon_refresh", Value: refresh, Path: "/api/v1/auth"}})
	resp := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout devolveu %d", resp.StatusCode)
	}
}
