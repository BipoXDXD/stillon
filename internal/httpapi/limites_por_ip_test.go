package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// Endereços de documentação (RFC 5737): nenhum existe de verdade.
const (
	conexaoDireta  = "203.0.113.10:4000"
	conexaoDoProxy = "192.0.2.10:4000"
	faixaDoProxy   = "192.0.2.0/24"
	clienteA       = "198.51.100.1"
	clienteB       = "198.51.100.2"
)

// rotaLimitada é uma rota de verdade com limitador por IP. O pedido leva um
// identificador que o serviço recusa antes de ir ao banco, e por isso estes
// testes não precisam de Postgres: o limitador vem antes do serviço.
type rotaLimitada struct {
	nome       string
	metodo     string
	caminho    string
	corpo      string
	fixos      map[string]string
	rajada     int
	dentro     int
	umaFicha   time.Duration
	retryAfter string
}

var (
	verificacaoGET = rotaLimitada{
		nome: "verificação GET", metodo: http.MethodGet, caminho: "/verify/token-invalido", rajada: 10,
		dentro: http.StatusNotFound, umaFicha: 3 * time.Second, retryAfter: "3",
	}
	verificacaoPOST = rotaLimitada{
		nome: "verificação POST", metodo: http.MethodPost, caminho: "/verify/token-invalido", rajada: 10,
		dentro: http.StatusNotFound, umaFicha: 3 * time.Second, retryAfter: "3",
	}
)

var rotasLimitadas = []rotaLimitada{
	{
		nome: "página pública", metodo: http.MethodGet, caminho: "/p/x", rajada: 60,
		dentro: http.StatusNotFound, umaFicha: 500 * time.Millisecond, retryAfter: "1",
	},
	verificacaoGET,
	verificacaoPOST,
	{
		nome: "código do 2FA", metodo: http.MethodPost, caminho: "/api/v1/auth/totp/verify",
		corpo: `{"challenge":"desafio-invalido","code":"000000"}`,
		fixos: map[string]string{"X-Requested-With": "stillon"}, rajada: 5,
		dentro: http.StatusUnauthorized, umaFicha: 3 * time.Second, retryAfter: "3",
	},
}

// bancada são as rotas registradas de verdade, com o relógio parado: nenhuma
// ficha volta sem Advance.
type bancada struct {
	rotas http.Handler
	clk   *clock.Fake
}

func novaBancada(t *testing.T, ajustar func(*config.Config)) *bancada {
	t.Helper()
	cfg := &config.Config{
		CORSOrigin: "https://app.exemplo.test",
		JWTSecret:  []byte("segredo-jwt-sintetico-so-para-teste"),
		IPHashKey:  []byte("chave-de-ip-sintetica-so-para-teste"),
		MasterKey:  []byte("chave-mestra-de-teste-32-bytes!!"),
		// O prazo de produção: estes testes não pagam Argon2id de verdade.
		RequestTimeout: 15 * time.Second,
	}
	if ajustar != nil {
		ajustar(cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	deps := httpapi.Deps{
		Channels:    notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(nil, clk),
		Clock:       clk,
	}
	return &bancada{rotas: httpapi.Routes(ctx, cfg, deps), clk: clk}
}

// pedir faz uma requisição como se ela tivesse chegado pela conexão indicada.
func (b *bancada) pedir(r rotaLimitada, conexao string, cabecalhos map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(r.metodo, r.caminho, strings.NewReader(r.corpo))
	req.RemoteAddr = conexao
	for nome, valor := range r.fixos {
		req.Header.Set(nome, valor)
	}
	for nome, valor := range cabecalhos {
		req.Header.Set(nome, valor)
	}
	rec := httptest.NewRecorder()
	b.rotas.ServeHTTP(rec, req)
	return rec
}

func (b *bancada) esgotar(t *testing.T, r rotaLimitada, conexao string, cabecalhos map[string]string) {
	t.Helper()
	for i := range r.rajada {
		if rec := b.pedir(r, conexao, cabecalhos); rec.Code != r.dentro {
			t.Fatalf("pedido %d da rajada: %d, queria %d", i+1, rec.Code, r.dentro)
		}
	}
}

func exigir429(t *testing.T, r rotaLimitada, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("além da rajada: %d, queria 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != r.retryAfter {
		t.Errorf("Retry-After = %q, queria %q", got, r.retryAfter)
	}
	if !strings.Contains(rec.Body.String(), `"rate_limited"`) {
		t.Errorf("corpo do 429 sem o código rate_limited: %s", rec.Body.String())
	}
}

func TestLimitedRouteAnswers429PastTheBurst(t *testing.T) {
	for _, r := range rotasLimitadas {
		t.Run(r.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			b.esgotar(t, r, conexaoDireta, nil)
			exigir429(t, r, b.pedir(r, conexaoDireta, nil))
		})
	}
}

func TestLimitedRouteRefillsOnTheControlledClock(t *testing.T) {
	for _, r := range rotasLimitadas {
		t.Run(r.nome+"/um instante antes da ficha", func(t *testing.T) {
			b := novaBancada(t, nil)
			b.esgotar(t, r, conexaoDireta, nil)
			b.clk.Advance(r.umaFicha - time.Millisecond)
			if rec := b.pedir(r, conexaoDireta, nil); rec.Code != http.StatusTooManyRequests {
				t.Errorf("antes da ficha: %d, queria 429", rec.Code)
			}
		})
		t.Run(r.nome+"/quando a ficha chega", func(t *testing.T) {
			b := novaBancada(t, nil)
			b.esgotar(t, r, conexaoDireta, nil)
			b.clk.Advance(r.umaFicha)
			if rec := b.pedir(r, conexaoDireta, nil); rec.Code != r.dentro {
				t.Errorf("com a ficha de volta: %d, queria %d", rec.Code, r.dentro)
			}
		})
	}
}

// As duas formas de o proxy confiável dizer quem é o cliente.
var cabecalhosDoProxy = []struct {
	nome      string
	cabecalho string
	lido      string
}{
	{"último salto do X-Forwarded-For", "", "X-Forwarded-For"},
	{"cabeçalho dedicado", "CF-Connecting-IP", "CF-Connecting-IP"},
}

// Atrás do proxy, um cliente esgotar o balde dele não pode barrar o vizinho:
// sem o IP real, todos dividem o balde do proxy.
func TestLimitedRouteCountsTheRealClientBehindTrustedProxy(t *testing.T) {
	for _, r := range rotasLimitadas {
		for _, c := range cabecalhosDoProxy {
			t.Run(r.nome+"/"+c.nome, func(t *testing.T) {
				b := novaBancada(t, func(cfg *config.Config) {
					cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix(faixaDoProxy)}
					cfg.ClientIPHeader = c.cabecalho
				})
				a := map[string]string{c.lido: clienteA}
				b.esgotar(t, r, conexaoDoProxy, a)
				exigir429(t, r, b.pedir(r, conexaoDoProxy, a))

				vizinho := b.pedir(r, conexaoDoProxy, map[string]string{c.lido: clienteB})
				if vizinho.Code != r.dentro {
					t.Errorf("o vizinho atrás do mesmo proxy: %d, queria %d", vizinho.Code, r.dentro)
				}
			})
		}
	}
}

// Fora da lista de proxies, o cabeçalho é texto de quem pediu: trocar o valor
// a cada pedido não pode comprar um balde novo.
func TestLimitedRouteIgnoresForgedClientIPFromUntrustedConnection(t *testing.T) {
	configuracoes := []struct {
		nome    string
		ajustar func(*config.Config)
	}{
		{"sem lista de proxies", nil},
		{"conexão fora da lista", func(cfg *config.Config) {
			cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix(faixaDoProxy)}
		}},
		{"cabeçalho dedicado, conexão fora da lista", func(cfg *config.Config) {
			cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix(faixaDoProxy)}
			cfg.ClientIPHeader = "CF-Connecting-IP"
		}},
	}
	for _, r := range rotasLimitadas {
		for _, c := range configuracoes {
			t.Run(r.nome+"/"+c.nome, func(t *testing.T) {
				b := novaBancada(t, c.ajustar)
				var ultimo *httptest.ResponseRecorder
				for i := range r.rajada + 1 {
					forjado := fmt.Sprintf("198.51.100.%d", i+10)
					ultimo = b.pedir(r, conexaoDireta, map[string]string{
						"X-Forwarded-For": forjado, "CF-Connecting-IP": forjado,
					})
				}
				exigir429(t, r, ultimo)
			})
		}
	}
}

// As duas rotas de /verify ficam atrás do mesmo RateLimit: quem esgota uma não
// pode ganhar um balde novo só por trocar o método.
func TestVerifyGetAndPostShareOneLimiter(t *testing.T) {
	casos := []struct{ esgotada, outra rotaLimitada }{
		{verificacaoGET, verificacaoPOST},
		{verificacaoPOST, verificacaoGET},
	}
	for _, c := range casos {
		t.Run(c.esgotada.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			b.esgotar(t, c.esgotada, conexaoDireta, nil)
			exigir429(t, c.outra, b.pedir(c.outra, conexaoDireta, nil))
		})
	}
}

// corpoSemTamanho esconde o tipo do leitor: sem *strings.Reader, o httptest não
// preenche o ContentLength, como num envio chunked.
type corpoSemTamanho struct{ io.Reader }

// O POST de /verify é um formulário sem campos, com teto de 1 KiB. O teto vale
// para os bytes lidos: quem manda chunked não declara tamanho nenhum.
func TestVerifyPostBodyCeiling(t *testing.T) {
	formatos := []struct {
		nome            string
		corpo           func(string) io.Reader
		tamanhoEsperado func(int) int64
	}{
		{
			"tamanho declarado",
			func(s string) io.Reader { return strings.NewReader(s) },
			func(n int) int64 { return int64(n) },
		},
		{
			"tamanho desconhecido",
			func(s string) io.Reader { return corpoSemTamanho{strings.NewReader(s)} },
			func(int) int64 { return -1 },
		},
	}
	casos := []struct {
		nome    string
		tamanho int
		quer    int
	}{
		{"no teto", 1 << 10, http.StatusNotFound},
		{"um byte acima do teto", 1<<10 + 1, http.StatusRequestEntityTooLarge},
	}
	for _, f := range formatos {
		for _, c := range casos {
			t.Run(f.nome+"/"+c.nome, func(t *testing.T) {
				b := novaBancada(t, nil)
				req := httptest.NewRequest(http.MethodPost, "/verify/token-invalido", f.corpo(strings.Repeat("x", c.tamanho)))
				if want := f.tamanhoEsperado(c.tamanho); req.ContentLength != want {
					t.Fatalf("ContentLength = %d, queria %d: o caso não testa o formato do nome", req.ContentLength, want)
				}
				req.RemoteAddr = conexaoDireta
				rec := httptest.NewRecorder()
				b.rotas.ServeHTTP(rec, req)
				if rec.Code != c.quer {
					t.Errorf("corpo de %d bytes: %d, queria %d", c.tamanho, rec.Code, c.quer)
				}
			})
		}
	}
}

// O token tem o formato aceito, então o serviço iria ao pool, que nesta bancada
// é nil: o 413 só sai se o corpo for lido antes da confirmação.
func TestVerifyPostBodyOverCeilingStopsBeforeConfirming(t *testing.T) {
	b := novaBancada(t, nil)
	tokenBemFormado := strings.Repeat("A", 43)
	req := httptest.NewRequest(http.MethodPost, "/verify/"+tokenBemFormado, strings.NewReader(strings.Repeat("x", 1<<10+1)))
	req.RemoteAddr = conexaoDireta
	rec := httptest.NewRecorder()

	b.rotas.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo acima do teto: %d, queria 413", rec.Code)
	}
}

func TestVerifyPostBodyReadFailureIs400(t *testing.T) {
	b := novaBancada(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/verify/token-invalido", iotest.ErrReader(io.ErrUnexpectedEOF))
	req.RemoteAddr = conexaoDireta
	rec := httptest.NewRecorder()

	b.rotas.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("falha de leitura do corpo: %d, queria 400", rec.Code)
	}
}
