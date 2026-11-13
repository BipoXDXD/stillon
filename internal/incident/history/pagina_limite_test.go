package history_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Dentro de 1..100 vale o limite pedido; fora, o padrão de 50. 101
// incidentes deixam ver as duas pontas da faixa. A API já recusa limite fora
// da faixa (paginaDe), então este é o piso do serviço para quem o chama
// direto.
func TestListHonraOLimiteNaFaixaEVoltaAoPadraoForaDela(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	admin, err := storage.NewPool(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause)
		SELECT gen_random_uuid(), $1, $2, $3, 'dns' FROM generate_series(1, 101)`,
		alvo, agora, agora.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		limite int32
		quer   int
	}{
		{1, 1}, {7, 7}, {100, 100}, {101, 50}, {0, 50}, {-1, 50},
	} {
		got, err := b.svc.List(ctx, dono, history.Filter{Limit: c.limite})
		if err != nil {
			t.Fatalf("List(limite %d): %v", c.limite, err)
		}
		if len(got) != c.quer {
			t.Errorf("List(limite %d) devolveu %d, queria %d", c.limite, len(got), c.quer)
		}
	}
}
