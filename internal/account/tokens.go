package account

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// Validades dos tokens de conta (spec): os dois são de uso único.
const (
	ValidadeDaConfirmacao = 24 * time.Hour
	ValidadeDaRedefinicao = time.Hour
)

// Propósitos gravados em account_tokens.purpose.
const (
	propositoConfirmacao = "confirm_email"
	propositoRedefinicao = "reset_password"
)

// ErrTokenInvalido: token inexistente, já usado ou vencido. Um erro só, para
// não contar a quem chuta qual dos três aconteceu.
var ErrTokenInvalido = errors.New("token inválido")

// bytesDoTokenDeConta são os 256 bits de entropia do token.
const bytesDoTokenDeConta = 32

// novoTokenDeConta gera o token que vai no link e o hash que vai para o banco:
// 32 bytes de crypto/rand em base64url sem padding, e o SHA-256 dele
// (crypto.HashToken). O token em si nunca é gravado.
func novoTokenDeConta() (token string, hash []byte, err error) {
	cru := make([]byte, bytesDoTokenDeConta)
	if _, err := rand.Read(cru); err != nil {
		return "", nil, fmt.Errorf("gerar token de conta: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(cru)
	return token, crypto.HashToken(token), nil
}
