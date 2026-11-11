package prober

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// incidenteAberto cria um incidente aberto do alvo, sem avisar ninguém.
func (b *bancada) incidenteAberto(t *testing.T, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(context.Background(), b.pool, func(q *db.Queries) error {
		return q.OpenIncident(context.Background(), db.OpenIncidentParams{
			ID: id, TargetID: alvo, StartedAt: b.clk.Now(), Cause: string(KindTimeout),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) comoQueries(t *testing.T, fn func(q *db.Queries) error) error {
	t.Helper()
	return storage.InProberTx(context.Background(), b.pool, fn)
}

// Marca e enfileira nos canais entregáveis. Um incidente cuja abertura já foi
// avisada (n == 0, spec §8) não ganha aviso novo nem perde a marca anterior:
// o ON CONFLICT do enfileiramento só deduplica o mesmo incidente, então só um
// incidente pré-marcado prova que o n == 0 corta o enfileiramento.
func TestAvisarAbertura(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	canal := b.canal(t, true, true)
	b.canal(t, false, true) // pendente de verificação
	id := b.incidenteAberto(t, alvo)
	em := b.clk.Now().Add(time.Minute)
	chamar := func() error {
		return b.comoQueries(t, func(q *db.Queries) error {
			return avisarAbertura(context.Background(), q, b.dono, id, em)
		})
	}

	if err := chamar(); err != nil {
		t.Fatal(err)
	}
	if abertura, _ := b.marcasDoIncidente(t, id); abertura == nil || !abertura.Equal(em) {
		t.Errorf("open_notified_at = %v; queria %v", abertura, em)
	}
	if ns := b.notificacoes(t); len(ns) != 1 || ns[0].Canal != canal || ns[0].Evento != incident.EventOpened {
		t.Fatalf("notificações = %+v; queria a abertura no canal entregável %s", ns, canal)
	}

	jaAvisado := b.incidenteAberto(t, b.alvo(t, "https://outro.exemplo.com", nil))
	antes := em.Add(-time.Hour)
	err := b.comoQueries(t, func(q *db.Queries) error {
		_, err := q.MarkOpenNotified(context.Background(), db.MarkOpenNotifiedParams{ID: jaAvisado, Now: antes})
		if err != nil {
			return err
		}
		return avisarAbertura(context.Background(), q, b.dono, jaAvisado, em)
	})
	if err != nil {
		t.Fatal(err)
	}
	if abertura, _ := b.marcasDoIncidente(t, jaAvisado); abertura == nil || !abertura.Equal(antes) {
		t.Errorf("open_notified_at = %v; queria a marca anterior %v intacta", abertura, antes)
	}
	if ns := b.notificacoes(t); len(ns) != 1 {
		t.Errorf("notificações = %+v; queria só a do primeiro incidente, nenhuma para o já avisado", ns)
	}
}

// Idem para o fechamento (ver TestAvisarAbertura).
func TestAvisarFechamento(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	canal := b.canal(t, true, true)
	id := b.incidenteAberto(t, alvo)
	em := b.clk.Now().Add(time.Minute)
	chamar := func() error {
		return b.comoQueries(t, func(q *db.Queries) error {
			return avisarFechamento(context.Background(), q, b.dono, id, em)
		})
	}

	if err := chamar(); err != nil {
		t.Fatal(err)
	}
	if _, fechamento := b.marcasDoIncidente(t, id); fechamento == nil || !fechamento.Equal(em) {
		t.Errorf("close_notified_at = %v; queria %v", fechamento, em)
	}
	if ns := b.notificacoes(t); len(ns) != 1 || ns[0].Canal != canal || ns[0].Evento != incident.EventResolved {
		t.Fatalf("notificações = %+v; queria o fechamento no canal %s", ns, canal)
	}

	jaAvisado := b.incidenteAberto(t, b.alvo(t, "https://outro.exemplo.com", nil))
	antes := em.Add(-time.Hour)
	err := b.comoQueries(t, func(q *db.Queries) error {
		_, err := q.MarkCloseNotified(context.Background(), db.MarkCloseNotifiedParams{ID: jaAvisado, Now: antes})
		if err != nil {
			return err
		}
		return avisarFechamento(context.Background(), q, b.dono, jaAvisado, em)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, fechamento := b.marcasDoIncidente(t, jaAvisado); fechamento == nil || !fechamento.Equal(antes) {
		t.Errorf("close_notified_at = %v; queria a marca anterior %v intacta", fechamento, antes)
	}
	if ns := b.notificacoes(t); len(ns) != 1 {
		t.Errorf("notificações = %+v; queria só a do primeiro incidente, nenhuma para o já avisado", ns)
	}
}
