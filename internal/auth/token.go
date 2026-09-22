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

func NewAccessToken(secret []byte, userID uuid.UUID, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("assinar token: %w", err)
	}
	return tok, nil
}

func ParseAccessToken(secret []byte, raw string, now time.Time) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims

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
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject não é UUID", ErrInvalidToken)
	}
	return id, nil
}
