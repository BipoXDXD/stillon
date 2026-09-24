package history_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/incident/history"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

var agora = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

type bancada struct {
	pool *pgxpool.Pool
	clk  *clock.Fake
	svc  *history.Service
}

func novaBancada(t *testing.T) *bancada {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	return &bancada{pool: pool, clk: clk, svc: history.NewService(pool, clk)}
}

func (b *bancada) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := b.pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) alvo(t *testing.T, dono uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, b.pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: nome, URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// incidente abre um incidente e, se resolvido, o fecha 10 minutos depois.
func (b *bancada) incidente(t *testing.T, alvo uuid.UUID, resolvido bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "dns"}); err != nil {
			return err
		}
		if resolvido {
			_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: id, ResolvedAt: agora.Add(10 * time.Minute)})
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ids(is []history.Incident) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(is))
	for _, i := range is {
		out = append(out, i.ID)
	}
	return out
}

func TestListFiltersAndOrdersNewestFirst(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	velho := b.incidente(t, alvo, true)
	aberto := b.incidente(t, alvo, false)

	abertos, fechados := true, false
	for nome, c := range map[string]struct {
		f    history.Filter
		quer []uuid.UUID
	}{
		"todos":      {history.Filter{}, []uuid.UUID{aberto, velho}},
		"abertos":    {history.Filter{Open: &abertos}, []uuid.UUID{aberto}},
		"resolvidos": {history.Filter{Open: &fechados}, []uuid.UUID{velho}},
	} {
		got, err := b.svc.List(ctx, dono, c.f)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != len(c.quer) || (len(g) > 0 && g[0] != c.quer[0]) {
			t.Errorf("%s: %v, queria %v", nome, g, c.quer)
		}
	}

	got, _ := b.svc.List(ctx, dono, history.Filter{})
	if got[0].TargetName != "API" || got[0].ResolvedAt != nil || got[1].ResolvedAt == nil || got[1].Cause != "dns" {
		t.Errorf("itens = %+v", got)
	}
}

func TestListPaginatesByCursor(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	alvo := b.alvo(t, dono, "API")
	var todos []uuid.UUID
	for range 3 {
		todos = append(todos, b.incidente(t, alvo, true))
	}

	pagina, err := b.svc.List(ctx, dono, history.Filter{Limit: 2})
	if err != nil || len(pagina) != 2 || pagina[0].ID != todos[2] {
		t.Fatalf("primeira página = %v, %v", ids(pagina), err)
	}
	cursor := pagina[1].ID
	resto, err := b.svc.List(ctx, dono, history.Filter{Limit: 2, Before: &cursor})
	if err != nil || len(resto) != 1 || resto[0].ID != todos[0] {
		t.Errorf("segunda página = %v, %v", ids(resto), err)
	}
}

func TestAckIsIdempotentAndScopedToTheOwner(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono, intruso := b.usuario(t), b.usuario(t)
	inc := b.incidente(t, b.alvo(t, dono, "API"), false)

	if _, err := b.svc.Ack(ctx, intruso, inc); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("ack pelo intruso deu %v, queria ErrNotFound", err)
	}
	if got, _ := b.svc.List(ctx, intruso, history.Filter{}); len(got) != 0 {
		t.Errorf("o intruso listou %d incidentes", len(got))
	}

	primeiro, err := b.svc.Ack(ctx, dono, inc)
	if err != nil || primeiro.AckAt == nil || !primeiro.AckAt.Equal(agora) {
		t.Fatalf("primeiro ack = %+v, %v", primeiro, err)
	}
	b.clk.Advance(time.Hour)
	segundo, err := b.svc.Ack(ctx, dono, inc)
	if err != nil || !segundo.AckAt.Equal(agora) {
		t.Errorf("segundo ack = %v, %v; queria o instante do primeiro", segundo.AckAt, err)
	}
	if _, err := b.svc.Ack(ctx, dono, uuid.Must(uuid.NewV7())); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("ack de id inexistente deu %v", err)
	}
}

// target_id restringe a um alvo; o de outro usuário dá lista vazia, e não os
// incidentes dele.
func TestListFiltersByTarget(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono, intruso := b.usuario(t), b.usuario(t)
	api, site := b.alvo(t, dono, "API"), b.alvo(t, dono, "Site")
	alheio := b.alvo(t, intruso, "Alheio")
	daAPI := b.incidente(t, api, true)
	b.incidente(t, site, true)
	b.incidente(t, alheio, true)

	got, err := b.svc.List(ctx, dono, history.Filter{TargetID: &api})
	if err != nil {
		t.Fatal(err)
	}
	if g := ids(got); len(g) != 1 || g[0] != daAPI {
		t.Errorf("filtro por alvo = %v, queria só %v", g, daAPI)
	}
	got, err = b.svc.List(ctx, dono, history.Filter{TargetID: &alheio})
	if err != nil || len(got) != 0 {
		t.Errorf("alvo alheio = %v, %v; queria lista vazia", ids(got), err)
	}
	if todos, _ := b.svc.List(ctx, dono, history.Filter{}); len(todos) != 2 {
		t.Errorf("sem filtro: %d incidentes, queria 2", len(todos))
	}
}
