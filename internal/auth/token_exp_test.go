package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// Um token sem exp, mesmo assinado com o segredo certo, valeria para sempre:
// o jwt/v5 só confere o exp quando ele está presente. Os dois tipos o exigem.
func TestTokensWithoutExpirationAreRejected(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	assinar := func(claims jwt.MapClaims) string {
		t.Helper()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	acesso := assinar(jwt.MapClaims{"sub": uuid.NewString(), "iat": now.Unix(), "sid": uuid.NewString()})
	if _, err := auth.ParseAccessToken(jwtSecret, acesso, now); err == nil {
		t.Error("access token sem exp foi aceito")
	}

	desafio := assinar(jwt.MapClaims{
		"sub": uuid.NewString(), "iat": now.Unix(), "aud": "totp-challenge",
		"pws": "carimbo", "cat": now.UnixMicro(),
	})
	if _, err := auth.ParseChallengeToken(jwtSecret, desafio, now); err == nil {
		t.Error("desafio sem exp foi aceito")
	}
}
