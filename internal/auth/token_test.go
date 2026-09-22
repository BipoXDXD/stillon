package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/auth"
)

var jwtSecret = []byte("segredo-de-teste-com-32-bytes!!!")

func TestAccessTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()

	raw, err := auth.NewAccessToken(jwtSecret, userID, now)
	if err != nil {
		t.Fatal(err)
	}

	got, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got != userID {
		t.Errorf("userID = %s, queria %s", got, userID)
	}
}

func TestAccessTokenExpiresIn15Minutes(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)

	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(14*time.Minute)); err != nil {
		t.Errorf("aos 14 minutos ainda deveria valer: %v", err)
	}
	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(16*time.Minute)); err == nil {
		t.Error("aos 16 minutos deveria ter expirado")
	}
}

func TestAccessTokenRejectsWrongSecret(t *testing.T) {
	now := time.Now()
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), now)

	if _, err := auth.ParseAccessToken([]byte("outro-segredo-com-32-bytes!!!!!!"), raw, now); err == nil {
		t.Error("token assinado com outro segredo foi aceito")
	}
}

// "alg: none" é o ataque clássico contra JWT: o atacante troca o algoritmo e
// remove a assinatura.
//
// Este teste NÃO trava o jwt.WithValidMethods (ruling R30): na golang-jwt v5 o
// alg=none já morre dentro do Verify, que devolve
// NoneSignatureTypeDisallowedError para qualquer chave que não seja a
// constante UnsafeAllowNoneSignatureType — e a nossa keyfunc devolve []byte.
// Ele fica como trava contra uma versão futura que afrouxe isso. Quem trava a
// opção é o TestAccessTokenRejectsAlgorithmConfusion, abaixo.
func TestAccessTokenRejectsNoneAlgorithm(t *testing.T) {
	forjado := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiIwMTkyYzNkNC1hYmNkLTdlZmYtODAwMC0wMDAwMDAwMDAwMDAifQ."

	if _, err := auth.ParseAccessToken(jwtSecret, forjado, time.Now()); err == nil {
		t.Error("token com alg=none foi aceito")
	}
}

// Confusão de algoritmo: o atacante mantém o mesmo segredo e só troca o alg do
// cabeçalho. É isto que o jwt.WithValidMethods impede — sem ele, a biblioteca
// verifica com o método que o cabeçalho pedir, ou seja, com o método que o
// atacante escolheu.
func TestAccessTokenRejectsAlgorithmConfusion(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	claims := jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	}

	forjado, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := auth.ParseAccessToken(jwtSecret, forjado, now); err == nil {
		t.Error("token forjado com alg=HS512 foi aceito; só HS256 é esperado")
	}
}

func TestChallengeTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()

	raw, err := auth.NewChallengeToken(jwtSecret, userID, now)
	if err != nil {
		t.Fatal(err)
	}

	got, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got != userID {
		t.Errorf("userID = %s, queria %s", got, userID)
	}
}

// O desafio dura só o tempo de digitar seis dígitos.
func TestChallengeTokenExpiresQuickly(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw, err := auth.NewChallengeToken(jwtSecret, uuid.New(), now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL-time.Minute)); err != nil {
		t.Errorf("dentro do prazo o desafio deveria valer: %v", err)
	}
	if _, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(auth.ChallengeTTL+time.Minute)); err == nil {
		t.Error("o desafio deveria ter expirado")
	}
}

// Os dois sentidos da separação por audience.
//
// Este é o ponto de segurança do segundo fator: o desafio é entregue DEPOIS da
// senha e ANTES do código. Se ele valesse como access token, quem descobriu a
// senha entraria direto e o TOTP seria decoração. E se um access token comum
// valesse como desafio, qualquer sessão viva fecharia o segundo fator sem
// nunca ter digitado um código.
//
// Cada perna tem seu contraponto positivo nos round-trips acima, então a
// recusa aqui não pode vir de "recusa tudo".
func TestChallengeAndAccessTokensAreNotInterchangeable(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()

	desafio, err := auth.NewChallengeToken(jwtSecret, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	acesso, err := auth.NewAccessToken(jwtSecret, userID, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := auth.ParseAccessToken(jwtSecret, desafio, now); err == nil {
		t.Error("o token de desafio foi aceito como access token: o segundo fator seria decorativo")
	}
	if _, err := auth.ParseChallengeToken(jwtSecret, acesso, now); err == nil {
		t.Error("um access token comum foi aceito como desafio: o segundo fator seria dispensável")
	}
}
