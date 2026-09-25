package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// A política pede 15 caracteres; testPass tem 22.
const senhaNova = "a-senha-nova-bem-comprida"

// entrar faz login num cliente próprio (um cookie jar = um aparelho) e devolve
// o cliente e o access token. Cada chamada abre uma família de sessão.
//
// Atenção ao orçamento: o login e POST /auth/password dividem o balde de 10/min
// com burst 5 por IP, e todo teste daqui roda do mesmo 127.0.0.1.
func entrar(t *testing.T, a *api) (*http.Client, string) {
	t.Helper()
	c := clientWithJar(t)
	resp := login(t, a, c, testEmail, testPass)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", resp.StatusCode)
	}
	return c, accessDe(t, resp)
}

func trocarSenha(t *testing.T, a *api, token string, corpo map[string]string) *http.Response {
	t.Helper()
	return postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/password", token, corpo)
}

// renova diz se o refresh do aparelho ainda vale.
func renova(t *testing.T, a *api, c *http.Client) bool {
	t.Helper()
	resp := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// senhaGravadaE confere o hash gravado sem passar pelo login, que gastaria o
// balde compartilhado.
func senhaGravadaE(t *testing.T, a *api, senha string) bool {
	t.Helper()
	var hash string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, a.userID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, _, err := auth.VerifyPassword(context.Background(), hash, senha)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// codigoDoErro devolve o code do envelope de erro. O status vem antes do
// corpo: uma resposta de sucesso (um 204, por exemplo) não tem envelope, e
// decodificá-la dava "EOF" no lugar da mensagem do teste, que mostra o status.
// Corpo que não é envelope (o 404 em texto do ServeMux, sem rota) volta como
// "(sem envelope)", e não casa com code nenhum.
func codigoDoErro(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp.StatusCode < 400 {
		return ""
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "(sem envelope)"
	}
	return env.Error.Code
}

func getComToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type conta struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

func lerMe(t *testing.T, a *api, token string) conta {
	t.Helper()
	resp := getComToken(t, a.srv.URL+"/api/v1/me", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me devolveu %d", resp.StatusCode)
	}
	var c conta
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// totp_enabled é o fator CONFIRMADO: o cadastro iniciado e não confirmado não
// liga nada, e é essa a perna que distingue as duas colunas.
func TestMeReturnsTheAccount(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	c := lerMe(t, a, token)
	if c.ID != a.userID.String() || c.Email != testEmail || c.TOTPEnabled || c.CreatedAt.IsZero() {
		t.Fatalf("/me = %+v", c)
	}

	er := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token, map[string]string{"password": testPass})
	er.Body.Close()
	if er.StatusCode != http.StatusOK {
		t.Fatalf("enroll devolveu %d", er.StatusCode)
	}
	if lerMe(t, a, token).TOTPEnabled {
		t.Error("totp_enabled ligou com o cadastro só iniciado")
	}

	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET totp_confirmed_at = now() WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}
	if !lerMe(t, a, token).TOTPEnabled {
		t.Error("totp_enabled desligado com o fator confirmado")
	}
}

// O coração da troca: três aparelhos logados; troca-se a senha no primeiro. O
// primeiro continua renovando; os outros dois não; a sessão de outro usuário
// não é tocada.
func TestChangePasswordKeepsCurrentRevokesOthers(t *testing.T) {
	a := newAPI(t)
	atual, token := entrar(t, a)
	b, _ := entrar(t, a)
	c, _ := entrar(t, a)

	outro := outroUsuario(t, a)
	mgr := auth.NewManager(auth.NewPGStore(a.pool), a.clk, testIPHashKey)
	doOutro, _, err := mgr.Issue(context.Background(), outro, auth.PasswordStamp("x"), auth.Meta{IP: "203.0.113.7"})
	if err != nil {
		t.Fatal(err)
	}

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("troca devolveu %d, queria 204", resp.StatusCode)
	}

	if !renova(t, a, atual) {
		t.Error("a sessão atual caiu junto: a troca tinha de mantê-la")
	}
	if renova(t, a, b) || renova(t, a, c) {
		t.Error("uma sessão de outro aparelho sobreviveu à troca de senha")
	}
	if _, _, err := mgr.Rotate(context.Background(), doOutro, auth.Meta{IP: "203.0.113.7"}); err != nil {
		t.Errorf("a sessão de outro usuário caiu: %v", err)
	}
	if !senhaGravadaE(t, a, senhaNova) || senhaGravadaE(t, a, testPass) {
		t.Error("o hash gravado não é o da senha nova")
	}

	var auditadas int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.password_changed' AND ip_hash IS NOT NULL`,
		a.userID).Scan(&auditadas); err != nil {
		t.Fatal(err)
	}
	if auditadas != 1 {
		t.Errorf("%d registros user.password_changed com ip_hash, queria 1", auditadas)
	}
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	outro, _ := entrar(t, a)

	resp := trocarSenha(t, a, token, map[string]string{"current_password": "nao-e-a-senha-atual", "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigoDoErro(t, resp) != "invalid_credentials" {
		t.Fatalf("status %d, queria 401 invalid_credentials", resp.StatusCode)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a senha mudou com a senha atual errada")
	}
	// A senha errada conta no contador da família (A3 do Plano 10), e não no
	// bloqueio da conta: quem chuta a senha com a sessão não tranca o dono.
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas no bloqueio da conta, queria 0 (A3)", n)
	}
	if !renova(t, a, outro) {
		t.Error("a troca recusada revogou as outras sessões")
	}
}

// A conta bloqueada NÃO impede o dono logado de trocar a senha (A3 do Plano
// 10): o bloqueio é do login. Antes, um anônimo que travasse o login a cada 15
// min tirava do dono, junto, a troca de senha pelo painel. A troca com as
// provas certas passa e zera o bloqueio, como sempre zerou (SwapUserHash).
func TestChangePasswordIgnoresTheAccountLock(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	bloquearConta(t, a)

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("troca com a conta bloqueada: %d %s, queria 204", resp.StatusCode, codigo)
	}
	if !senhaGravadaE(t, a, senhaNova) {
		t.Error("a troca aceita não gravou a senha nova")
	}
	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != 0 || bloqueado {
		t.Errorf("depois da troca: %d falhas, bloqueada=%v; queria o bloqueio zerado", falhas, bloqueado)
	}
}

// Com o segundo fator ligado, a senha sozinha não troca nada: sem código e com
// código errado é o 401 genérico. A falha conta na família (A3 do Plano 10), e
// não no bloqueio da conta.
func TestChangePasswordRequiresTOTP(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	token := accessTokenDaSessao(t, a, c)

	// invalid_credentials, e nunca unauthorized: o painel renova a sessão em
	// unauthorized, e um código digitado errado viraria logout (D24).
	for nome, corpo := range map[string]map[string]string{
		"sem código":    {"current_password": testPass, "new_password": senhaNova},
		"código errado": {"current_password": testPass, "new_password": senhaNova, "totp_code": "000000"},
	} {
		resp := trocarSenha(t, a, token, corpo)
		got := codigoDoErro(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || got != "invalid_credentials" {
			t.Fatalf("%s: %d %s; queria 401 invalid_credentials", nome, resp.StatusCode, got)
		}
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Fatal("a senha mudou sem o segundo fator")
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas no bloqueio da conta, queria 0 (A3)", n)
	}

	code, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	certo := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova, "totp_code": code})
	certo.Body.Close()
	if certo.StatusCode != http.StatusNoContent {
		t.Fatalf("com o código certo: %d, queria 204", certo.StatusCode)
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("o sucesso não zerou o contador: %d", n)
	}
}

// O código de recuperação vale no lugar do código do app, e é consumido.
func TestChangePasswordAcceptsARecoveryCode(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)
	token := accessTokenDaSessao(t, a, c)

	resp := trocarSenha(t, a, token, map[string]string{
		"current_password": testPass, "new_password": senhaNova, "totp_code": a.recuperacao[0],
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("com código de recuperação: %d, queria 204", resp.StatusCode)
	}
	var restantes, usos int
	ctx := context.Background()
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, a.userID).Scan(&restantes); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'totp.recovery_used'`, a.userID).Scan(&usos); err != nil {
		t.Fatal(err)
	}
	if restantes != 9 || usos != 1 {
		t.Errorf("%d códigos restantes e %d usos auditados; queria 9 e 1", restantes, usos)
	}
}

// A política vem antes de tudo: com a senha nova fora dela, a senha atual
// (aqui, errada) nem é verificada, e nenhuma falha é contada.
func TestChangePasswordPolicy(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	casos := map[string]string{
		"too_short": strings.Repeat("a", auth.MinPasswordLen-1),
		"too_long":  strings.Repeat("a", auth.MaxPasswordLen+1),
	}
	for motivo, nova := range casos {
		resp := trocarSenha(t, a, token, map[string]string{"current_password": "errada-mas-irrelevante", "new_password": nova})
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Fields []struct {
					Field  string `json:"field"`
					Reason string `json:"reason"`
				} `json:"fields"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" ||
			len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "new_password" || env.Error.Fields[0].Reason != motivo {
			t.Errorf("%s: status %d, corpo %+v", motivo, resp.StatusCode, env.Error)
		}
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas contadas: a política tem de vir antes da senha atual", n)
	}
}

// Com a família do token encerrada (aqui, pelo logout), a troca é 401
// unauthorized e nada muda, nem com a senha certa.
//
// Duas guardas dão esta propriedade: a leitura antes da senha e a checagem sob
// a trava. Sabotar só uma deixa o teste verde; a segunda tem prova própria em
// TestChangePasswordRechecksTheFamilyUnderTheLock (internal/account).
func TestChangePasswordDeadFamily(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()

	resp := trocarSenha(t, a, token, map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigoDoErro(t, resp) != "unauthorized" {
		t.Fatalf("status %d, queria 401 unauthorized", resp.StatusCode)
	}
	if !senhaGravadaE(t, a, testPass) {
		t.Error("a senha mudou com a sessão encerrada")
	}
}

// A troca divide o balde do login (contrato: "mesmo limite por IP"). Os cinco
// logins usam e-mail inexistente para não travar a conta: o que se mede é o
// limitador.
//
// É a trava do wiring, pelo Routes de verdade: registrar /auth/password sem o
// limiteLogin, ou com uma instância própria do limitador, dá 401 no lugar do
// 429 (sabotagens registradas na Task 11). Os cinco logins recusados com 401,
// e não 429, são o contraponto: o balde tem 5 fichas, e a sexta requisição,
// que é a troca, é a que o esgota.
func TestChangePasswordSharesTheLoginLimiter(t *testing.T) {
	a := newAPI(t)
	for i := range 5 {
		resp := login(t, a, clientWithJar(t), "ninguem@exemplo.com", testPass)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login %d devolveu %d, queria 401: o balde esgotou antes da troca", i+1, resp.StatusCode)
		}
	}
	resp := trocarSenha(t, a, "token-qualquer", map[string]string{"current_password": testPass, "new_password": senhaNova})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status %d, queria 429: a troca não divide o balde do login", resp.StatusCode)
	}
}

// accessTokenDaSessao renova no aparelho c e devolve o access token novo, com
// o sid da família viva daquele aparelho.
func accessTokenDaSessao(t *testing.T, a *api, c *http.Client) string {
	t.Helper()
	resp := post(t, c, a.srv.URL+"/api/v1/auth/refresh", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh devolveu %d", resp.StatusCode)
	}
	return accessDe(t, resp)
}
