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

// DefaultArgon2MaxConcurrent é min(NumCPU, 4): mais vagas que núcleos não
// deixa nada mais rápido, só usa mais memória; e 4 vagas são 256 MiB.
func DefaultArgon2MaxConcurrent() int { return min(runtime.NumCPU(), 4) }

// semaforoArgon2 junta as vagas e o prazo de espera por uma delas. O prazo é
// fixado quando o semáforo é criado, junto com a capacidade: quem reserva lê
// os dois do mesmo valor, e trocar o prazo é trocar o semáforo inteiro.
type semaforoArgon2 struct {
	vagas  chan struct{}
	espera time.Duration
}

// argon2Atual é o semáforo em vigor. O teto vale para o processo inteiro (é a
// memória do processo que ele protege), por isso é um só.
var argon2Atual atomic.Pointer[semaforoArgon2]

func init() { SetArgon2MaxConcurrent(DefaultArgon2MaxConcurrent()) }

// SetArgon2MaxConcurrent troca o teto (1 a MaxArgon2Concurrent; fora disso,
// o valor é trazido para dentro da faixa). É chamada na montagem das rotas
// (ARGON2_MAX_CONCURRENT). Uma vaga reservada antes da troca é devolvida ao
// semáforo de onde saiu.
func SetArgon2MaxConcurrent(n int) { trocarSemaforoArgon2(n, Argon2Wait) }

// trocarSemaforoArgon2 instala um semáforo novo com n vagas (trazido para a
// faixa 1 a MaxArgon2Concurrent) e o prazo espera.
func trocarSemaforoArgon2(n int, espera time.Duration) {
	n = min(max(n, 1), MaxArgon2Concurrent)
	argon2Atual.Store(&semaforoArgon2{vagas: make(chan struct{}, n), espera: espera})
}

// Argon2MaxConcurrent é o teto em vigor.
func Argon2MaxConcurrent() int { return cap(argon2Atual.Load().vagas) }

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
	sem := argon2Atual.Load()
	vagas := sem.vagas
	soltar := sync.OnceFunc(func() { <-vagas })
	select {
	case vagas <- struct{}{}:
		return soltar, nil
	default:
	}
	prazo := time.NewTimer(sem.espera)
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
