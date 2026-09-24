package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// NewCSPNonce sorteia o nonce de uma resposta com CSP: 128 bits de
// crypto/rand, em base64 de URL sem padding.
//
// A página pública (/p/{slug}) e o painel usam esta mesma função. Duas
// cópias de gerador de nonce divergem, e o defeito que já aconteceu foi
// exatamente num gerador: com a base64 padrão, o "+" sai do html/template
// como "&#43;", e o atributo deixa de trazer o nonce literal da CSP (D26 do
// Plano 4). A gramática de nonce da CSP aceita "-" e "_", e nenhum dos dois
// precisa de escape em atributo HTML.
func NewCSPNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
