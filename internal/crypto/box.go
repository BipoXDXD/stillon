package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	// ErrMalformed indica entrada curta demais para conter nonce e tag.
	ErrMalformed = errors.New("ciphertext malformado")
	// ErrAuthFailed cobre chave errada, AAD errado e adulteração. Os três
	// casos devolvem o mesmo erro de propósito: distinguir entre eles daria
	// a um atacante um oráculo.
	ErrAuthFailed = errors.New("falha na autenticação do ciphertext")
)

// KeySize é o tamanho exigido da chave mestra: AES-256.
const KeySize = 32

// Box cifra e decifra segredos em repouso com AES-256-GCM (spec §5.4).
type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("chave deve ter %d bytes, tem %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("construir AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construir GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal devolve nonce || ciphertext || tag.
//
// O nonce vai prefixado porque GCM exige um nonce distinto por operação com a
// mesma chave, e guardá-lo junto do ciphertext é o que permite decifrar sem
// uma coluna extra.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("gerar nonce: %w", err)
	}
	// O primeiro argumento é o destino: passar nonce faz o resultado já sair
	// com o prefixo, sem cópia extra.
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Open(sealed, aad []byte) (Secret, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns+b.aead.Overhead() {
		return nil, ErrMalformed
	}
	nonce, ct := sealed[:ns], sealed[ns:]

	pt, err := b.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return Secret(pt), nil
}

// AAD amarra um ciphertext à célula exata que o armazena: tabela, coluna e id
// da linha, no formato table || ':' || column || ':' || id. Com isso, mover o
// valor de um registro para outro — ou de uma coluna cifrada para outra da
// mesma linha — invalida a autenticação, mesmo que o atacante tenha acesso de
// escrita ao banco.
//
// A coluna entra desde já porque este é um caminho de mão única: acrescentar
// um componente ao AAD depois que houver ciphertext gravado exigiria re-cifrar
// tudo. Hoje custa um parâmetro.
//
// O id tem 16 bytes fixos no fim, então não há ambiguidade de fronteira mesmo
// que um nome de tabela ou de coluna contenha ':'.
func AAD(table, column string, id uuid.UUID) []byte {
	b := make([]byte, 0, len(table)+1+len(column)+1+len(id))
	b = append(b, table...)
	b = append(b, ':')
	b = append(b, column...)
	b = append(b, ':')
	return append(b, id[:]...)
}
