package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// verificar manda o desafio com o código TOTP "de agora" e devolve a resposta.
func verificar(t *testing.T, a *api, c *http.Client, segredo, desafio string) *http.Response {
	t.Helper()
	code, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": code})
}

// O desafio abre UMA sessão (Plano 8). Antes, com o mesmo desafio e um código
// TOTP novo a cada período, abria uma sessão por período durante os cinco
// minutos de validade.
func TestTOTPChallengeOpensOnlyOneSession(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)

	primeiro := verificar(t, a, c, segredo, desafio)
	defer primeiro.Body.Close()
	if primeiro.StatusCode != http.StatusOK {
		t.Fatalf("primeiro uso do desafio devolveu %d, queria 200", primeiro.StatusCode)
	}

	// Um período depois: o código é NOVO (o timestep anterior já foi
	// consumido e não é o que está sendo testado aqui), o desafio é o mesmo
	// e ainda está no prazo.
	a.clk.Advance(30 * time.Second)
	segundo := verificar(t, a, c, segredo, desafio)
	defer segundo.Body.Close()
	if segundo.StatusCode != http.StatusUnauthorized {
		t.Fatalf("o mesmo desafio com um código novo devolveu %d, queria 401", segundo.StatusCode)
	}
	if code := decodeEnvelope(t, segundo).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized (o desafio, e não o código, foi recusado)", code)
	}
	if ck := refreshCookie(segundo); ck != nil && ck.Value != "" {
		t.Error("o desafio reusado ainda assim emitiu cookie de refresh")
	}

	// Perna positiva, no mesmo instante e com o mesmo tipo de código: um
	// desafio NOVO passa. Sem ela, o 401 acima poderia vir do código.
	a.clk.Advance(30 * time.Second)
	novo := verificar(t, a, c, segredo, desafioAtual(t, a, c))
	defer novo.Body.Close()
	if novo.StatusCode != http.StatusOK {
		t.Errorf("um desafio novo devolveu %d, queria 200", novo.StatusCode)
	}
}

// Consumir um desafio invalida os MAIS ANTIGOS ainda abertos; o inverso, o
// mais antigo primeiro e o mais novo depois, funciona (spec do Plano 8, §3.1).
func TestNewerTOTPChallengeRetiresTheOlderOne(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)

	antigo := desafioAtual(t, a, c)
	a.clk.Advance(time.Second)
	novo := desafioAtual(t, a, c)

	r1 := verificar(t, a, c, segredo, novo)
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("o desafio mais novo devolveu %d, queria 200", r1.StatusCode)
	}
	a.clk.Advance(30 * time.Second)
	r2 := verificar(t, a, c, segredo, antigo)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("o desafio mais antigo, depois do mais novo, devolveu %d, queria 401", r2.StatusCode)
	}

	// O inverso: dois desafios novos, o mais antigo verificado primeiro.
	a.clk.Advance(30 * time.Second)
	antigo = desafioAtual(t, a, c)
	a.clk.Advance(time.Second)
	novo = desafioAtual(t, a, c)
	r3 := verificar(t, a, c, segredo, antigo)
	defer r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("o desafio mais antigo, verificado primeiro, devolveu %d, queria 200", r3.StatusCode)
	}
	a.clk.Advance(30 * time.Second)
	r4 := verificar(t, a, c, segredo, novo)
	defer r4.Body.Close()
	if r4.StatusCode != http.StatusOK {
		t.Errorf("o desafio mais novo, depois do mais antigo, devolveu %d, queria 200", r4.StatusCode)
	}
}

// Um desafio emitido antes do deploy do Plano 8 não tem o claim cat. Ele
// chega ao verify como qualquer desafio inválido: 401 unauthorized, que o
// painel trata voltando ao passo da senha, e nunca um 500.
func TestTOTPChallengeFromBeforeTheMarkIsRefusedAsExpired(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)

	var u struct{ PasswordHash string }
	if err := a.pool.QueryRow(t.Context(), `SELECT password_hash FROM users WHERE id = $1`, a.userID).
		Scan(&u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	antigo, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": a.userID.String(),
		"aud": []string{"totp-challenge"},
		"iat": a.clk.Now().Unix(),
		"exp": a.clk.Now().Add(auth.ChallengeTTL).Unix(),
		"pws": auth.PasswordStamp(u.PasswordHash),
	}).SignedString(testJWTSecret)
	if err != nil {
		t.Fatal(err)
	}

	r := verificar(t, a, c, segredo, antigo)
	defer r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("desafio sem cat devolveu %d, queria 401", r.StatusCode)
	}
	if code := decodeEnvelope(t, r).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized", code)
	}
}

// Um desafio já consumido é recusado ANTES do código, e o código de
// recuperação enviado com ele continua valendo (revisão final do Plano 8).
// Antes, o TOTP.Verify apagava o código de recuperação numa transação própria
// e só depois o ConsumeChallenge recusava o desafio: o usuário recebia 401 e
// perdia um código que não se renova. Os gatilhos reais são duas abas (a mais
// nova já entrou) e a resposta do verify perdida na rede, com o reenvio do
// mesmo desafio e o próximo código.
func TestSpentTOTPChallengeDoesNotBurnARecoveryCode(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	recuperacao := codigosDeRecuperacao(t, a, c)
	desafio := desafioAtual(t, a, c)

	primeiro := verificar(t, a, c, segredo, desafio)
	defer primeiro.Body.Close()
	if primeiro.StatusCode != http.StatusOK {
		t.Fatalf("primeiro uso do desafio devolveu %d, queria 200", primeiro.StatusCode)
	}

	reuso := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": recuperacao[0]})
	defer reuso.Body.Close()
	if reuso.StatusCode != http.StatusUnauthorized {
		t.Fatalf("o desafio gasto com um código de recuperação devolveu %d, queria 401", reuso.StatusCode)
	}
	if code := decodeEnvelope(t, reuso).Error.Code; code != "unauthorized" {
		t.Errorf("code = %q, queria unauthorized (o desafio, e não o código, foi recusado)", code)
	}

	// A prova de que o código não foi gasto: com um desafio NOVO, o MESMO
	// código de recuperação abre a sessão. O segundo de avanço é o do D10:
	// com o relógio parado, o desafio novo sairia no mesmo microssegundo do
	// consumido e seria recusado pela marca.
	a.clk.Advance(time.Second)
	novo := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafioAtual(t, a, c), "code": recuperacao[0]})
	defer novo.Body.Close()
	if novo.StatusCode != http.StatusOK {
		t.Errorf("o mesmo código de recuperação, com um desafio novo, devolveu %d, queria 200 (o desafio gasto queimou o código)", novo.StatusCode)
	}
}
