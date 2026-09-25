// Package auth cuida de senha, token de acesso e ciclo de vida de sessão.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Parâmetros da spec §5.2: acima do mínimo OWASP (19 MiB, t=2, p=1).
const (
	argonMemory  uint32 = 64 * 1024 // KiB, ou seja 64 MiB
	argonTime    uint32 = 3
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var ErrInvalidHash = errors.New("hash de senha malformado")

// Política de senha NOVA (D3 do Plano 5), da NIST 800-63B-4 §3.1.1.2: 15
// caracteres no mínimo, porque o segundo fator é opcional e a senha pode ser o
// único fator; aceitar ao menos 64; nenhuma regra de composição. O máximo de
// 128 dá folga a gerenciadores de senha e mantém o pior caso em 512 bytes.
//
// Vale para senha nova (vigilume user create, set-password, POST
// /auth/password). O login continua aceitando uma senha antiga mais curta.
const (
	MinPasswordLen = 15
	MaxPasswordLen = 128
)

// Motivos de recusa, no vocabulário fechado dos fields de erro da API.
const (
	ReasonTooShort = "too_short"
	ReasonTooLong  = "too_long"
)

// ValidateNewPassword devolve "" quando a senha serve, ou o motivo da recusa.
//
// Conta code points, e não bytes (NIST: cada code point é um caractere): "é"
// tem dois bytes e é um caractere. Não normaliza nem apara: espaço é caractere
// como outro qualquer, e a senha é verificada inteira, como foi digitada.
func ValidateNewPassword(plain string) string {
	switch n := utf8.RuneCountInString(plain); {
	case n < MinPasswordLen:
		return ReasonTooShort
	case n > MaxPasswordLen:
		return ReasonTooLong
	}
	return ""
}

// HashPassword gera um hash no formato PHC, com salt aleatório.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gerar salt: %w", err)
	}
	return HashPasswordWith(plain, salt, argonMemory, argonTime, argonThreads)
}

// HashPasswordWith é exportada para que os testes consigam construir hashes
// com parâmetros antigos e exercitar o caminho de rehash. O código de
// produção usa sempre HashPassword.
//
// Recusa os mesmos parâmetros que o decodeHash recusa ao ler (Plano 8), e
// salt vazio: argon2.IDKey entra em panic com t=0, p=0 ou memória abaixo de
// 8*p, e um hash assim nem seria lido de volta.
func HashPasswordWith(plain string, salt []byte, memory, time uint32, threads uint8) (string, error) {
	if !parametrosValidos(memory, time, threads) || len(salt) == 0 {
		return "", ErrInvalidHash
	}
	sum := argon2.IDKey([]byte(plain), salt, time, memory, threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// parametrosValidos é a faixa que argon2.IDKey aceita sem panic. É uma regra
// só para a escrita (HashPasswordWith) e a leitura (decodeHash), para que as
// duas não divirjam.
func parametrosValidos(memory, time uint32, threads uint8) bool {
	return time >= 1 && threads >= 1 && memory >= 8*uint32(threads)
}

// verificacoes conta os Argon2id de verificação (VerifyPassword, e por ela
// VerifyDummy). Ver Argon2Verifications.
var verificacoes atomic.Uint64

// Argon2Verifications devolve quantas verificações de senha com Argon2id o
// processo já fez. Existe para os testes que travam "todo caminho de falha
// paga um Argon2id" (e-mail inexistente, conta bloqueada, senha errada)
// contando verificações, e não medindo relógio: a medida de tempo oscilava
// sob carga no CI (Task 11 do Plano 5). É só leitura; não muda nada.
func Argon2Verifications() uint64 { return verificacoes.Load() }

// VerifyPassword confere a senha e informa se o hash precisa ser refeito.
//
// needsRehash permite subir o custo do Argon2 no futuro sem invalidar senha
// nenhuma: o login valida com os parâmetros gravados, e quem chama regrava o
// hash com os parâmetros atuais.
func VerifyPassword(encoded, plain string) (ok bool, needsRehash bool, err error) {
	memory, time, threads, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}

	// #nosec G115 -- want vem do decodeHash sobre um hash no formato PHC; o
	// comprimento é sempre argonKeyLen (32), e estourar uint32 exigiria um
	// hash de gigabytes, impedido pelo formato e pelo body limit.
	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	verificacoes.Add(1)

	// Comparação em tempo constante: bytes.Equal sai cedo na primeira
	// diferença e vazaria informação por tempo.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	needsRehash = memory < argonMemory || time < argonTime || threads < argonThreads
	return true, needsRehash, nil
}

func decodeHash(encoded string) (memory, time uint32, threads uint8, salt, sum []byte, err error) {
	// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}

	var version int
	if _, e := fmt.Sscanf(parts[2], "v=%d", &version); e != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); e != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	// argon2.IDKey entra em panic com t=0, p=0 ou memória abaixo de 8*p, e o
	// Sscanf aceita esses valores de bom grado. Isto é robustez, não fechar
	// buraco: chegar aqui exige escrita arbitrária em users.password_hash, e
	// quem tem isso grava o hash de uma senha que conhece e entra como quem
	// quiser — o panic seria um downgrade do ataque, e o middleware.Recover
	// já o converteria em 500. Recusar o hash é simplesmente a resposta
	// certa para um valor que não é um hash válido.
	if !parametrosValidos(memory, time, threads) {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if sum, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(sum) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	return memory, time, threads, salt, sum, nil
}

// dummyHash é calculado uma única vez, na primeira chamada. Calcular sob
// demanda evita pagar 64 MiB e três iterações no boot de todo processo,
// inclusive nos que rodam só como --role=prober.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("senha-inexistente-usada-apenas-para-tempo-constante")
	if err != nil {
		panic("auth: não foi possível preparar o hash dummy: " + err.Error())
	}
	return h
})

// VerifyDummy consome o mesmo trabalho de CPU de uma verificação real.
//
// O login chama esta função quando o e-mail não existe, para que a resposta
// custe o mesmo de uma senha errada. Sem isso, o tempo de resposta diz ao
// atacante quais e-mails têm conta (spec §5.2).
func VerifyDummy() {
	_, _, _ = VerifyPassword(dummyHash(), "qualquer-coisa")
}
