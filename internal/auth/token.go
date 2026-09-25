package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AccessTTL vem da spec §5.2. Curto de propósito: o refresh rotacionado é
// quem carrega a sessão longa.
const AccessTTL = 15 * time.Minute

var ErrInvalidToken = errors.New("token de acesso inválido")

// AccessClaims é o que um access token válido diz: quem é o usuário e de qual
// família de refresh (sessão) ele saiu.
type AccessClaims struct {
	UserID uuid.UUID
	// FamilyID é o claim sid (o "Session ID" registrado na IANA pelo OpenID
	// Connect). É o que permite a GET /sessions marcar a sessão atual e às
	// rotas de credencial recusar um token cuja sessão já foi encerrada (D2 do
	// Plano 5): o cookie de refresh, com Path=/api/v1/auth, não chega lá.
	FamilyID uuid.UUID
}

type accessJWT struct {
	jwt.RegisteredClaims
	SID string `json:"sid"`
}

// NewAccessToken recusa família nula: todo access token sai de uma sessão, e um
// token sem sid seria recusado por ParseAccessToken logo adiante.
func NewAccessToken(secret []byte, userID, familyID uuid.UUID, now time.Time) (string, error) {
	if familyID == uuid.Nil {
		return "", errors.New("assinar token: família de sessão nula")
	}
	claims := accessJWT{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
		},
		SID: familyID.String(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar token: %w", err)
	}
	return tok, nil
}

func ParseAccessToken(secret []byte, raw string, now time.Time) (AccessClaims, error) {
	var claims accessJWT

	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		// Trava contra confusão de algoritmo (ruling R30). Sem esta opção,
		// um token forjado com alg=HS512 e assinado com o MESMO segredo é
		// aceito: a biblioteca usa o alg do cabeçalho, que é escolha do
		// atacante. Não é o alg=none que esta opção impede — esse já morre
		// no Verify da própria biblioteca, porque o keyfunc devolve []byte
		// em vez da constante UnsafeAllowNoneSignatureType.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// Relógio injetado para que o teste de expiração não dependa do
		// tempo real da máquina.
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return AccessClaims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	// Access token não carrega audience; se veio uma, é um token de outro
	// propósito — em especial o desafio de TOTP, que é entregue depois da
	// senha e ANTES do segundo fator. Sem esta checagem ele valeria como
	// credencial completa e o segundo fator viraria decoração.
	if len(claims.Audience) > 0 {
		return AccessClaims{}, fmt.Errorf("%w: audience inesperada", ErrInvalidToken)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return AccessClaims{}, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	// Sem sid, o token não diz de qual sessão veio, e as rotas de credencial
	// não teriam como recusá-lo depois de a sessão ser encerrada. Não há token
	// assim em circulação: o painel ainda não foi publicado (D2 do Plano 5).
	familia, err := uuid.Parse(claims.SID)
	if err != nil || familia == uuid.Nil {
		return AccessClaims{}, fmt.Errorf("%w: sid ausente ou inválido", ErrInvalidToken)
	}
	return AccessClaims{UserID: id, FamilyID: familia}, nil
}

// challengeAudience separa o token de desafio do access token. Sem essa
// separação, o token entregue após a senha — antes do segundo fator — valeria
// como credencial completa, e o TOTP viraria decoração.
const challengeAudience = "totp-challenge"

// ChallengeTTL é curto: é só o tempo de digitar seis dígitos.
const ChallengeTTL = 5 * time.Minute

type challengeJWT struct {
	jwt.RegisteredClaims
	// PWS é o PasswordStamp do hash que conferiu com a senha do login. O
	// /totp/verify o entrega a Issue, que recusa a sessão se a senha mudou
	// depois do desafio: sem isto, um desafio obtido com a senha antiga abria
	// sessão até cinco minutos depois de um set-password (Task 11 do Plano 5).
	PWS string `json:"pws"`
	// CAT é o instante de emissão em microssegundos Unix (Plano 8). É a chave
	// da marca d'água que torna o desafio de uso único: o iat não serve,
	// porque jwt.NewNumericDate o arredonda para segundos, e dois desafios do
	// mesmo usuário no mesmo segundo teriam a mesma marca. Microssegundo é a
	// resolução do timestamptz, então o valor volta do banco sem perda.
	CAT int64 `json:"cat"`
}

// Challenge é o que um desafio válido diz: de quem é, com qual senha foi
// obtido e quando foi emitido.
type Challenge struct {
	UserID uuid.UUID
	// Stamp é o carimbo da senha verificada no login (PasswordStamp).
	Stamp string
	// IssuedAt é o instante de emissão, com resolução de microssegundo: é o
	// valor que o /totp/verify grava como marca d'água ao consumir o desafio.
	IssuedAt time.Time
}

// NewChallengeToken recusa carimbo vazio: o desafio só sai depois de uma
// senha verificada, e sem o carimbo o /totp/verify não teria como conferir
// que ela ainda vale.
func NewChallengeToken(secret []byte, userID uuid.UUID, stamp string, now time.Time) (string, error) {
	if stamp == "" {
		return "", errors.New("assinar desafio: sem o carimbo da senha verificada")
	}
	claims := challengeJWT{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{challengeAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ChallengeTTL)),
		},
		PWS: stamp,
		CAT: now.UnixMicro(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar desafio: %w", err)
	}
	return tok, nil
}

// ParseChallengeToken devolve o desafio: o usuário, o carimbo da senha
// verificada no login e o instante de emissão. Desafio sem carimbo ou sem
// instante é inválido; um desafio emitido antes do Plano 8 não tem instante e
// morre com o deploy (no pior caso, um login refeito dentro de ChallengeTTL).
func ParseChallengeToken(secret []byte, raw string, now time.Time) (Challenge, error) {
	var claims challengeJWT

	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
		// Sem esta checagem, um access token comum seria aceito como desafio
		// e vice-versa.
		jwt.WithAudience(challengeAudience),
	)
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	if claims.PWS == "" {
		return Challenge{}, fmt.Errorf("%w: desafio sem carimbo de senha", ErrInvalidToken)
	}
	if claims.CAT <= 0 {
		return Challenge{}, fmt.Errorf("%w: desafio sem instante de emissão", ErrInvalidToken)
	}
	return Challenge{UserID: id, Stamp: claims.PWS, IssuedAt: time.UnixMicro(claims.CAT).UTC()}, nil
}
