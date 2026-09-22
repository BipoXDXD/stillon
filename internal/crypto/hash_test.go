package crypto_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

func TestHashTokenIsDeterministicAndFixedLength(t *testing.T) {
	a := crypto.HashToken("abc")
	b := crypto.HashToken("abc")
	c := crypto.HashToken("abd")

	if !bytes.Equal(a, b) {
		t.Error("HashToken não é determinístico")
	}
	if bytes.Equal(a, c) {
		t.Error("entradas diferentes produziram o mesmo hash")
	}
	if len(a) != 32 {
		t.Errorf("hash tem %d bytes, queria 32", len(a))
	}
}

// IP tem espaço de busca minúsculo (2^32 para IPv4): um SHA-256 puro seria
// revertido por enumeração em minutos. Por isso o IP usa hash com chave, e o
// token — que tem 256 bits de entropia — não precisa.
func TestHashIPDependsOnTheKey(t *testing.T) {
	k1 := bytes.Repeat([]byte{1}, 32)
	k2 := bytes.Repeat([]byte{2}, 32)

	if bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k2, "203.0.113.9")) {
		t.Error("chaves diferentes produziram o mesmo hash de IP")
	}
	if !bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k1, "203.0.113.9")) {
		t.Error("HashIP não é determinístico com a mesma chave")
	}
	if bytes.Equal(crypto.HashIP(k1, "203.0.113.9"), crypto.HashIP(k1, "203.0.113.10")) {
		t.Error("IPs diferentes produziram o mesmo hash")
	}
}

// Known-answer tests travam o algoritmo. Esses bytes vão para colunas
// persistidas (sessions.token_hash, ip_hash): trocar de algoritmo invalidaria
// em silêncio toda sessão existente, sem nenhum erro visível.
func TestHashTokenMatchesKnownVector(t *testing.T) {
	// SHA-256("abc"), vetor publicado (FIPS 180-2, apêndice B.1).
	queria := mustHex(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")

	if got := crypto.HashToken("abc"); !bytes.Equal(got, queria) {
		t.Errorf("HashToken(\"abc\") = %x, queria %x", got, queria)
	}
}

func TestHashIPMatchesKnownVector(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	// HMAC-SHA-256 de "203.0.113.9" com chave de 32 bytes 0x01, conferido
	// fora do Go:
	//   printf '203.0.113.9' | openssl dgst -sha256 -mac HMAC -macopt hexkey:0101...01
	queria := mustHex(t, "2925ba1ba18520e256ff87a48739fe6b667649f20e9d7e8a5e0aa1c9e5f136ba")

	got := crypto.HashIP(key, "203.0.113.9")
	if !bytes.Equal(got, queria) {
		t.Errorf("HashIP = %x, queria %x", got, queria)
	}
	if len(got) != 32 {
		t.Errorf("hash tem %d bytes, queria 32", len(got))
	}
}

// Uma chave vazia não produz erro em hmac.New: produz um HMAC com chave de
// zeros, indistinguível de um hash legítimo à inspeção. Como é sempre erro de
// programação (a chave vem da configuração), a resposta certa é panic na
// partida, não um hash enumerável gravado no banco.
func TestHashIPPanicsOnWeakKey(t *testing.T) {
	casos := map[string][]byte{
		"chave nil":   nil,
		"chave vazia": {},
		"chave curta": bytes.Repeat([]byte{1}, 31),
	}

	for nome, key := range casos {
		t.Run(nome, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("HashIP aceitou a chave sem entrar em panic")
				}
			}()
			crypto.HashIP(key, "203.0.113.9")
		})
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
