package targets_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

type ambiente struct {
	svc   *targets.Service
	pool  *pgxpool.Pool
	admin *pgxpool.Pool // superusuário: enxerga o banco como um dump enxergaria
	clk   *clock.Fake
}

func novoAmbiente(t *testing.T) ambiente {
	t.Helper()
	pool := testdb.New(t)
	admin, err := storage.NewPool(context.Background(), testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	return ambiente{svc: targets.NewService(pool, caixa(t), clk), pool: pool, admin: admin, clk: clk}
}

func (a ambiente) usuario(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

func entrada() targets.Input {
	limiar := targets.DefaultDegradedThresholdMS
	return targets.Input{
		Name: "API", URL: "https://api.exemplo.com/health", Enabled: true,
		DegradedThresholdMS: &limiar,
		Headers:             map[string]string{"Authorization": "Bearer segredo-do-cliente"},
	}
}

func TestCreateAppliesDefaultsAndHidesHeaderValues(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)

	alvo, err := a.svc.Create(context.Background(), dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	if alvo.Method != "GET" || alvo.IntervalSec != 60 || alvo.TimeoutMS != 10000 || alvo.ExpectedStatus != 200 {
		t.Errorf("padrões não aplicados: %+v", alvo)
	}
	if len(alvo.HeaderNames) != 1 || alvo.HeaderNames[0] != "Authorization" {
		t.Errorf("HeaderNames = %v", alvo.HeaderNames)
	}

	// Pelo lado de fora da RLS, como um dump do banco: o valor não pode estar
	// em claro, e o alvo nasce vencido, para ser sondado já.
	var enc []byte
	var proximo time.Time
	if err := a.admin.QueryRow(context.Background(),
		`SELECT headers_enc, next_run_at FROM targets WHERE id = $1`, alvo.ID).Scan(&enc, &proximo); err != nil {
		t.Fatal(err)
	}
	if len(enc) == 0 || bytes.Contains(enc, []byte("segredo-do-cliente")) {
		t.Errorf("headers_enc vazio ou em claro: %q", enc)
	}
	if !proximo.Equal(a.clk.Now()) {
		t.Errorf("next_run_at = %v, queria agora (%v)", proximo, a.clk.Now())
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	a := novoAmbiente(t)
	in := entrada()
	in.URL = "http://127.0.0.1/"
	_, err := a.svc.Create(context.Background(), a.usuario(t), in)
	var verr *targets.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("erro = %v, queria *ValidationError", err)
	}
}

func TestAnotherUsersTargetIsNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, intruso := a.usuario(t), a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	novoNome := "roubado"
	if _, err := a.svc.Get(ctx, intruso, alvo.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Get do intruso = %v, queria ErrNotFound", err)
	}
	if _, err := a.svc.Update(ctx, intruso, alvo.ID, targets.Patch{Name: &novoNome}); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Update do intruso = %v, queria ErrNotFound", err)
	}
	if err := a.svc.Delete(ctx, intruso, alvo.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Delete do intruso = %v, queria ErrNotFound", err)
	}
	if _, err := a.svc.Checks(ctx, intruso, alvo.ID, a.clk.Now().Add(-time.Hour), a.clk.Now()); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Checks do intruso = %v, queria ErrNotFound", err)
	}

	intacto, err := a.svc.Get(ctx, dono, alvo.ID)
	if err != nil || intacto.Name != "API" {
		t.Errorf("o alvo do dono mudou: %+v, %v", intacto, err)
	}
}

// D13: mudar o que é sondado reagenda para agora; mudar o nome, não.
func TestUpdateReschedulesOnlyWhenTheProbeChanges(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	criadoEm := a.clk.Now()

	proximo := func() time.Time {
		var p time.Time
		if err := a.admin.QueryRow(ctx, `SELECT next_run_at FROM targets WHERE id = $1`, alvo.ID).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	a.clk.Advance(time.Hour)
	nome := "Outro nome"
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{Name: &nome}); err != nil {
		t.Fatal(err)
	}
	if !proximo().Equal(criadoEm) {
		t.Errorf("mudar o nome reagendou a sonda")
	}

	url := "https://api.exemplo.com/v2/health"
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &url}); err != nil {
		t.Fatal(err)
	}
	if !proximo().Equal(a.clk.Now()) {
		t.Errorf("mudar a URL não reagendou para agora")
	}
}

// O Patch é validado sobre o alvo RESULTANTE: um timeout que cabia no
// intervalo antigo pode não caber no novo.
func TestUpdateValidatesTheResultingTarget(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.TimeoutMS = 29000
	in.IntervalSec = 60
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}

	trinta := int32(30)
	_, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{IntervalSec: &trinta})
	var verr *targets.ValidationError
	if !errors.As(err, &verr) || verr.Fields[0].Field != "timeout_ms" {
		t.Errorf("erro = %v, queria ValidationError em timeout_ms", err)
	}
}

func TestUpdateCanClearThresholdAndHeaders(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{
		SetDegradedThreshold: true, DegradedThresholdMS: nil,
		SetHeaders: true, Headers: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DegradedThresholdMS != nil || len(got.HeaderNames) != 0 {
		t.Errorf("limiar = %v, headers = %v; queria os dois limpos", got.DegradedThresholdMS, got.HeaderNames)
	}
}

func TestListPaginatesByCursorNewestFirst(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	var criados []uuid.UUID
	for range 5 {
		alvo, err := a.svc.Create(ctx, dono, entrada())
		if err != nil {
			t.Fatal(err)
		}
		criados = append(criados, alvo.ID)
	}

	var vistos []uuid.UUID
	var cursor *uuid.UUID
	for {
		pagina, err := a.svc.List(ctx, dono, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pagina {
			vistos = append(vistos, p.ID)
		}
		if len(pagina) < 2 {
			break
		}
		ultimo := pagina[len(pagina)-1].ID
		cursor = &ultimo
	}

	if len(vistos) != 5 {
		t.Fatalf("paginação devolveu %d alvos, queria 5: %v", len(vistos), vistos)
	}
	for i := range vistos {
		if vistos[i] != criados[len(criados)-1-i] {
			t.Errorf("posição %d = %s, queria %s (mais novo primeiro)", i, vistos[i], criados[len(criados)-1-i])
		}
	}
}

func TestChecksWindowIsLimited(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	fim := a.clk.Now()

	_, err = a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow-time.Second), fim)
	var verr *targets.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("janela de 48h+1s deu %v, queria ValidationError", err)
	}
	if _, err := a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxChecksWindow), fim); err != nil {
		t.Errorf("janela de 48h exatas deu %v", err)
	}
}
