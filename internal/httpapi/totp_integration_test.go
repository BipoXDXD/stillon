package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/sentinela/internal/auth"
)

// accessTokenValido assina um access token legítimo do usuário do teste, com
// o mesmo segredo que a API usa.
func accessTokenValido(t *testing.T, a *api) string {
	t.Helper()
	tok, err := auth.NewAccessToken(testJWTSecret, a.userID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// statusEmMe devolve o status de /api/v1/me com o token dado. É o teste da
// pergunta "este token vale como credencial?".
func statusEmMe(t *testing.T, a *api, token string) int {
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

// cadastrarTOTP faz login, cadastra e confirma o segundo fator, e devolve o
// segredo base32 do usuário.
func cadastrarTOTP(t *testing.T, a *api, c *http.Client) string {
	t.Helper()

	entrada := login(t, a, c, testEmail, testPass)
	defer entrada.Body.Close()
	if entrada.StatusCode != http.StatusOK {
		t.Fatalf("login devolveu %d", entrada.StatusCode)
	}
	var primeiro struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(entrada.Body).Decode(&primeiro); err != nil {
		t.Fatal(err)
	}
	if primeiro.AccessToken == "" {
		t.Fatal("login sem TOTP não devolveu access token")
	}

	er := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/enroll", primeiro.AccessToken, nil)
	defer er.Body.Close()
	if er.StatusCode != http.StatusOK {
		t.Fatalf("enroll devolveu %d", er.StatusCode)
	}
	var enroll struct {
		URI string `json:"otpauth_uri"`
	}
	if err := json.NewDecoder(er.Body).Decode(&enroll); err != nil {
		t.Fatal(err)
	}

	key, err := otp.NewKeyFromURL(enroll.URI)
	if err != nil {
		t.Fatalf("enroll não devolveu uma URI otpauth:// válida: %v", err)
	}
	code, err := totp.GenerateCode(key.Secret(), a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}

	confirm := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/confirm", primeiro.AccessToken,
		map[string]string{"code": code})
	defer confirm.Body.Close()
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirmação devolveu %d", confirm.StatusCode)
	}
	var confirmado struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(confirm.Body).Decode(&confirmado); err != nil {
		t.Fatal(err)
	}
	if len(confirmado.RecoveryCodes) != 10 {
		t.Fatalf("%d códigos de recuperação, queria 10", len(confirmado.RecoveryCodes))
	}

	// Guardado para as asserções do chamador: com o segundo fator ativo, este
	// access token continua valendo em /me. É a perna positiva que impede que
	// "o desafio é recusado em /me" seja verdade por /me recusar tudo.
	if got := statusEmMe(t, a, primeiro.AccessToken); got != http.StatusOK {
		t.Fatalf("/me com access token legítimo devolveu %d, queria 200", got)
	}

	return key.Secret()
}

// Prova a propriedade que importa: com TOTP ativo, senha correta sozinha não
// entrega sessão nenhuma.
func TestLoginWithTOTPRequiresSecondFactor(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	secret := cadastrarTOTP(t, a, c)

	// Agora o login com senha correta não pode mais entregar sessão.
	segundo := login(t, a, c, testEmail, testPass)
	defer segundo.Body.Close()
	if segundo.StatusCode != http.StatusOK {
		t.Fatalf("login com TOTP ativo devolveu %d", segundo.StatusCode)
	}

	var desafio struct {
		TOTPRequired bool   `json:"totp_required"`
		Challenge    string `json:"challenge"`
		AccessToken  string `json:"access_token"`
	}
	if err := json.NewDecoder(segundo.Body).Decode(&desafio); err != nil {
		t.Fatal(err)
	}

	if !desafio.TOTPRequired {
		t.Fatal("login não exigiu segundo fator")
	}
	if desafio.AccessToken != "" {
		t.Error("login entregou access token antes do segundo fator")
	}
	if desafio.Challenge == "" {
		t.Fatal("login não entregou desafio")
	}
	// Nem sessão pela porta dos fundos: sem cookie de refresh, não há como
	// trocar o desafio por uma sessão em /auth/refresh.
	if ck := refreshCookie(segundo); ck != nil && ck.Value != "" {
		t.Error("login entregou cookie de refresh antes do segundo fator")
	}

	// O desafio não pode valer como credencial. Se valesse, quem descobriu a
	// senha entraria direto e o segundo fator seria decoração. A perna oposta
	// — /me aceita o access token legítimo — já foi verificada no cadastro.
	if got := statusEmMe(t, a, desafio.Challenge); got != http.StatusUnauthorized {
		t.Errorf("o token de desafio foi aceito em /me (status %d)", got)
	}

	// Código errado não fecha o login, e não pode contar que a senha estava
	// certa: é o mesmo 401 genérico do login.
	errado := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio.Challenge, "code": "000000"})
	defer errado.Body.Close()
	if errado.StatusCode != http.StatusUnauthorized {
		t.Errorf("código errado devolveu %d, queria 401", errado.StatusCode)
	}
	if code := decodeEnvelope(t, errado).Error.Code; code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", code)
	}
	if ck := refreshCookie(errado); ck != nil && ck.Value != "" {
		t.Error("código errado ainda assim emitiu cookie de refresh")
	}

	// Completar o segundo fator entrega a sessão.
	code2, err := totp.GenerateCode(secret, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio.Challenge, "code": code2})
	defer final.Body.Close()
	if final.StatusCode != http.StatusOK {
		t.Fatalf("verificação do TOTP devolveu %d", final.StatusCode)
	}
	var sessao struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(final.Body).Decode(&sessao); err != nil {
		t.Fatal(err)
	}
	if sessao.AccessToken == "" {
		t.Fatal("o segundo fator completo não devolveu access token")
	}
	if ck := refreshCookie(final); ck == nil || ck.Value == "" {
		t.Error("o segundo fator completo não abriu sessão (sem cookie de refresh)")
	}
	// E o token que sai daqui é credencial de verdade — senão o fluxo inteiro
	// terminaria sem dar acesso a nada, e o teste do desafio acima seria
	// verdadeiro por vacuidade.
	if got := statusEmMe(t, a, sessao.AccessToken); got != http.StatusOK {
		t.Errorf("/me com o token emitido após o segundo fator devolveu %d, queria 200", got)
	}
}

// O sentido oposto da separação por audience, no HTTP: um access token comum
// não pode servir de desafio. Se servisse, qualquer sessão viva fecharia o
// segundo fator de quem já está logado sem nunca ter digitado um código — e,
// pior, o desvio do login seria contornável por quem tivesse um token antigo.
func TestAccessTokenIsNotAcceptedAsTOTPChallenge(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	secret := cadastrarTOTP(t, a, c)

	entrada := login(t, a, c, testEmail, testPass)
	defer entrada.Body.Close()
	var desafio struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(entrada.Body).Decode(&desafio); err != nil {
		t.Fatal(err)
	}
	if desafio.Challenge == "" {
		t.Fatal("login não entregou desafio")
	}

	code, err := totp.GenerateCode(secret, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Um access token válido, do mesmo usuário e assinado com o mesmo
	// segredo, mas sem a audience do desafio.
	acesso := accessTokenValido(t, a)
	comAcesso := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": acesso, "code": code})
	defer comAcesso.Body.Close()
	if comAcesso.StatusCode != http.StatusUnauthorized {
		t.Errorf("access token foi aceito como desafio (status %d)", comAcesso.StatusCode)
	}

	// Perna oposta, com o MESMO código: o desafio de verdade passa. Sem ela,
	// a recusa acima poderia vir do código, e não da audience.
	comDesafio := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio.Challenge, "code": code})
	defer comDesafio.Body.Close()
	if comDesafio.StatusCode != http.StatusOK {
		t.Errorf("o desafio legítimo com o mesmo código devolveu %d, queria 200", comDesafio.StatusCode)
	}
}

// O código de recuperação é a saída de quem perdeu o celular, e vale uma vez
// só — percorrido pelo HTTP inteiro, que é por onde o usuário passa.
func TestRecoveryCodeClosesLoginOnlyOnce(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)

	entrada := login(t, a, c, testEmail, testPass)
	var primeiro struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(entrada.Body).Decode(&primeiro); err != nil {
		t.Fatal(err)
	}
	entrada.Body.Close()

	er := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/enroll", primeiro.AccessToken, nil)
	var enroll struct {
		URI string `json:"otpauth_uri"`
	}
	if err := json.NewDecoder(er.Body).Decode(&enroll); err != nil {
		t.Fatal(err)
	}
	er.Body.Close()

	key, err := otp.NewKeyFromURL(enroll.URI)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(key.Secret(), a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirm := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/confirm", primeiro.AccessToken,
		map[string]string{"code": code})
	var confirmado struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(confirm.Body).Decode(&confirmado); err != nil {
		t.Fatal(err)
	}
	confirm.Body.Close()
	if len(confirmado.RecoveryCodes) < 2 {
		t.Fatalf("%d códigos de recuperação, queria 10", len(confirmado.RecoveryCodes))
	}

	usar := func(codigo string) int {
		t.Helper()
		resp := login(t, a, c, testEmail, testPass)
		var d struct {
			Challenge string `json:"challenge"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if d.Challenge == "" {
			t.Fatal("login não entregou desafio")
		}
		v := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": d.Challenge, "code": codigo})
		defer v.Body.Close()
		return v.StatusCode
	}

	if got := usar(confirmado.RecoveryCodes[0]); got != http.StatusOK {
		t.Fatalf("primeiro uso do código de recuperação devolveu %d, queria 200", got)
	}
	if got := usar(confirmado.RecoveryCodes[0]); got != http.StatusUnauthorized {
		t.Errorf("o mesmo código de recuperação foi aceito de novo (status %d)", got)
	}
	if got := usar(confirmado.RecoveryCodes[1]); got != http.StatusOK {
		t.Errorf("outro código de recuperação devolveu %d, queria 200", got)
	}
}
