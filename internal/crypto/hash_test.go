package crypto_test

import (
	"bytes"
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
