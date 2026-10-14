package statuspage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

var agora = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

type ambiente struct {
	svc  *statuspage.Service
	pool *pgxpool.Pool
	clk  *clock.Fake
}

func novoAmbiente(t *testing.T) ambiente {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	return ambiente{svc: statuspage.NewService(pool, clk), pool: pool, clk: clk}
}

func (a ambiente) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// alvo cria um alvo do dono, com URL que não pode vazar para a página.
func (a ambiente) alvo(t *testing.T, dono uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: nome, URL: "https://interno.exemplo.com/health", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: a.clk.Now(), CreatedAt: a.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func campos(err error) []statuspage.FieldError {
	var verr *statuspage.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	return verr.Fields
}

func TestCreateKeepsTheOrderAndGetReadsItBack(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")

	criada, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "minha-empresa", Title: "  Status da Empresa  ", Published: true,
		Items: []statuspage.Item{{TargetID: site, DisplayName: " Site "}, {TargetID: api, DisplayName: "API"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lida, err := a.svc.Get(ctx, dono)
	if err != nil {
		t.Fatal(err)
	}
	quer := []statuspage.Item{{TargetID: site, DisplayName: "Site"}, {TargetID: api, DisplayName: "API"}}
	for _, p := range []statuspage.Page{criada, lida} {
		if p.ID != criada.ID || p.Slug != "minha-empresa" || p.Title != "Status da Empresa" || !p.Published ||
			!slices.Equal(p.Items, quer) || !p.CreatedAt.Equal(agora) {
			t.Errorf("página = %+v; queria a ordem dada, com espaços aparados", p)
		}
	}
}

func TestGetWithoutPageIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	if _, err := a.svc.Get(context.Background(), a.usuario(t)); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("Get sem página = %v, queria ErrNotFound", err)
	}
}

// Spec §2.1: uma página por usuário. O slug é único entre todos.
func TestOnePagePerUserAndSlugsAreUnique(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "Status"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa-2", Title: "Outra"}); !errors.Is(err, statuspage.ErrExists) {
		t.Errorf("segunda página do mesmo dono = %v, queria ErrExists", err)
	}
	if _, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "empresa", Title: "Minha"}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Errorf("slug de outro dono = %v, queria ErrSlugTaken", err)
	}
	pOutro, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "outra", Title: "Minha"})
	if err != nil {
		t.Fatal(err)
	}
	tomado := "empresa"
	if _, err := a.svc.Update(ctx, outro, pOutro.ID, statuspage.Patch{Slug: &tomado}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Errorf("PATCH para o slug de outro dono = %v, queria ErrSlugTaken", err)
	}
}

func TestValidationNamesEveryField(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo := a.alvo(t, dono, "api")
	vinteEUm := make([]statuspage.Item, statuspage.MaxItems+1)
	for i := range vinteEUm {
		vinteEUm[i] = statuspage.Item{TargetID: uuid.Must(uuid.NewV7()), DisplayName: "x"}
	}

	for nome, c := range map[string]struct {
		in   statuspage.Input
		quer statuspage.FieldError
	}{
		"slug vazio":               {statuspage.Input{Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "required"}},
		"slug maiúsculo":           {statuspage.Input{Slug: "Empresa", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug curto":               {statuspage.Input{Slug: "ab", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug com hífen":           {statuspage.Input{Slug: "-empresa", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug longo":               {statuspage.Input{Slug: strings.Repeat("a", 41), Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"slug com barra":           {statuspage.Input{Slug: "a/../b", Title: "T"}, statuspage.FieldError{Field: "slug", Reason: "invalid"}},
		"título vazio":             {statuspage.Input{Slug: "empresa", Title: "   "}, statuspage.FieldError{Field: "title", Reason: "required"}},
		"título longo":             {statuspage.Input{Slug: "empresa", Title: strings.Repeat("é", 101)}, statuspage.FieldError{Field: "title", Reason: "too_long"}},
		"título com controle":      {statuspage.Input{Slug: "empresa", Title: "a\nb"}, statuspage.FieldError{Field: "title", Reason: "invalid"}},
		"título com inversão bidi": {statuspage.Input{Slug: "empresa", Title: "a\u202eb"}, statuspage.FieldError{Field: "title", Reason: "invalid"}},
		"nome com largura zero": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo, DisplayName: "a\u200bb"}}}, statuspage.FieldError{Field: "items[0].display_name", Reason: "invalid"}},
		"itens demais": {statuspage.Input{Slug: "empresa", Title: "T", Items: vinteEUm}, statuspage.FieldError{Field: "items", Reason: "out_of_range"}},
		"nome vazio": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo}}}, statuspage.FieldError{Field: "items[0].display_name", Reason: "required"}},
		"alvo repetido": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo, DisplayName: "a"}, {TargetID: alvo, DisplayName: "b"}}},
			statuspage.FieldError{Field: "items[1].target_id", Reason: "invalid"}},
		"alvo nulo": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{DisplayName: "a"}}}, statuspage.FieldError{Field: "items[0].target_id", Reason: "invalid"}},
		"alvo inexistente": {statuspage.Input{Slug: "empresa", Title: "T",
			Items: []statuspage.Item{{TargetID: alvo, DisplayName: "a"}, {TargetID: uuid.Must(uuid.NewV7()), DisplayName: "b"}}},
			statuspage.FieldError{Field: "items[1].target_id", Reason: "invalid"}},
	} {
		_, err := a.svc.Create(ctx, dono, c.in)
		if f := campos(err); len(f) != 1 || f[0] != c.quer {
			t.Errorf("%s: %v (%+v), queria %+v", nome, err, f, c.quer)
		}
	}
	if _, err := a.svc.Get(ctx, dono); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("uma entrada inválida deixou página gravada: %v", err)
	}
}

// Spec §5.5: o alvo de outro usuário não entra na página, e a recusa
// desfaz tudo, inclusive a página.
func TestAnotherUsersTargetIsRefused(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	alheio := a.alvo(t, outro, "do outro")

	_, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "T", Items: []statuspage.Item{{TargetID: alheio, DisplayName: "roubado"}},
	})
	if f := campos(err); len(f) != 1 || f[0] != (statuspage.FieldError{Field: "items[0].target_id", Reason: "invalid"}) {
		t.Fatalf("alvo alheio = %v, queria items[0].target_id/invalid", err)
	}
	if _, err := a.svc.Get(ctx, dono); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("a recusa deixou a página gravada: %v", err)
	}
}

func TestUpdateChangesOnlyWhatCameAndReplacesItemsWhole(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site, db1 := a.alvo(t, dono, "api"), a.alvo(t, dono, "site"), a.alvo(t, dono, "db")
	p, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "Status", Items: []statuspage.Item{{TargetID: api, DisplayName: "API"}, {TargetID: site, DisplayName: "Site"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	titulo, publicada := "Novo título", true
	p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Title: &titulo, Published: &publicada})
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Novo título" || !p.Published || p.Slug != "empresa" || len(p.Items) != 2 {
		t.Errorf("PATCH de título e publicação mexeu no resto: %+v", p)
	}

	novos := []statuspage.Item{{TargetID: db1, DisplayName: "Banco"}, {TargetID: api, DisplayName: "API"}}
	p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Items: &novos})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Items, novos) {
		t.Errorf("itens = %+v, queria a lista nova, na ordem nova", p.Items)
	}

	vazia := []statuspage.Item{}
	if p, err = a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Items: &vazia}); err != nil || len(p.Items) != 0 {
		t.Errorf("PATCH com items vazio = %+v, %v; queria sem itens", p.Items, err)
	}

	ruim := "Empresa"
	if _, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Slug: &ruim}); campos(err) == nil {
		t.Errorf("PATCH com slug inválido = %v, queria ValidationError", err)
	}
}

func TestUpdateOfAnotherIDIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	pOutro, err := a.svc.Create(ctx, outro, statuspage.Input{Slug: "outra", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	titulo := "invadido"
	for _, id := range []uuid.UUID{pOutro.ID, uuid.Must(uuid.NewV7())} {
		if _, err := a.svc.Update(ctx, dono, id, statuspage.Patch{Title: &titulo}); !errors.Is(err, statuspage.ErrNotFound) {
			t.Errorf("PATCH na página %s = %v, queria ErrNotFound", id, err)
		}
	}
	if p, _ := a.svc.Get(ctx, outro); p.Title != "T" {
		t.Errorf("a página do outro mudou: %+v", p)
	}
	semPagina := a.usuario(t)
	if _, err := a.svc.Update(ctx, semPagina, pOutro.ID, statuspage.Patch{Title: &titulo}); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("PATCH de quem não tem página = %v, queria ErrNotFound", err)
	}
}

// Alvo apagado sai da página junto (ON DELETE CASCADE nos itens): a página
// continua de pé, com os outros itens na ordem de antes.
func TestDeletedTargetLeavesThePage(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	api, site := a.alvo(t, dono, "api"), a.alvo(t, dono, "site")
	if _, err := a.svc.Create(ctx, dono, statuspage.Input{
		Slug: "empresa", Title: "T", Items: []statuspage.Item{{TargetID: api, DisplayName: "API"}, {TargetID: site, DisplayName: "Site"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: dono, ID: api})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.svc.Get(ctx, dono)
	if err != nil || len(p.Items) != 1 || p.Items[0].TargetID != site {
		t.Errorf("depois de apagar o alvo: %+v, %v; queria só o Site", p.Items, err)
	}
}

// Dois PATCH ao mesmo tempo não perdem campo (achado da revisão): o Update
// trava a linha antes de ler. Aqui, outra transação trava a página e muda o
// título; o PATCH de published espera por ela e lê o título novo. Sem a
// trava, ele leria o título velho e o regravaria por cima.
func TestConcurrentPatchesDoNotLoseFields(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	p, err := a.svc.Create(ctx, dono, statuspage.Input{Slug: "empresa", Title: "Velho"})
	if err != nil {
		t.Fatal(err)
	}

	outra, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer outra.Rollback(ctx)
	for _, sql := range []string{
		`SELECT set_config('app.user_id', '` + dono.String() + `', true)`,
		`SELECT id FROM status_pages FOR UPDATE`,
		`UPDATE status_pages SET title = 'Novo'`,
	} {
		if _, err := outra.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	fim := make(chan error, 1)
	sim := true
	go func() {
		_, err := a.svc.Update(ctx, dono, p.ID, statuspage.Patch{Published: &sim})
		fim <- err
	}()
	// O PATCH está esperando pela trava da outra transação.
	prazo := time.Now().Add(5 * time.Second)
	for {
		var esperando int
		if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&esperando); err != nil {
			t.Fatal(err)
		}
		if esperando > 0 {
			break
		}
		if time.Now().After(prazo) {
			t.Fatal("o PATCH não esperou pela trava da outra transação")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := outra.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-fim; err != nil {
		t.Fatal(err)
	}
	if got, err := a.svc.Get(ctx, dono); err != nil || got.Title != "Novo" || !got.Published {
		t.Errorf("página = %+v, %v; queria o título da outra transação E published", got, err)
	}
}
