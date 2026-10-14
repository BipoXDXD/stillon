package auth_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// ocuparArgon2 enche o semáforo do Argon2id com capacidade n e devolve as n
// vagas presas; o Cleanup as solta e volta à capacidade padrão.
func ocuparArgon2(t *testing.T, n int) []func() {
	t.Helper()
	auth.SetArgon2MaxConcurrent(n)
	var vagas []func()
	t.Cleanup(func() {
		for _, soltar := range vagas {
			soltar()
		}
		auth.SetArgon2MaxConcurrent(auth.DefaultArgon2MaxConcurrent())
	})
	for range n {
		soltar, err := auth.ReserveArgon2(context.Background())
		if err != nil {
			t.Fatalf("reservar vaga %d de %d: %v", len(vagas)+1, n, err)
		}
		vagas = append(vagas, soltar)
	}
	return vagas
}

// A1 (Plano 10, §3.1): TODA chamada do Argon2id passa pelo semáforo — o hash,
// a verificação e o VerifyDummy do e-mail inexistente (senão o tempo deixaria
// de ser o mesmo com ou sem conta). Com as vagas presas, cada caminho desiste
// no prazo com ErrArgon2Busy sem rodar o Argon2id; solta uma vaga, o mesmo
// caminho passa.
func TestEveryArgon2PathWaitsForASlot(t *testing.T) {
	h, err := auth.HashPassword(context.Background(), senha)
	if err != nil {
		t.Fatal(err)
	}
	auth.VerifyDummy(context.Background()) // prepara o hash dummy fora do teste (ele é calculado uma vez)
	auth.EncurtarEsperaDoArgon2(t, 50*time.Millisecond)
	vagas := ocuparArgon2(t, 2)

	antes := auth.Argon2Verifications()
	caminhos := map[string]func() error{
		"HashPassword": func() error { _, err := auth.HashPassword(context.Background(), senha); return err },
		"HashPasswordWith": func() error {
			_, err := auth.HashPasswordWith(senha, []byte("saltsaltsaltsalt"), 64*1024, 3, 2)
			return err
		},
		"VerifyPassword": func() error { _, _, err := auth.VerifyPassword(context.Background(), h, senha); return err },
		"VerifyDummy":    func() error { return auth.VerifyDummy(context.Background()) },
	}
	for nome, chamar := range caminhos {
		inicio := time.Now()
		if err := chamar(); !errors.Is(err, auth.ErrArgon2Busy) {
			t.Errorf("%s com o semáforo cheio: erro = %v, queria ErrArgon2Busy", nome, err)
		}
		if d := time.Since(inicio); d < 50*time.Millisecond {
			t.Errorf("%s desistiu em %v, antes do prazo de 50ms", nome, d)
		}
	}
	if n := auth.Argon2Verifications() - antes; n != 0 {
		t.Errorf("%d Argon2id de verificação rodaram com o semáforo cheio", n)
	}

	vagas[0]() // o Cleanup solta de novo: soltar é idempotente
	ok, _, err := auth.VerifyPassword(context.Background(), h, senha)
	if err != nil || !ok {
		t.Errorf("com uma vaga livre: ok=%v err=%v, queria a senha aceita", ok, err)
	}
}

// emBolha roda f numa bolha do synctest com o semáforo do Argon2id criado
// DENTRO dela (capacidade n), para que a espera por vaga conte como
// bloqueio durável e synctest.Wait saiba quando a goroutine está na fila.
// O canal da bolha não pode sobreviver a ela: o semáforo padrão é recriado
// fora, no Cleanup do teste de fora.
func emBolha(t *testing.T, n int, f func(t *testing.T)) {
	t.Helper()
	t.Cleanup(func() { auth.SetArgon2MaxConcurrent(auth.DefaultArgon2MaxConcurrent()) })
	synctest.Test(t, func(t *testing.T) {
		auth.SetArgon2MaxConcurrent(n)
		f(t)
	})
}

// reservarNaBolha toma uma vaga do semáforo da bolha e devolve quem a solta.
func reservarNaBolha(t *testing.T) func() {
	t.Helper()
	soltar, err := auth.ReserveArgon2(context.Background())
	if err != nil {
		t.Fatalf("reservar a vaga da bolha: %v", err)
	}
	return soltar
}

// Quem espera ganha a vaga que se solta dentro do prazo: o semáforo é fila
// curta, não recusa imediata. synctest.Wait garante que a verificação já
// está parada na fila quando a vaga é solta; sem isso, uma goroutine atrasada
// pegaria a vaga livre pelo caminho rápido e o teste passaria sem provar a
// espera.
func TestArgon2WaiterGetsTheReleasedSlot(t *testing.T) {
	h, err := auth.HashPassword(context.Background(), senha)
	if err != nil {
		t.Fatal(err)
	}
	emBolha(t, 1, func(t *testing.T) {
		soltar := reservarNaBolha(t)
		defer soltar() // num Fatal, a goroutine presa na fila termina

		fim := make(chan error, 1)
		go func() {
			_, _, err := auth.VerifyPassword(context.Background(), h, senha)
			fim <- err
		}()
		synctest.Wait()
		select {
		case err := <-fim:
			t.Fatalf("a verificação terminou com o semáforo cheio, sem esperar a vaga: %v", err)
		default:
		}

		soltar()
		if err := <-fim; err != nil {
			t.Errorf("a verificação que esperou a vaga falhou: %v", err)
		}
	})
}

// M1 da revisão do Plano 10 (auth): quem espera a vaga com o contexto da
// requisição desiste quando ele é cancelado (o cliente foi embora), e não
// segura a goroutine na fila até o fim do prazo. O erro é ErrArgon2Busy (a
// API responde como ao semáforo cheio, sem contar prova) e também o do
// contexto. No relógio da bolha, desistir sem o tempo andar só pode ser o ctx.
func TestArgon2WaitStopsWhenTheContextIsCanceled(t *testing.T) {
	emBolha(t, 1, func(t *testing.T) {
		soltarOcupada := reservarNaBolha(t)
		defer soltarOcupada()

		ctx, cancelar := context.WithCancel(context.Background())
		fim := make(chan error, 1)
		inicio := time.Now()
		go func() {
			soltar, err := auth.ReserveArgon2(ctx)
			soltar()
			fim <- err
		}()
		synctest.Wait()
		select {
		case err := <-fim:
			t.Fatalf("a espera terminou antes do cancelamento: %v", err)
		default:
		}

		cancelar()
		err := <-fim
		if !errors.Is(err, auth.ErrArgon2Busy) || !errors.Is(err, context.Canceled) {
			t.Errorf("erro = %v, queria ErrArgon2Busy e context.Canceled", err)
		}
		if d := time.Since(inicio); d != 0 {
			t.Errorf("desistiu depois de %v no relógio da bolha, queria no cancelamento", d)
		}

		// Um contexto já cancelado com vaga livre: a vaga é tomada (o select
		// prefere a vaga livre), porque a verificação custa o mesmo que desistir.
		soltarOcupada()
		soltar, err := auth.ReserveArgon2(ctx)
		if err != nil {
			t.Errorf("com vaga livre e o ctx cancelado: %v, queria a vaga", err)
		}
		soltar()
	})
}

// Capacidade: o padrão é min(NumCPU, 4); a faixa aceita é 1–64, e o que
// passar dela é trazido para dentro (a config já recusa antes).
func TestArgon2CapacityDefaultsAndClamps(t *testing.T) {
	t.Cleanup(func() { auth.SetArgon2MaxConcurrent(auth.DefaultArgon2MaxConcurrent()) })
	if got, quer := auth.DefaultArgon2MaxConcurrent(), min(runtime.NumCPU(), 4); got != quer {
		t.Errorf("padrão = %d, queria min(NumCPU, 4) = %d", got, quer)
	}
	for _, c := range []struct{ pedido, quer int }{{0, 1}, {1, 1}, {7, 7}, {64, 64}, {100, 64}} {
		auth.SetArgon2MaxConcurrent(c.pedido)
		if got := auth.Argon2MaxConcurrent(); got != c.quer {
			t.Errorf("SetArgon2MaxConcurrent(%d): capacidade %d, queria %d", c.pedido, got, c.quer)
		}
	}
}
