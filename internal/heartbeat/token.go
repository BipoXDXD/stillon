package heartbeat

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// tokenBytes é o tamanho do token de ping: 256 bits de crypto/rand, como o de
// verificação de canal.
const tokenBytes = 32

// encodedTokenLength é o tamanho do token em base64url sem preenchimento.
var encodedTokenLength = base64.RawURLEncoding.EncodedLen(tokenBytes)

// NewPingToken gera o token e o hash que vai para o banco. O token só existe
// no retorno: quem chama o entrega uma vez, e ele não fica guardado.
func NewPingToken() (token string, hash []byte) {
	b := make([]byte, tokenBytes)
	// No Go 1.26, Read preenche tudo ou encerra o processo se não houver entropia.
	_, _ = rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, crypto.HashToken(token)
}

// HashPingToken devolve o hash de um token recebido na URL. false para o que
// não tem o formato de um token: nem vai ao banco.
func HashPingToken(token string) ([]byte, bool) {
	if len(token) != encodedTokenLength {
		return nil, false
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return nil, false
	}
	return crypto.HashToken(token), true
}
