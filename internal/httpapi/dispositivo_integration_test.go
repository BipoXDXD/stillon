package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
)

// O login e o /totp/verify depois do Plano 10: a tentativa é reservada antes
// de verificar (A2), o navegador que completou um login recebe o cookie de
// dispositivo e tem um contador próprio (A3), e o logout não mente (A9).

const nomeDoCookieDeDispositivo = "vigilume_dispositivo"

func cookieDeDispositivo(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == nomeDoCookieDeDispositivo {
			return ck
		}
	}
	return nil
}

// apiAtrasDeProxy confia no X-Forwarded-For do 127.0.0.0/8: cada tentativa
// pode vir de uma "origem" própria, e o limitador por IP do login (burst 5)
// não se mete no que o teste mede.
func apiAtrasDeProxy(t *testing.T) *api {
	t.Helper()
	return newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	})
}

var origens atomic.Int32

// novaOrigem devolve um IP de documentação ainda não usado no processo.
func novaOrigem() string {
	n := origens.Add(1)
	return "198.51." + strconv.Itoa(int(n/250)%250) + "." + strconv.Itoa(int(n%250)+1)
}

// postDe é o post() com o X-Forwarded-For de uma origem nova. Sem *testing.T
// no caminho de erro: roda também em goroutine.
func postDe(c *http.Client, url string, body any) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, jsonBodySemT(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "vigilume")
	req.Header.Set("X-Forwarded-For", novaOrigem())
	return c.Do(req)
}

func jsonBodySemT(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// mustURL é o url.Parse de um endereço do próprio teste.
func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// entrarDe faz login de uma origem nova e devolve status e code do envelope.
func entrarDe(t *testing.T, a *api, c *http.Client, senha string) (int, string) {
	t.Helper()
	resp, err := postDe(c, a.srv.URL+"/api/v1/auth/login", map[string]string{"email": testEmail, "password": senha})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, codigoDoErro(t, resp)
}

// O cookie de dispositivo só sai de um login COMPLETO: do login sem 2FA e do
// /totp/verify, e não do login que devolve o desafio nem da renovação. Os
// atributos são os do refresh (HttpOnly, SameSite=Strict, Path /api/v1/auth;
// sem Secure em http) e a validade é de 180 dias.
func TestDeviceCookieComesOnlyWithACompleteLogin(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	resp := login(t, a, c, testEmail, testPass)
	resp.Body.Close()
	ck := cookieDeDispositivo(resp)
	if resp.StatusCode != http.StatusOK || ck == nil {
		t.Fatalf("login sem 2FA: %d, cookie de dispositivo %v; queria 200 com o cookie", resp.StatusCode, ck)
	}
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/v1/auth" ||
		ck.MaxAge != int((180*24*time.Hour)/time.Second) || ck.Secure {
		t.Errorf("atributos do cookie de dispositivo: %+v", ck)
	}

	renovacao := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	renovacao.Body.Close()
	if renovacao.StatusCode != http.StatusOK || cookieDeDispositivo(renovacao) != nil {
		t.Errorf("renovação: %d, cookie de dispositivo %v; queria 200 sem cookie novo",
			renovacao.StatusCode, cookieDeDispositivo(renovacao))
	}

	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Second)
	outro := clientWithJar(t)
	passo1 := login(t, a, outro, testEmail, testPass)
	var d struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(passo1.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	passo1.Body.Close()
	if cookieDeDispositivo(passo1) != nil {
		t.Error("o login que devolve o desafio já pôs o cookie de dispositivo")
	}
	passo2 := post(t, outro, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": d.Challenge, "code": codigoDe(t, a, segredo)})
	passo2.Body.Close()
	if passo2.StatusCode != http.StatusOK || cookieDeDispositivo(passo2) == nil {
		t.Errorf("/totp/verify: %d, cookie de dispositivo %v; queria 200 com o cookie",
			passo2.StatusCode, cookieDeDispositivo(passo2))
	}
}

// Com PUBLIC_BASE_URL https, o cookie de dispositivo sai Secure, como o de
// refresh.
func TestDeviceCookieIsSecureWithHTTPS(t *testing.T) {
	a := newAPIWith(t, func(cfg *config.Config) { cfg.PublicBaseURL = "https://vigilume.exemplo.com" })
	resp := login(t, a, http.DefaultClient, testEmail, testPass)
	resp.Body.Close()
	if ck := cookieDeDispositivo(resp); ck == nil || !ck.Secure {
		t.Errorf("com PUBLIC_BASE_URL https, cookie de dispositivo %+v; queria Secure", ck)
	}
}

// A3 (Plano 10): um anônimo que sabe o e-mail tranca a conta com cinco senhas
// erradas, e sem o cookie ninguém entra, nem com a senha certa. O navegador
// do dono, que já completou um login, entra: o cookie de dispositivo válido
// para a conta tira o login do bloqueio da conta. Um cookie forjado (com um MAC
// que o servidor não assinou) não vale. O bloqueio da conta continua lá.
func TestDeviceCookieLetsTheOwnerInPastTheAccountLock(t *testing.T) {
	a := apiAtrasDeProxy(t)
	dono := clientWithJar(t)
	if st, codigo := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Fatalf("primeiro login do dono: %d %s", st, codigo)
	}

	anonimo := clientWithJar(t)
	for i := range auth.LockThreshold {
		if st, _ := entrarDe(t, a, anonimo, "chute-"+strconv.Itoa(i)+"-com-mais-de-15"); st != http.StatusUnauthorized {
			t.Fatalf("chute %d do anônimo: %d", i+1, st)
		}
	}
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueada {
		t.Fatalf("depois dos chutes: %d falhas, bloqueada=%v; o cenário não trancou a conta", falhas, bloqueada)
	}
	if st, codigo := entrarDe(t, a, anonimo, testPass); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("senha certa sem o cookie, conta trancada: %d %s, queria 401 invalid_credentials", st, codigo)
	}

	// Um cookie forjado: o formato de um de verdade (16 bytes de id, 8 de
	// prazo, 32 de MAC), com um MAC que o servidor não assinou.
	forjador := clientWithJar(t)
	forjado := &http.Cookie{Name: nomeDoCookieDeDispositivo, Value: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 56)), Path: "/api/v1/auth"}
	forjador.Jar.SetCookies(mustURL(t, a.srv.URL+"/api/v1/auth"), []*http.Cookie{forjado})
	if st, codigo := entrarDe(t, a, forjador, testPass); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("cookie forjado: %d %s, queria 401 invalid_credentials", st, codigo)
	}

	if st, codigo := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Errorf("o dono com o cookie de dispositivo, conta trancada: %d %s, queria 200", st, codigo)
	}
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueada {
		t.Errorf("o login do dispositivo mexeu no bloqueio da conta: %d falhas, bloqueada=%v", falhas, bloqueada)
	}
}

// As falhas de quem apresenta o cookie contam no DISPOSITIVO, e não na conta.
// Cinco falhas fazem o cookie ser ignorado por 15 min: nesse período o login
// segue pelo bloqueio da conta (trancada, nem a senha certa entra;
// destravada, entra); passado ele, o cookie volta a valer.
func TestDeviceFailuresCountOnTheDeviceOnly(t *testing.T) {
	a := apiAtrasDeProxy(t)
	dono := clientWithJar(t)
	if st, _ := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Fatalf("primeiro login do dono: %d", st)
	}
	for i := 1; i < auth.LockThreshold; i++ {
		if st, _ := entrarDe(t, a, dono, "errada-"+strconv.Itoa(i)+"-com-mais-de-15"); st != http.StatusUnauthorized {
			t.Fatalf("falha %d do dono: %d", i, st)
		}
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas na conta depois de 4 falhas com o cookie, queria 0", n)
	}

	bloquearConta(t, a)
	if st, _ := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Fatalf("o dono com 4 falhas no dispositivo e a conta trancada: %d, queria 200", st)
	}
	for i := 1; i <= auth.LockThreshold; i++ {
		entrarDe(t, a, dono, "errada-de-novo-"+strconv.Itoa(i)+"-longa")
	}
	if st, codigo := entrarDe(t, a, dono, testPass); st != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("depois de 5 falhas no dispositivo, conta trancada: %d %s, queria 401 invalid_credentials (cookie ignorado)", st, codigo)
	}

	// Ignorado, o cookie é como nenhum: com a conta destravada, o login segue
	// pelo bloqueio da conta e entra.
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	if st, codigo := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Errorf("cookie ignorado, conta destravada: %d %s, queria 200 pelo bloqueio da conta", st, codigo)
	}

	bloquearConta(t, a)
	a.clk.Advance(auth.LockDuration)
	if st, codigo := entrarDe(t, a, dono, testPass); st != http.StatusOK {
		t.Errorf("passados 15 min do castigo do dispositivo, conta trancada: %d %s, queria 200", st, codigo)
	}
}

// O cookie vale também no segundo passo: com a conta trancada, o dono com o
// cookie recebe o desafio e fecha o login no /totp/verify. O mesmo desafio,
// apresentado sem o cookie, cai no bloqueio da conta.
//
// O cookie do login do cadastro não serve: ligar o 2FA muda o carimbo das
// credenciais (I1 da revisão do Plano 10). O dono entra de novo, com o 2FA,
// e é esse login completo que põe o cookie.
func TestDeviceCookieAlsoCoversTheSecondFactor(t *testing.T) {
	a := apiAtrasDeProxy(t)
	dono := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Second)
	if st := entrarComFator(t, a, dono, segredo); st != http.StatusOK {
		t.Fatalf("login completo do dono com o 2FA: %d", st)
	}
	bloquearConta(t, a)
	a.clk.Advance(time.Second)

	resp, err := postDe(dono, a.srv.URL+"/api/v1/auth/login", map[string]string{"email": testEmail, "password": testPass})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Challenge string `json:"challenge"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || d.Challenge == "" {
		t.Fatalf("login do dono com o cookie, conta trancada: %d, sem desafio", resp.StatusCode)
	}

	semCookie, err := postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": d.Challenge, "code": codigoDe(t, a, segredo)})
	if err != nil {
		t.Fatal(err)
	}
	codigo := codigoDoErro(t, semCookie)
	semCookie.Body.Close()
	if semCookie.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("verify sem o cookie, conta trancada: %d %s, queria 401 invalid_credentials", semCookie.StatusCode, codigo)
	}

	errado, err := postDe(dono, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": d.Challenge, "code": codigoErradoPara(t, a, segredo)})
	if err != nil {
		t.Fatal(err)
	}
	errado.Body.Close()
	if falhas, _ := estadoDoBloqueio(t, a); falhas != auth.LockThreshold {
		t.Errorf("o código errado com o cookie contou na conta: %d falhas, queria %d", falhas, auth.LockThreshold)
	}

	certo, err := postDe(dono, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": d.Challenge, "code": codigoDe(t, a, segredo)})
	if err != nil {
		t.Fatal(err)
	}
	certo.Body.Close()
	if certo.StatusCode != http.StatusOK {
		t.Errorf("verify com o cookie, conta trancada: %d, queria 200", certo.StatusCode)
	}
}

// A2 (Plano 10): oito senhas erradas simultâneas, cada uma de uma origem,
// contam exatamente LockThreshold falhas: a quinta reserva trava a conta, e
// as seguintes são recusadas sem contar. Antes, todas liam a conta
// destravada, verificavam e só então gravavam a falha — o limite de cinco
// virava o número de origens.
//
// A linha do usuário fica travada até as oito chegarem ao UPDATE (a reserva,
// agora; a gravação da falha, antes), para que a corrida aconteça sempre, e
// não só quando o agendador ajuda. O teto do Argon2id sobe para 16: a vaga é
// tomada antes da reserva, e com o padrão (até 4) as outras esperariam a
// vaga, e não a linha.
func TestParallelLoginGuessesStopAtTheLock(t *testing.T) {
	a := newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		cfg.Argon2MaxConcurrent = 16
	})
	dispararContraALinhaTravada(t, a, 8, func() (*http.Response, error) {
		return postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/login",
			map[string]string{"email": testEmail, "password": "chute-simultaneo-longo"})
	})
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueada {
		t.Errorf("8 senhas erradas simultâneas: %d falhas (bloqueada=%v), queria exatamente %d",
			falhas, bloqueada, auth.LockThreshold)
	}
}

// O mesmo para o segundo fator, que é onde a corrida mais valia: quem tem a
// senha dispara códigos simultâneos com o mesmo desafio.
func TestParallelSecondFactorGuessesStopAtTheLock(t *testing.T) {
	a := apiAtrasDeProxy(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Second)
	desafio := desafioAtual(t, a, clientWithJar(t))
	errado := codigoErradoPara(t, a, segredo)

	dispararContraALinhaTravada(t, a, 8, func() (*http.Response, error) {
		return postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": errado})
	})
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueada {
		t.Errorf("8 códigos errados simultâneos: %d falhas (bloqueada=%v), queria exatamente %d",
			falhas, bloqueada, auth.LockThreshold)
	}
}

// dispararContraALinhaTravada trava a linha do usuário, faz n requisições ao
// mesmo tempo, espera as n pararem na trava, solta, e exige 401
// invalid_credentials de todas. n cabe no pool de 10 conexões com a trava e a
// consulta de espera.
func dispararContraALinhaTravada(t *testing.T, a *api, n int, fazer func() (*http.Response, error)) {
	t.Helper()
	soltar := travarLinhaDoUsuario(t, a)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codigos := map[string]int{}
	for range n {
		wg.Go(func() {
			resp, err := fazer()
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			var env envelope
			_ = json.NewDecoder(resp.Body).Decode(&env)
			mu.Lock()
			codigos[strconv.Itoa(resp.StatusCode)+" "+env.Error.Code]++
			mu.Unlock()
		})
	}
	chegaram := esperarBloqueados(t, a.pool, n)
	soltar()
	wg.Wait()
	if !chegaram {
		t.Fatalf("as %d requisições não pararam juntas na trava da linha (respostas %v)", n, codigos)
	}
	if codigos["401 invalid_credentials"] != n {
		t.Errorf("respostas %v, queria %d × 401 invalid_credentials", codigos, n)
	}
}

// storeQueFalha é o Store de verdade com a transação quebrada quando armado:
// o banco caiu no meio do logout.
type storeQueFalha struct {
	inner  auth.Store
	falhar *atomic.Bool
}

func (s storeQueFalha) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	if s.falhar.Load() {
		return errors.New("banco fora do ar")
	}
	return s.inner.InTx(ctx, fn)
}

// A9 (Plano 10): se a revogação falha, o logout responde 500 internal_error e
// NÃO apaga o cookie; a família continua viva (é a verdade), e o cliente pode
// tentar de novo. A perna positiva: com o banco de volta, o mesmo logout é 204,
// apaga o cookie e derruba a família.
func TestLogoutThatCannotRevokeKeepsTheCookie(t *testing.T) {
	falhar := &atomic.Bool{}
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Sessions = auth.NewManager(storeQueFalha{inner: d.Store, falhar: falhar}, d.Clock, testIPHashKey)
	})
	c := clientWithJar(t)
	resp := login(t, a, c, testEmail, testPass)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}

	falhar.Store(true)
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	codigo := codigoDoErro(t, sair)
	sair.Body.Close()
	if sair.StatusCode != http.StatusInternalServerError || codigo != "internal_error" {
		t.Errorf("logout sem conseguir revogar: %d %s, queria 500 internal_error", sair.StatusCode, codigo)
	}
	if ck := refreshCookie(sair); ck != nil {
		t.Errorf("o logout que falhou mexeu no cookie de refresh: %+v", ck)
	}
	if n := familiasVivas(t, a); n != 1 {
		t.Fatalf("%d famílias vivas depois do logout que falhou, queria 1", n)
	}

	falhar.Store(false)
	sair = post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()
	if ck := refreshCookie(sair); sair.StatusCode != http.StatusNoContent || ck == nil || ck.MaxAge >= 0 {
		t.Errorf("logout com o banco de volta: %d, cookie %+v; queria 204 apagando o cookie", sair.StatusCode, ck)
	}
	if n := familiasVivas(t, a); n != 0 {
		t.Errorf("%d famílias vivas depois do logout, queria 0", n)
	}
}

// A reserva vem ANTES do código (A2): com a conta trancada, um código de
// recuperação certo no /totp/verify é recusado sem ser gasto. Verificar antes
// de reservar queimaria o código — que não se renova — para devolver 401.
func TestLockedAccountDoesNotBurnARecoveryCode(t *testing.T) {
	a := newAPI(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	a.clk.Advance(time.Second)
	c := clientWithJar(t)
	desafio := desafioAtual(t, a, c)
	bloquearConta(t, a)

	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": a.recuperacao[0]})
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("código de recuperação com a conta trancada: %d %s, queria 401 invalid_credentials", resp.StatusCode, codigo)
	}
	if _, _, codigos, _ := estadoDoSegundoFator(t, a); codigos != 10 {
		t.Errorf("%d códigos de recuperação depois da recusa, queria 10: o código foi gasto", codigos)
	}
}

// entrarComFator faz o login em dois passos com o cliente dado, de uma origem
// nova, e devolve o status do /totp/verify (ou o do login, se ele não deu o
// desafio). Deixa o relógio 30 s adiante: o timestep usado fica para trás.
func entrarComFator(t *testing.T, a *api, c *http.Client, segredo string) int {
	t.Helper()
	resp, err := postDe(c, a.srv.URL+"/api/v1/auth/login", map[string]string{"email": testEmail, "password": testPass})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Challenge string `json:"challenge"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || d.Challenge == "" {
		return resp.StatusCode
	}
	v, err := postDe(c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": d.Challenge, "code": codigoDe(t, a, segredo)})
	if err != nil {
		t.Fatal(err)
	}
	v.Body.Close()
	a.clk.Advance(30 * time.Second)
	return v.StatusCode
}
