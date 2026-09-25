package targets_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// encherAte cria n alvos do dono direto no banco (o Create inteiro, n vezes,
// seria lento à toa).
func (a ambiente) encherAte(t *testing.T, dono uuid.UUID, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $2::int) i`, dono, n); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// O 100º entra; o 101º é ErrLimitReached; o teto é por usuário.
//
// Duas barreiras fazem a contagem ser por usuário: o WHERE user_id de
// CountTargets e a RLS da InUserTx. Sabotar só uma deixa o teste verde; o
// relatório da Task 8 registra a sabotagem das duas juntas.
func TestCreateStopsAtTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	if _, err := a.svc.Create(ctx, dono, entrada()); err != nil {
		t.Fatalf("o 100º alvo foi recusado: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, entrada()); !errors.Is(err, targets.ErrLimitReached) {
		t.Fatalf("o 101º alvo deu %v, queria ErrLimitReached", err)
	}
	if _, err := a.svc.Create(ctx, outro, entrada()); err != nil {
		t.Errorf("o teto de um usuário barrou outro: %v", err)
	}
}

// Dois POST simultâneos com 99 alvos: um entra, o outro bate no teto. O gancho
// segura cada criação entre a contagem e a inserção até as duas chegarem ali,
// ou por meio segundo: com a trava, a segunda nem conta antes de a primeira
// comitar; sem ela, as duas contam 99 e as duas inserem.
func TestConcurrentCreatesRespectTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	a.svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = a.svc.Create(context.Background(), dono, entrada())
		}()
	}
	wg.Wait()

	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != int(targets.MaxTargetsPerUser) {
		t.Errorf("%d alvos depois de dois POST simultâneos (erros: %v), queria %d", n, erros, targets.MaxTargetsPerUser)
	}
}
