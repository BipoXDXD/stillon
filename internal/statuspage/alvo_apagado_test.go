package statuspage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// Um alvo apagado enquanto o POST ou o PATCH grava os itens vira erro do
// item, não 500: a conferência de posse trava os alvos, e a remoção que
// chega antes faz o alvo sumir da conferência.
func TestTargetDeletedWhileSavingItemsIsAnItemError(t *testing.T) {
	casos := []struct {
		nome   string
		gravar func(a ambiente, dono uuid.UUID, itens []statuspage.Item) error
	}{
		{"POST", func(a ambiente, dono uuid.UUID, itens []statuspage.Item) error {
			_, err := a.svc.Create(context.Background(), dono, statuspage.Input{Slug: "empresa", Title: "Empresa", Items: itens})
			return err
		}},
		{"PATCH", func(a ambiente, dono uuid.UUID, itens []statuspage.Item) error {
			ctx := context.Background()
			p, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "Empresa"})
			if err != nil {
				return err
			}
			_, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Items: &itens})
			return err
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			ctx := context.Background()
			dono := a.usuario(t)
			alvo := a.alvo(t, dono, "Site")

			// Outra transação apaga o alvo e segura o commit.
			outra, err := a.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer outra.Rollback(ctx)
			if _, err := outra.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := outra.Exec(ctx, `DELETE FROM targets WHERE id = $1`, alvo); err != nil {
				t.Fatal(err)
			}
			var pidOutra int32
			if err := outra.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pidOutra); err != nil {
				t.Fatal(err)
			}

			fim := make(chan error, 1)
			go func() { fim <- c.gravar(a, dono, []statuspage.Item{{TargetID: alvo, DisplayName: "Site"}}) }()
			esperarBloqueioPor(t, a, pidOutra)
			if err := outra.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			err = <-fim
			if f := campos(err); len(f) != 1 || f[0] != (statuspage.FieldError{Field: "items[0].target_id", Reason: "invalid"}) {
				t.Errorf("gravar com o alvo apagado no meio = %v; queria items[0].target_id/invalid", err)
			}
		})
	}
}

// esperarBloqueioPor espera até alguma sessão estar parada numa trava da
// sessão pid.
func esperarBloqueioPor(t *testing.T, a ambiente, pid int32) {
	t.Helper()
	prazo := time.Now().Add(5 * time.Second)
	for {
		var bloqueadas int
		if err := a.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid))`, pid).Scan(&bloqueadas); err != nil {
			t.Fatal(err)
		}
		if bloqueadas > 0 {
			return
		}
		if time.Now().After(prazo) {
			t.Fatal("ninguém esperou pela transação que apaga o alvo")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
