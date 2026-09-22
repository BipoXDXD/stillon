package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

	er := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/enroll", primeiro.AccessToken, map[string]string{"password": testPass})
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

	a.recuperacao = confirmado.RecoveryCodes
	return key.Secret()
}

// codigosDeRecuperacao devolve os códigos guardados pelo cadastro.
func codigosDeRecuperacao(t *testing.T, a *api, _ *http.Client) []string {
	t.Helper()
	if len(a.recuperacao) != 10 {
		t.Fatalf("%d códigos guardados, queria 10", len(a.recuperacao))
	}
	return a.recuperacao
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

	er := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/enroll", primeiro.AccessToken, map[string]string{"password": testPass})
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

// estadoDoBloqueio devolve o contador de falhas e se há bloqueio ativo.
func estadoDoBloqueio(t *testing.T, a *api) (falhas int, bloqueado bool) {
	t.Helper()
	var ate *time.Time
	if err := a.pool.QueryRow(context.Background(),
		`SELECT failed_attempts, locked_until FROM users WHERE id = $1`, a.userID).
		Scan(&falhas, &ate); err != nil {
		t.Fatal(err)
	}
	return falhas, ate != nil && a.clk.Now().Before(*ate)
}

// desafioAtual faz login e devolve o token de desafio.
func desafioAtual(t *testing.T, a *api, c *http.Client) string {
	t.Helper()
	resp := login(t, a, c, testEmail, testPass)
	defer resp.Body.Close()
	var d struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Challenge == "" {
		t.Fatalf("login não entregou desafio (status %d)", resp.StatusCode)
	}
	return d.Challenge
}

// R38, pelo HTTP: com o fator ativo, enroll devolve 403 e não desliga nada.
//
// É o bypass que importa: o atacante com um access token ROUBADO chamaria
// enroll, o segundo fator cairia, e a senha voltaria a bastar sozinha.
func TestEnrollIsForbiddenAfterTOTPIsConfirmed(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)

	// O token de quem já está dentro — exatamente o que um roubo entregaria.
	token := accessTokenValido(t, a)
	resp := postComToken(t, c, a.srv.URL+"/api/v1/auth/totp/enroll", token, map[string]string{"password": testPass})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("enroll com fator ativo devolveu %d, queria 403", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "forbidden" {
		t.Errorf("code = %q, queria forbidden", code)
	}

	// A asserção que separa "recusou" de "recusou sem estragar nada".
	var confirmadoEm *time.Time
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_confirmed_at FROM users WHERE id = $1`, a.userID).Scan(&confirmadoEm); err != nil {
		t.Fatal(err)
	}
	if confirmadoEm == nil {
		t.Fatal("o enroll recusado desligou o segundo fator no banco: bypass de 2FA")
	}

	// E a consequência prática: o login continua exigindo o segundo fator.
	depois := login(t, a, c, testEmail, testPass)
	defer depois.Body.Close()
	var d struct {
		TOTPRequired bool   `json:"totp_required"`
		AccessToken  string `json:"access_token"`
	}
	if err := json.NewDecoder(depois.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if !d.TOTPRequired || d.AccessToken != "" {
		t.Error("depois do enroll recusado o login voltou a passar só com a senha")
	}
}

// R40: errar o segundo fator conta para o bloqueio progressivo da conta.
//
// Sem isso, quem já tem a senha chuta os seis dígitos indefinidamente: o
// limite de /totp/verify é por IP, e várias origens passam por cima dele.
func TestFailedSecondFactorLocksTheAccount(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)

	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != 0 || bloqueado {
		t.Fatalf("a conta já começou com %d falhas (bloqueada=%v)", falhas, bloqueado)
	}

	// Um único desafio serve para todas as tentativas: ele não é consumido no
	// erro, e é assim que o atacante faria.
	desafio := desafioAtual(t, a, c)
	for i := 0; i < auth.LockThreshold; i++ {
		resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": "000000"})
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusUnauthorized {
			t.Fatalf("tentativa %d devolveu %d, queria 401", i+1, status)
		}
	}

	falhas, bloqueado := estadoDoBloqueio(t, a)
	if falhas < auth.LockThreshold {
		t.Errorf("failed_attempts = %d depois de %d erros de segundo fator, queria >= %d: "+
			"as falhas do segundo fator não contam para o bloqueio",
			falhas, auth.LockThreshold, auth.LockThreshold)
	}
	if !bloqueado {
		t.Error("a conta não trancou depois de errar o segundo fator LockThreshold vezes")
	}

	// (O segundo fator correto também para de passar enquanto durar o
	// bloqueio — provado em TestSecondFactorIsRefusedWhileTheAccountIsLocked,
	// em teste separado porque aqui o limite por IP de /totp/verify já foi
	// gasto pelas cinco tentativas acima.)

	// Consequência prática: nem a senha correta passa mais.
	resp := login(t, a, c, testEmail, testPass)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login em conta trancada pelo segundo fator devolveu %d, queria 401", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", code)
	}
}

// A perna oposta do R40: o segundo fator correto zera o contador. Sem ela,
// "as falhas contam" passaria numa implementação que nunca zera e trancaria a
// conta de quem acerta.
func TestSuccessfulSecondFactorClearsTheFailureCount(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	secret := cadastrarTOTP(t, a, c)

	desafio := desafioAtual(t, a, c)
	for i := 0; i < 2; i++ {
		resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": "000000"})
		resp.Body.Close()
	}
	if falhas, _ := estadoDoBloqueio(t, a); falhas != 2 {
		t.Fatalf("failed_attempts = %d, queria 2", falhas)
	}

	code, err := totp.GenerateCode(secret, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": code})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("o código correto devolveu %d, queria 200", ok.StatusCode)
	}

	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != 0 || bloqueado {
		t.Errorf("failed_attempts = %d (bloqueada=%v) depois do segundo fator correto, queria 0",
			falhas, bloqueado)
	}
}

// O login com a senha certa NÃO pode zerar o contador quando há segundo fator
// ativo: seria um botão de destravar entre um chute de seis dígitos e o
// próximo, e o bloqueio do R40 nunca chegaria ao limiar.
func TestLoggingInAgainDoesNotClearSecondFactorFailures(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)

	desafio := desafioAtual(t, a, c)
	for i := 0; i < 2; i++ {
		resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
			map[string]string{"challenge": desafio, "code": "000000"})
		resp.Body.Close()
	}
	if falhas, _ := estadoDoBloqueio(t, a); falhas != 2 {
		t.Fatalf("failed_attempts = %d, queria 2", falhas)
	}

	// O atacante, que tem a senha, tenta usar o próprio login como reset.
	desafioAtual(t, a, c)

	if falhas, _ := estadoDoBloqueio(t, a); falhas != 2 {
		t.Errorf("failed_attempts = %d depois de novo login, queria 2: "+
			"refazer o login zera o contador e o bloqueio do segundo fator nunca dispara", falhas)
	}
}

// Enquanto a conta está trancada, nem o segundo fator correto fecha o login.
//
// Sem esta guarda, o bloqueio do R40 não seguraria nada: o atacante que
// chegasse ao código certo depois de trancar a conta entraria assim mesmo, e o
// ResetFailures do sucesso ainda destravaria a conta para ele.
func TestSecondFactorIsRefusedWhileTheAccountIsLocked(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	secret := cadastrarTOTP(t, a, c)

	// O desafio é obtido ANTES do bloqueio: com a conta trancada o login nem
	// chega a emitir um.
	desafio := desafioAtual(t, a, c)
	bloquearConta(t, a)

	code, err := totp.GenerateCode(secret, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": code})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("segundo fator correto em conta trancada devolveu %d, queria 401", resp.StatusCode)
	}
	if code := decodeEnvelope(t, resp).Error.Code; code != "invalid_credentials" {
		t.Errorf("code = %q, queria invalid_credentials", code)
	}

	// A perna oposta: destravada, a mesma verificação passa. Sem ela, o 401
	// acima poderia vir do código, do desafio ou de qualquer outra coisa.
	// O desafio e o código são refeitos porque o relógio andou duas horas:
	// ambos têm vida curta de propósito.
	a.clk.Advance(2 * time.Hour)
	novoDesafio := desafioAtual(t, a, c)
	novoCode, err := totp.GenerateCode(secret, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	liberado := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": novoDesafio, "code": novoCode})
	defer liberado.Body.Close()
	if liberado.StatusCode != http.StatusOK {
		t.Errorf("depois de o bloqueio expirar o segundo fator devolveu %d, queria 200", liberado.StatusCode)
	}
}

// segredoGravado responde se o enroll chegou a gravar um segredo para o
// usuário. É a asserção que distingue "recusou" de "recusou e gravou mesmo
// assim" — sem ela, um handler que devolvesse 401 depois de gravar passaria.
func segredoGravado(t *testing.T, a *api) bool {
	t.Helper()
	var sealed []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, a.userID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return len(sealed) > 0
}

// TestEnrollRequiresThePassword fecha o espelho do R38.
//
// O R38 fechou "token roubado DESLIGA o fator". Ninguém havia fechado "token
// roubado LIGA o fator com o autenticador do ladrão", que é pior: como o R38
// removeu o caminho de volta, o sequestro é permanente e os códigos de
// recuperação saem no mesmo passo, para o atacante.
func TestEnrollRequiresThePassword(t *testing.T) {
	a := newAPI(t)
	token := accessTokenValido(t, a)

	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token,
		map[string]string{"password": "senha-que-nao-e-a-dele"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("enroll com senha errada devolveu %d, queria 401", resp.StatusCode)
	}
	if segredoGravado(t, a) {
		t.Error("o enroll recusado gravou o segredo mesmo assim")
	}
}

// TestStolenAccessTokenCannotEnableTheSecondFactor percorre o sequestro
// inteiro: tudo que o atacante tem é um access token válido da vítima.
func TestStolenAccessTokenCannotEnableTheSecondFactor(t *testing.T) {
	a := newAPI(t)
	roubado := accessTokenValido(t, a)

	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", roubado, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("enroll só com access token devolveu 200: a conta pode ser sequestrada")
	}

	// A prova que importa não é o status: é a vítima continuar entrando na
	// própria conta com a senha, sem segundo fator nenhum no caminho.
	c := clientWithJar(t)
	entrada := login(t, a, c, testEmail, testPass)
	defer entrada.Body.Close()
	var corpo struct {
		AccessToken  string `json:"access_token"`
		TOTPRequired bool   `json:"totp_required"`
	}
	if err := json.NewDecoder(entrada.Body).Decode(&corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.TOTPRequired {
		t.Error("a vítima passou a precisar de um segundo fator que ela não cadastrou")
	}
	if corpo.AccessToken == "" {
		t.Error("a vítima perdeu o acesso à própria conta com a senha correta")
	}
}

// estadoDoSegundoFator lê direto do banco: confirmado, segredo ainda gravado,
// quantos códigos de recuperação sobraram e quantos eventos totp.disabled a
// auditoria registrou.
func estadoDoSegundoFator(t *testing.T, a *api) (confirmado, temSegredo bool, codigos, desativacoes int) {
	t.Helper()
	ctx := context.Background()
	var at *time.Time
	var sealed []byte
	if err := a.pool.QueryRow(ctx,
		`SELECT totp_confirmed_at, totp_secret_enc FROM users WHERE id = $1`, a.userID).
		Scan(&at, &sealed); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, a.userID).Scan(&codigos); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'totp.disabled'`,
		a.userID).Scan(&desativacoes); err != nil {
		t.Fatal(err)
	}
	return at != nil, len(sealed) > 0, codigos, desativacoes
}

func desativar(t *testing.T, a *api, token string, corpo map[string]string) int {
	t.Helper()
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/disable", token, corpo)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestDisableRequiresPasswordAndPossession dá a saída que o R38 tirou.
//
// Sem rota de desativação, quem foi sequestrado pelo caminho do item 20 não
// tinha remédio dentro do sistema. A rota precisa de DUAS provas: a senha e a
// posse do fator. Só a senha rebaixaria o 2FA a nada; só o código deixaria a
// sessão roubada desligá-lo, que é exatamente o bypass do R38.
func TestDisableRequiresPasswordAndPossession(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	token := accessTokenValido(t, a)

	codigo := func() string {
		t.Helper()
		code, err := totp.GenerateCode(segredo, a.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		return code
	}

	if got := desativar(t, a, token, map[string]string{"password": testPass}); got != http.StatusUnauthorized {
		t.Errorf("desativar só com a senha devolveu %d, queria 401", got)
	}
	if got := desativar(t, a, token, map[string]string{"code": codigo()}); got != http.StatusUnauthorized {
		t.Errorf("desativar só com o código devolveu %d, queria 401", got)
	}
	if got := desativar(t, a, token, map[string]string{
		"password": "senha-que-nao-e-a-dele", "code": codigo()}); got != http.StatusUnauthorized {
		t.Errorf("desativar com senha errada devolveu %d, queria 401", got)
	}

	// Nenhuma das três recusas pode ter desligado nada.
	if confirmado, temSegredo, codigos, _ := estadoDoSegundoFator(t, a); !confirmado || !temSegredo || codigos != 10 {
		t.Fatalf("uma recusa mexeu no estado: confirmado=%v segredo=%v códigos=%d",
			confirmado, temSegredo, codigos)
	}

	if got := desativar(t, a, token, map[string]string{
		"password": testPass, "code": codigo()}); got != http.StatusNoContent {
		t.Fatalf("desativar com senha e código devolveu %d, queria 204", got)
	}

	confirmado, temSegredo, codigos, desativacoes := estadoDoSegundoFator(t, a)
	if confirmado {
		t.Error("o segundo fator continuou confirmado depois da desativação")
	}
	if temSegredo {
		t.Error("o segredo continuou gravado depois da desativação")
	}
	if codigos != 0 {
		t.Errorf("sobraram %d códigos de recuperação depois da desativação", codigos)
	}
	if desativacoes != 1 {
		t.Errorf("auditoria registrou %d eventos totp.disabled, queria 1", desativacoes)
	}

	// A prova final: a conta volta a entrar só com a senha.
	c2 := clientWithJar(t)
	entrada := login(t, a, c2, testEmail, testPass)
	defer entrada.Body.Close()
	var corpo struct {
		AccessToken  string `json:"access_token"`
		TOTPRequired bool   `json:"totp_required"`
	}
	if err := json.NewDecoder(entrada.Body).Decode(&corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.TOTPRequired || corpo.AccessToken == "" {
		t.Error("depois de desativar, o login ainda exige segundo fator")
	}
}

// TestDisableAcceptsARecoveryCode é o caso que motiva a rota existir: quem
// perdeu o telefone não tem código do app, e sem isto ficaria trancado.
func TestDisableAcceptsARecoveryCode(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)
	token := accessTokenValido(t, a)

	recuperacao := codigosDeRecuperacao(t, a, c)

	if got := desativar(t, a, token, map[string]string{
		"password": testPass, "recovery_code": recuperacao[0]}); got != http.StatusNoContent {
		t.Fatalf("desativar com código de recuperação devolveu %d, queria 204", got)
	}
	if confirmado, temSegredo, codigos, _ := estadoDoSegundoFator(t, a); confirmado || temSegredo || codigos != 0 {
		t.Errorf("desativação incompleta: confirmado=%v segredo=%v códigos=%d",
			confirmado, temSegredo, codigos)
	}
}
