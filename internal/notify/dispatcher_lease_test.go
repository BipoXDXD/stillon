package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// Um dispatcher que travou além do lease perdeu a notificação: outro a
// reivindicou de novo e é o dono agora. A gravação atrasada do primeiro não
// pode sobrescrever o que o dono novo decidiu, nem mexer na saúde do canal.
func TestStaleOutcomeAfterLostLeaseIsRefused(t *testing.T) {
	for _, tc := range []struct {
		nome     string
		atrasado desfecho
	}{
		{"enviada", desfecho{status: statusSent}},
		{"retentativa", desfecho{status: statusPending, reason: ReasonServerError}},
		{"cancelada", desfecho{status: statusCancelled, reason: ReasonInvalidChannel}},
		{"falha definitiva", desfecho{status: statusFailed, reason: ReasonRejected}},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			id := b.enfileirar(t, incident.EventOpened)
			ctx := context.Background()

			velho, err := b.d.reivindicar(ctx)
			if err != nil || len(velho) != 1 {
				t.Fatalf("1ª reivindicação = %v, %v", velho, err)
			}
			b.clk.Advance(lease)
			novo, err := b.d.reivindicar(ctx)
			if err != nil || len(novo) != 1 {
				t.Fatalf("2ª reivindicação = %v, %v; queria a mesma notificação de volta", novo, err)
			}
			antes := b.estado(t, id)

			err = b.d.persistir(ctx, velho[0], tc.atrasado)
			if !errors.Is(err, errLeasePerdido) {
				t.Fatalf("persistir do dono antigo = %v; queria errLeasePerdido", err)
			}
			if depois := b.estado(t, id); depois.Status != antes.Status || depois.Attempts != antes.Attempts ||
				!depois.Proxima.Equal(antes.Proxima) || depois.Enviada != nil {
				t.Errorf("notificação = %+v; queria intacta como o dono novo a deixou (%+v)", depois, antes)
			}
			if n := b.linhasDeSaude(t); n != 0 {
				t.Errorf("channel_health tem %d linhas; o dono antigo não pode mexer na saúde do canal", n)
			}

			if err := b.d.persistir(ctx, novo[0], desfecho{status: statusSent}); err != nil {
				t.Errorf("persistir do dono novo = %v", err)
			}
		})
	}
}

// A tentativa devolvida pelo 429 do dono novo deixa attempts igual ao da
// reivindicação antiga: a cerca não pode ser o número de tentativas.
func TestStaleOutcomeIsRefusedEvenWhenAttemptsMatchAgain(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()

	velho, err := b.d.reivindicar(ctx)
	if err != nil || len(velho) != 1 {
		t.Fatalf("1ª reivindicação = %v, %v", velho, err)
	}
	b.clk.Advance(lease)
	novo, err := b.d.reivindicar(ctx)
	if err != nil || len(novo) != 1 {
		t.Fatalf("2ª reivindicação = %v, %v", novo, err)
	}
	devolvida := desfecho{status: statusPending, reason: ReasonRateLimited, proxima: b.clk.Now(), devolver: true}
	if err := b.d.persistir(ctx, novo[0], devolvida); err != nil {
		t.Fatalf("persistir do 429 do dono novo = %v", err)
	}
	if l := b.estado(t, id); l.Attempts != velho[0].Attempts {
		t.Fatalf("attempts = %d; o cenário precisa dele igual ao da 1ª reivindicação (%d)", l.Attempts, velho[0].Attempts)
	}

	if err := b.d.persistir(ctx, velho[0], desfecho{status: statusSent}); !errors.Is(err, errLeasePerdido) {
		t.Fatalf("persistir do dono antigo = %v; queria errLeasePerdido", err)
	}
	if l := b.estado(t, id); l.Status != "pending" || l.Enviada != nil {
		t.Errorf("notificação = %+v; queria pendente, sem envio", l)
	}
}

// linhasDeSaude conta channel_health sob o escopo prober: sem escopo, a RLS
// esconderia qualquer linha e o 0 seria verdade por acidente.
func (b *bancada) linhasDeSaude(t *testing.T) int {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_health`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
