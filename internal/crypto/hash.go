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
//
// Entra em panic com chave fraca em vez de devolver erro: hmac.New aceita
// chave nil sem reclamar e devolve um HMAC com chave de zeros, que parece um
// hash legítimo e é justamente o hash enumerável que a §4.2 quis evitar. A
// chave vem da configuração, então chave curta é erro de programação — falhar
// alto na partida é melhor que gravar no banco uma coluna que não protege nada.
func HashIP(key []byte, ip string) []byte {
	if len(key) < KeySize {
		panic("crypto: HashIP exige chave de ao menos 32 bytes")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ip))
	return mac.Sum(nil)
}
