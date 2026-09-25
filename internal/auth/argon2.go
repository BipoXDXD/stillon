package auth

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// O teto de Argon2id simultâneos (A1 do Plano 10). Cada Argon2id custa 64 MiB
// (argonMemory) por ~120 ms, e o limitador por IP limita a vazão de cada
// origem, não a concorrência total: vinte logins simultâneos de duas origens
// passavam de 1 GiB, e o processo morria por falta de memória, com o sondador
// junto no --role=all. O semáforo envolve TODA chamada do Argon2id — hash,
// verificação e o VerifyDummy do e-mail inexistente, para que o tempo continue
// o mesmo com ou sem conta.
//
// Quem não consegue vaga em Argon2Wait recebe ErrArgon2Busy, e a API responde
// 429 rate_limited com Retry-After. Os parâmetros do Argon2id não mudam: os
// hashes gravados continuam valendo.

// ErrArgon2Busy: o teto de Argon2id simultâneos ficou cheio por Argon2Wait.
var ErrArgon2Busy = errors.New("auth: verificações de senha demais ao mesmo tempo")

const (
	// Argon2Wait é quanto uma chamada espera por uma vaga antes de desistir.
	// A API o anuncia em Retry-After.
	Argon2Wait = 2 * time.Second
	// MaxArgon2Concurrent é o teto aceito para ARGON2_MAX_CONCURRENT: 64
	// Argon2id simultâneos já são 4 GiB.
	MaxArgon2Concurrent = 64
)

// esperaArgon2 é Argon2Wait; os testes do pacote a encurtam.
var esperaArgon2 = Argon2Wait

// DefaultArgon2MaxConcurrent é min(NumCPU, 4): mais vagas que núcleos não
// deixa nada mais rápido, só usa mais memória; e 4 vagas são 256 MiB.
func DefaultArgon2MaxConcurrent() int { return min(runtime.NumCPU(), 4) }

var vagasArgon2 atomic.Pointer[chan struct{}]

func init() { SetArgon2MaxConcurrent(DefaultArgon2MaxConcurrent()) }

// SetArgon2MaxConcurrent troca o teto (1 a MaxArgon2Concurrent; fora disso,
// o valor é trazido para dentro da faixa). É chamada na montagem das rotas
// (ARGON2_MAX_CONCURRENT). Uma vaga reservada antes da troca é devolvida ao
// semáforo de onde saiu.
func SetArgon2MaxConcurrent(n int) {
	n = min(max(n, 1), MaxArgon2Concurrent)
	vagas := make(chan struct{}, n)
	vagasArgon2.Store(&vagas)
}

// Argon2MaxConcurrent é o teto em vigor.
func Argon2MaxConcurrent() int { return cap(*vagasArgon2.Load()) }

// ReserveArgon2 toma uma vaga do semáforo, esperando até Argon2Wait. A função
// devolvida solta a vaga (chamá-la de novo não faz nada). Sem vaga no prazo,
// devolve ErrArgon2Busy e uma função que não faz nada.
//
// A espera também para quando ctx é cancelado (M1 da revisão do Plano 10): o
// cliente que desistiu não segura uma goroutine na fila pelo prazo inteiro. O
// erro é ErrArgon2Busy, para quem chama responder como ao semáforo cheio (sem
// verificar nem contar nada), e também o erro do ctx. Uma vaga livre é tomada
// mesmo com ctx já cancelado: o caminho rápido não olha o ctx.
//
// Exportada para que os testes de fora do pacote segurem vagas; o código de
// produção só a usa por HashPassword, VerifyPassword e VerifyDummy.
func ReserveArgon2(ctx context.Context) (release func(), err error) {
	vagas := *vagasArgon2.Load()
	soltar := sync.OnceFunc(func() { <-vagas })
	select {
	case vagas <- struct{}{}:
		return soltar, nil
	default:
	}
	prazo := time.NewTimer(esperaArgon2)
	defer prazo.Stop()
	select {
	case vagas <- struct{}{}:
		return soltar, nil
	case <-prazo.C:
		return func() {}, ErrArgon2Busy
	case <-ctx.Done():
		return func() {}, fmt.Errorf("%w: %w", ErrArgon2Busy, ctx.Err())
	}
}
