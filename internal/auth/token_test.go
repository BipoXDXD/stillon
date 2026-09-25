package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

var jwtSecret = []byte("segredo-de-teste-com-32-bytes!!!")

func TestAccessTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	userID, familia := uuid.New(), uuid.New()

	raw, err := auth.NewAccessToken(jwtSecret, userID, familia, now)
	if err != nil {
		t.Fatal(err)
	}

	got, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != userID {
		t.Errorf("userID = %s, queria %s", got.UserID, userID)
	}
	if got.FamilyID != familia {
		t.Errorf("família = %s, queria %s", got.FamilyID, familia)
	}
}

// Um token sem sid não diz de qual sessão saiu, e as rotas de credencial não
// teriam como recusá-lo depois de a sessão ser encerrada (D2 do Plano 5). É
// assinado com o segredo CERTO: a recusa só pode vir da falta do claim.
func TestAccessTokenWithoutSessionFamilyIsRejected(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	registrados := jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	}
	casos := map[string]jwt.Claims{
		"sem sid": registrados,
		"sid que não é UUID": jwt.MapClaims{
			"sub": registrados.Subject, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "sid": "abc",
		},
		"sid nulo": jwt.MapClaims{
			"sub": registrados.Subject, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "sid": uuid.Nil.String(),
		},
	}
	for nome, claims := range casos {
		t.Run(nome, func(t *testing.T) {
			raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := auth.ParseAccessToken(jwtSecret, raw, now); err == nil {
				t.Error("token sem família de sessão válida foi aceito")
			}
		})
	}
}

func TestAccessTokenRefusesNilFamily(t *testing.T) {
	if _, err := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.Nil, time.Now()); err == nil {
		t.Error("assinou um token sem família de sessão")
	}
}

func TestAccessTokenExpiresIn15Minutes(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.New(), now)

	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(14*time.Minute)); err != nil {
		t.Errorf("aos 14 minutos ainda deveria valer: %v", err)
	}
	if _, err := auth.ParseAccessToken(jwtSecret, raw, now.Add(16*time.Minute)); err == nil {
		t.Error("aos 16 minutos deveria ter expirado")
	}
}

func TestAccessTokenRejectsWrongSecret(t *testing.T) {
	now := time.Now()
	raw, _ := auth.NewAccessToken(jwtSecret, uuid.New(), uuid.New(), now)

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

	carimbo := auth.PasswordStamp("$argon2id$hash-de-teste")

	raw, err := auth.NewChallengeToken(jwtSecret, userID, carimbo, now)
	if err != nil {
		t.Fatal(err)
	}

	got, err := auth.ParseChallengeToken(jwtSecret, raw, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != userID {
		t.Errorf("userID = %s, queria %s", got.UserID, userID)
	}
	if got.Stamp != carimbo {
		t.Errorf("carimbo = %q, queria %q", got.Stamp, carimbo)
	}
	if !got.IssuedAt.Equal(now) {
		t.Errorf("emitido em %v, queria %v", got.IssuedAt, now)
	}
}

// O instante do desafio tem resolução de microssegundo, e não de segundo como
// o iat: é a chave da marca d'água do /totp/verify, e dois desafios do mesmo
// usuário no mesmo segundo precisam de marcas diferentes (Plano 8).
func TestChallengeTokenKeepsTheIssueInstantToTheMicrosecond(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	instantes := []time.Time{now.Add(1 * time.Microsecond), now.Add(2 * time.Microsecond)}
	for _, em := range instantes {
		raw, err := auth.NewChallengeToken(jwtSecret, uuid.New(), auth.PasswordStamp("x"), em)
		if err != nil {
			t.Fatal(err)
		}
		got, err := auth.ParseChallengeToken(jwtSecret, raw, em)
		if err != nil {
			t.Fatal(err)
		}
		if !got.IssuedAt.Equal(em) {
			t.Errorf("emitido em %v, voltou %v: o instante perdeu resolução", em, got.IssuedAt)
		}
	}
}

// Desafio sem instante de emissão (um emitido antes do Plano 8) é recusado:
// sem ele, o /totp/verify não tem o que gravar como marca d'água.
func TestChallengeTokenRequiresTheIssueInstant(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	semInstante, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.NewString(),
		"aud": []string{"totp-challenge"},
		"exp": now.Add(time.Minute).Unix(),
		"pws": auth.PasswordStamp("x"),
	}).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ParseChallengeToken(jwtSecret, semInstante, now); err == nil {
		t.Error("desafio sem instante de emissão foi aceito")
	}
}

// O desafio leva o carimbo da senha verificada, e sem ele não existe: é o que
// o /totp/verify entrega a Issue para recusar a sessão depois de um
// set-password (Task 11 do Plano 5).
func TestChallengeTokenRequiresThePasswordStamp(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if _, err := auth.NewChallengeToken(jwtSecret, uuid.New(), "", now); err == nil {
		t.Error("desafio sem carimbo foi assinado")
	}
	semCarimbo, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   uuid.NewString(),
		Audience:  jwt.ClaimStrings{"totp-challenge"},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
	}).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ParseChallengeToken(jwtSecret, semCarimbo, now); err == nil {
		t.Error("desafio sem carimbo foi aceito")
	}
}

// O carimbo é o SHA-256 do hash, e não o hash: o desafio viaja pelo cliente, e
// o hash de senha nunca sai do servidor.
func TestPasswordStampIsNotTheHash(t *testing.T) {
	hash := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$c29tYQ"
	c := auth.PasswordStamp(hash)
	if c == "" || strings.Contains(c, "argon2id") || strings.Contains(c, "c2FsdA") {
		t.Errorf("carimbo = %q: carrega o hash", c)
	}
	if c != auth.PasswordStamp(hash) || c == auth.PasswordStamp(hash+"x") {
		t.Error("o carimbo não é função só do hash")
	}
}

// O desafio dura só o tempo de digitar seis dígitos.
func TestChallengeTokenExpiresQuickly(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw, err := auth.NewChallengeToken(jwtSecret, uuid.New(), auth.PasswordStamp("x"), now)
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

	desafio, err := auth.NewChallengeToken(jwtSecret, userID, auth.PasswordStamp("x"), now)
	if err != nil {
		t.Fatal(err)
	}
	acesso, err := auth.NewAccessToken(jwtSecret, userID, uuid.New(), now)
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
