package httpapi_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// senhaDoBancoFora nunca é usada para conectar: a porta está fechada. Ela
// entra na DSN para o teste provar que não sai em resposta nem em log.
const senhaDoBancoFora = "SENHA-DO-BANCO-FORA"

// apiSemBanco é a API real, com todas as cadeias, sobre um pool que aponta para
// uma porta fechada: toda consulta falha, sem Docker. É o "erro forçado" da
// regra, em toda rota de uma vez.
func apiSemBanco(t *testing.T) (*httptest.Server, *clock.Fake) {
	t.Helper()
	escuta, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	porta := escuta.Addr().(*net.TCPAddr).Port
	if err := escuta.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, fmt.Sprintf(
		"postgres://stillon:%s@127.0.0.1:%d/stillon?connect_timeout=1&pool_max_conns=2", senhaDoBancoFora, porta))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	clk := clock.NewFake(time.Now().UTC().Truncate(time.Hour))
	cfg := &config.Config{
		CORSOrigin: "https://app.exemplo.com", JWTSecret: testJWTSecret, IPHashKey: testIPHashKey, MasterKey: testMasterKey,
		RequestTimeout: 15 * time.Second,
	}
	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	segundoFator := auth.NewTOTP(pool, box, "StillOn")
	store := auth.NewPGStore(pool)
	srv := httptest.NewServer(httpapi.Routes(ctx, cfg, httpapi.Deps{
		Store: store, Sessions: auth.NewManager(store, clk, cfg.IPHashKey), TOTP: segundoFator,
		Accounts:    account.NewService(pool, segundoFator, clk, cfg.IPHashKey),
		Targets:     targets.NewService(pool, box, clk),
		Incidents:   history.NewService(pool, clk),
		Channels:    notify.NewChannelService(pool, box, clk, &discordFalso{}, &carteiroFalso{}, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(pool, clk),
		Pings:       heartbeat.NewPingService(pool, clk),
		Heartbeats:  heartbeat.NewService(pool, clk),
		Clock:       clk, Ready: pool.Ping,
	}))
	t.Cleanup(srv.Close)
	return srv, clk
}

// tokenCanario tem o formato dos tokens de ping e de verificação, para que
// passe da checagem de formato e chegue ao banco.
func tokenCanario(t *testing.T) string {
	t.Helper()
	aleatorio := make([]byte, 32)
	if _, err := rand.Read(aleatorio); err != nil {
		t.Fatal(err)
	}
	return "CANARY" + base64.RawURLEncoding.EncodeToString(aleatorio)[6:]
}

var marcasDeDetalheInterno = []string{
	senhaDoBancoFora, "pgx", "pgconn", "SQLSTATE", "SELECT ", "dial ", "connection refused", "127.0.0.1",
	"goroutine", ".go:", "internal/", "panic", "stillon@",
}

type cenarioSemBanco struct {
	srv                   *httptest.Server
	acesso, canario, slug string
	tokenDeURL            string
	corpos                map[string]string
	proibidos             []string
}

// O banco fora do ar é o erro interno mais comum em produção: nenhuma rota pode
// responder com o motivo, com canário recebido ou com cookie novo, e o log,
// que recebe o erro real, não pode levar o que veio do cliente.
func TestBancoForaNaoVazaNadaEmNenhumaRota(t *testing.T) {
	log := capturarLog(t)
	srv, clk := apiSemBanco(t)
	usuario := uuid.New()
	acesso, err := auth.NewAccessToken(testJWTSecret, usuario, uuid.New(), clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	desafio, err := auth.NewChallengeToken(testJWTSecret, usuario, auth.PasswordStamp("x"), clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	canario := "CANARY-" + uuid.NewString()
	c := cenarioSemBanco{
		srv: srv, acesso: acesso, canario: canario, slug: "canary-" + strings.ToLower(uuid.NewString()[:23]),
		tokenDeURL: tokenCanario(t),
		corpos: map[string]string{
			"POST /api/v1/auth/login":       `{"email":"` + canario + `@exemplo.com","password":"` + canario + `"}`,
			"POST /api/v1/auth/password":    `{"current_password":"` + canario + `","new_password":"` + canario + `","totp_code":"` + canario + `"}`,
			"POST /api/v1/auth/totp/verify": `{"challenge":"` + desafio + `","code":"` + canario + `"}`,
		},
	}
	c.proibidos = append([]string{c.canario, c.tokenDeURL, c.slug}, marcasDeDetalheInterno...)

	esperados := map[string]respostaSemBanco{
		"GET /healthz":         {status: http.StatusOK},
		"GET /readyz":          {status: http.StatusServiceUnavailable, codigo: "unavailable"},
		"GET /p/{slug}":        {status: http.StatusServiceUnavailable, pagina: true},
		"GET /verify/{token}":  {status: http.StatusInternalServerError, pagina: true},
		"POST /verify/{token}": {status: http.StatusInternalServerError, pagina: true},
	}
	padrao := respostaSemBanco{status: http.StatusInternalServerError, codigo: "internal_error"}

	rotas := rotasRegistradas(t)
	for _, rota := range rotas {
		clk.Advance(time.Second) // devolve fichas aos limitadores: ver esperarVagaNoLimite
		esperado, ok := esperados[rota]
		if !ok {
			esperado = padrao
		}
		c.conferir(t, rota, c.acesso, esperado)
	}
	// A mesma volta com o canário no Authorization: quem recebe credencial
	// inválida nunca a devolve nem a registra. O status aqui varia por rota, e
	// já é o assunto de TestEveryProtectedRouteRejectsForgedToken.
	for _, rota := range rotas {
		clk.Advance(time.Second)
		c.conferir(t, rota, c.canario, respostaSemBanco{})
	}

	registrado := log.String()
	for _, proibido := range []string{c.canario, c.tokenDeURL, c.slug, senhaDoBancoFora} {
		if strings.Contains(registrado, proibido) {
			t.Errorf("o log trouxe %q:\n%s", proibido, registrado)
		}
	}
	// O erro real precisa estar lá, ou os canários ausentes não provam nada.
	for _, operacao := range []string{
		"conferir sessão", "buscar usuário", "trocar senha", "rotacionar sessão", "revogar sessão",
		"registrar ping", "status page pública", "verificar token", "confirmar token",
	} {
		if !strings.Contains(registrado, operacao) {
			t.Errorf("o log não registrou a falha de %q:\n%.2000s", operacao, registrado)
		}
	}
}

// respostaSemBanco é o que uma rota deve responder. status zero: só confere o
// vazamento. pagina: resposta HTML de texto fixo, fora do envelope.
type respostaSemBanco struct {
	status int
	codigo string
	pagina bool
}

var curingaDeRota = regexp.MustCompile(`\{[^}]+\}`)

func (c cenarioSemBanco) caminho(modelo string) string {
	return curingaDeRota.ReplaceAllStringFunc(modelo, func(curinga string) string {
		switch curinga {
		case "{slug}":
			return c.slug
		case "{token}":
			return c.tokenDeURL
		case "{exit_code}":
			return "3"
		}
		return uuid.NewString()
	})
}

func (c cenarioSemBanco) conferir(t *testing.T, rota, credencial string, esperado respostaSemBanco) {
	t.Helper()
	metodo, modelo, _ := strings.Cut(rota, " ")
	corpoDoPedido := "{}"
	if corpo, ok := c.corpos[rota]; ok {
		corpoDoPedido = corpo
	}
	req, err := http.NewRequest(metodo, c.srv.URL+c.caminho(modelo)+"?marca="+c.canario, strings.NewReader(corpoDoPedido))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	req.Header.Set("Authorization", "Bearer "+credencial)
	req.Header.Set("Cookie", "stillon_refresh="+c.canario+"; stillon_dispositivo="+c.canario)
	req.Header.Set("User-Agent", c.canario)
	req.Header.Set("Referer", "https://exemplo.com/?t="+c.canario)
	req.Header.Set("X-Forwarded-For", c.canario)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lido, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	corpo := string(lido)

	for _, proibido := range c.proibidos {
		if strings.Contains(corpo, proibido) {
			t.Errorf("%s: o corpo trouxe %q: %s", rota, proibido, corpo)
		}
		if strings.Contains(fmt.Sprint(resp.Header), proibido) {
			t.Errorf("%s: os cabeçalhos trouxeram %q: %v", rota, proibido, resp.Header)
		}
	}
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
		t.Errorf("%s: com o banco fora a resposta mexeu nos cookies: %v", rota, cookies)
	}
	if esperado.status == 0 {
		return
	}
	if resp.StatusCode != esperado.status {
		t.Errorf("%s deu %d %s, queria %d", rota, resp.StatusCode, corpo, esperado.status)
		return
	}
	if esperado.codigo != "" {
		conferirEnvelopeFechado(t, rota, corpo, esperado.codigo, resp.Header.Get("X-Trace-Id"))
	}
}

// conferirEnvelopeFechado exige o envelope sem nenhuma chave a mais: é o que
// impede um campo "detail" novo de carregar o erro real.
func conferirEnvelopeFechado(t *testing.T, rota, corpo, codigo, trace string) {
	t.Helper()
	var fora map[string]json.RawMessage
	if err := json.Unmarshal([]byte(corpo), &fora); err != nil {
		t.Errorf("%s: corpo fora do envelope: %v: %s", rota, err, corpo)
		return
	}
	if got := slices.Sorted(maps.Keys(fora)); !slices.Equal(got, []string{"error", "trace_id"}) {
		t.Errorf("%s: chaves %v, queria error e trace_id: %s", rota, got, corpo)
	}
	var erro map[string]json.RawMessage
	if err := json.Unmarshal(fora["error"], &erro); err != nil {
		t.Errorf("%s: error fora do formato: %v: %s", rota, err, corpo)
		return
	}
	if got := slices.Sorted(maps.Keys(erro)); !slices.Equal(got, []string{"code", "message"}) {
		t.Errorf("%s: error tem %v, queria code e message: %s", rota, got, corpo)
	}
	if string(erro["code"]) != `"`+codigo+`"` {
		t.Errorf("%s: code %s, queria %s", rota, erro["code"], codigo)
	}
	if string(fora["trace_id"]) != `"`+trace+`"` || trace == "" {
		t.Errorf("%s: trace_id %s difere do cabeçalho %q", rota, fora["trace_id"], trace)
	}
}
