package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

// HashToken é o que vai para a coluna sessions.token_hash (spec §5.2).
//
// SHA-256 puro basta aqui: o token tem 32 bytes de crypto/rand, ou seja 256
// bits de entropia, o que torna busca exaustiva inviável. Chave não
// acrescentaria segurança e acrescentaria uma chave para gerenciar.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// HashIP é o que vai para as colunas ip_hash (spec §4.2).
//
// Aqui a chave é obrigatória. O espaço de endereços IPv4 tem 2^32 elementos:
// um hash sem chave seria revertido por enumeração exaustiva em minutos, e a
// coluna deixaria de proteger qualquer coisa.
func HashIP(key []byte, ip string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ip))
	return mac.Sum(nil)
}
