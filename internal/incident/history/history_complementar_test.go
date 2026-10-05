package history_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/incident/history"
)

func TestListPreservesDatabaseErrorAndRecoversComplementar(t *testing.T) {
	b := novaBancada(t)
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	inc := b.incidente(t, alvo, false)
	restaurar := indisponibilizarIncidentsComplementar(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got, err := b.svc.List(ctx, dono, history.Filter{})
	assertRelationErrorComplementar(t, err, "listar incidentes: ")
	if len(got) != 0 {
		t.Fatalf("List devolveu incidentes apesar da falha do banco: %+v", got)
	}

	restaurar()
	got, err = b.svc.List(ctx, dono, history.Filter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("List não se recuperou após restaurar schema: %d incidentes, erro=%v", len(got), err)
	}
	if got[0].ID != inc || got[0].TargetName != "API" || got[0].Cause != "dns" || got[0].AckAt != nil {
		t.Fatalf("recuperação não preservou o incidente original: %+v", got[0])
	}
}

func TestAckPreservesDatabaseErrorAndRecoversComplementar(t *testing.T) {
	b := novaBancada(t)
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	inc := b.incidente(t, alvo, false)
	restaurar := indisponibilizarIncidentsComplementar(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got, err := b.svc.Ack(ctx, dono, inc)
	if errors.Is(err, history.ErrNotFound) {
		t.Fatalf("Ack confundiu falha do banco com incidente inexistente: %v", err)
	}
	assertRelationErrorComplementar(t, err, "reconhecer incidente: ")
	if got != (history.Incident{}) {
		t.Fatalf("Ack devolveu incidente reconhecido apesar da falha do banco: %+v", got)
	}

	restaurar()
	items, err := b.svc.List(ctx, dono, history.Filter{})
	if err != nil || len(items) != 1 || items[0].ID != inc || items[0].AckAt != nil {
		t.Fatalf("Ack com falha alterou o incidente: itens=%+v, erro=%v", items, err)
	}
	got, err = b.svc.Ack(ctx, dono, inc)
	if err != nil || got.ID != inc || got.AckAt == nil || !got.AckAt.Equal(agora) {
		t.Fatalf("Ack não se recuperou após restaurar schema: incidente=%+v, erro=%v", got, err)
	}
	items, err = b.svc.List(ctx, dono, history.Filter{})
	if err != nil || len(items) != 1 || items[0].AckAt == nil || !items[0].AckAt.Equal(agora) {
		t.Fatalf("reconhecimento recuperado não foi persistido: itens=%+v, erro=%v", items, err)
	}
}

func assertRelationErrorComplementar(t *testing.T, err error, contextPrefix string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("erro não preservou causa PostgreSQL de relação indisponível: %v", err)
	}
	if !strings.HasPrefix(err.Error(), contextPrefix) {
		t.Fatalf("erro perdeu contexto da operação %q: %v", contextPrefix, err)
	}
}

// O pool vem exclusivamente da fixture descartável da bancada. O rename
// atômico simula schema indisponível sem alterar migrations nem dados; cada
// serviço continua usando sua transação RLS. A restauração registrada depois
// de New executa antes do fechamento do pool e do restore de snapshot.
func indisponibilizarIncidentsComplementar(t *testing.T, b *bancada) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := b.pool.Exec(ctx, `ALTER TABLE public.incidents RENAME TO incidents_indisponivel_complementar`); err != nil {
		t.Fatalf("indisponibilizar relação da bancada: %v", err)
	}
	restaurada := false
	restaurar := func() {
		if restaurada {
			return
		}
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer restoreCancel()
		if _, err := b.pool.Exec(restoreCtx, `ALTER TABLE public.incidents_indisponivel_complementar RENAME TO incidents`); err != nil {
			t.Fatalf("restaurar relação da bancada: %v", err)
		}
		restaurada = true
	}
	t.Cleanup(restaurar)
	return restaurar
}
