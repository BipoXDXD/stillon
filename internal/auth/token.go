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

func NewChallengeToken(secret []byte, userID uuid.UUID, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		Audience:  jwt.ClaimStrings{challengeAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ChallengeTTL)),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar desafio: %w", err)
	}
	return tok, nil
}

func ParseChallengeToken(secret []byte, raw string, now time.Time) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims

	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
		// Sem esta checagem, um access token comum seria aceito como desafio
		// e vice-versa.
		jwt.WithAudience(challengeAudience),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	return id, nil
}
