package statuspage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// O dono despublica enquanto uma projeção está no ar (achado da segunda
// revisão). A projeção leu a página antes da mudança; sem a geração, ela a
// gravaria no cache depois do forget, e a página seguiria pública por 30 s.
// O gancho faz a mudança exatamente entre a leitura e a gravação no cache.
func TestUnpublishDuringAProjectionIsNotUndoneByTheCache(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := NewService(pool, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)))
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Create(ctx, dono, Input{Slug: "empresa", Title: "Status", Published: true})
	if err != nil {
		t.Fatal(err)
	}

	nao := false
	svc.depoisDeProjetar = func(string) {
		svc.depoisDeProjetar = nil // uma vez só
		if _, err := svc.Update(ctx, dono, p.ID, Patch{Published: &nao}); err != nil {
			t.Error(err)
		}
	}
	// Quem estava no voo recebe a página já despublicada: a geração mudou
	// durante o voo, e a consulta é refeita.
	if _, err := svc.Public(ctx, "empresa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a chamada durante a mudança deu %v; queria ErrNotFound", err)
	}
	// E a projeção velha não ficou no cache.
	if _, err := svc.Public(ctx, "empresa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a chamada seguinte deu %v; queria ErrNotFound, e não a versão velha em cache", err)
	}
}
