package targets_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	// O valor devolvido é montado em memória; o que vale é o que ficou
	// gravado.
	var semHeaders, semLimiar bool
	if err := a.admin.QueryRow(ctx,
		`SELECT headers_enc IS NULL, degraded_threshold_ms IS NULL FROM targets WHERE id = $1`,
		alvo.ID).Scan(&semHeaders, &semLimiar); err != nil {
		t.Fatal(err)
	}
	if !semHeaders || !semLimiar {
		t.Errorf("no banco: headers_enc nulo = %v, limiar nulo = %v; queria os dois nulos", semHeaders, semLimiar)
	}
}

// Um PATCH que não menciona headers não pode apagá-los: o Update decifra os
// atuais e os cifra de novo, e perder isso no caminho seria silencioso.
func TestUpdateKeepsStoredHeaders(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}

	nome := "Outro nome"
	got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{Name: &nome})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.HeaderNames) != 1 || got.HeaderNames[0] != "Authorization" {
		t.Errorf("HeaderNames = %v, queria [Authorization]", got.HeaderNames)
	}

	var enc []byte
	if err := a.admin.QueryRow(ctx, `SELECT headers_enc FROM targets WHERE id = $1`, alvo.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	h, err := targets.OpenHeaders(caixa(t), alvo.ID, enc)
	if err != nil {
		t.Fatal(err)
	}
	if h["Authorization"] != "Bearer segredo-do-cliente" {
		t.Errorf("headers gravados = %v, queria o Authorization original", h)
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

// D6: até 48h a série é bruta; acima, horária (rollups); acima de
// MaxRollupWindow, 400. As bordas são exatas dos dois lados.
func TestChecksWindowPicksTheResolution(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	fim := a.clk.Now()

	for _, c := range []struct {
		janela time.Duration
		quer   targets.Resolution
	}{
		{targets.MaxChecksWindow, targets.ResolutionRaw},
		{targets.MaxChecksWindow + time.Second, targets.Resolution1h},
		{targets.MaxRollupWindow, targets.Resolution1h},
	} {
		s, err := a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-c.janela), fim)
		if err != nil || s.Resolution != c.quer {
			t.Errorf("janela %v deu %q/%v, queria %q", c.janela, s.Resolution, err, c.quer)
		}
	}
	_, err = a.svc.Checks(ctx, dono, alvo.ID, fim.Add(-targets.MaxRollupWindow-time.Second), fim)
	var verr *targets.ValidationError
	if !errors.As(err, &verr) || verr.Fields[0] != (targets.FieldError{Field: "from", Reason: targets.ReasonOutOfRange}) {
		t.Errorf("janela acima do teto deu %v, queria from/out_of_range", err)
	}
}

// D6: a série horária vem de check_rollup_1h, só do alvo pedido, e inclui a
// hora que contém from (arredondado para baixo) e exclui a que começa em to.
func TestChecksReadsHourlyRollupsAboveTheRawWindow(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	outro, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	fim := a.clk.Now().Truncate(time.Hour) // 12:00 UTC
	inicio := fim.Add(-72 * time.Hour)
	p50 := int32(80)
	for _, r := range []struct {
		alvo   uuid.UUID
		bucket time.Time
	}{
		{alvo.ID, inicio.Add(-time.Hour)}, // antes da janela
		{alvo.ID, inicio},                 // contém from (from = inicio+30min)
		{alvo.ID, fim.Add(-time.Hour)},    // última hora dentro
		{alvo.ID, fim},                    // começa em to: fora
		{outro.ID, inicio},                // outro alvo: fora
	} {
		if _, err := a.admin.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
			VALUES ($1, $2, 60, 59, $3, 120, 200, 300)`, r.alvo, r.bucket, p50); err != nil {
			t.Fatal(err)
		}
	}

	s, err := a.svc.Checks(ctx, dono, alvo.ID, inicio.Add(30*time.Minute), fim)
	if err != nil {
		t.Fatal(err)
	}
	if s.Resolution != targets.Resolution1h || len(s.Checks) != 0 || len(s.Rollups) != 2 {
		t.Fatalf("série = %q, %d checks, %d rollups; queria 1h com 2 rollups", s.Resolution, len(s.Checks), len(s.Rollups))
	}
	primeiro, ultimo := s.Rollups[0], s.Rollups[1]
	if !primeiro.Bucket.Equal(inicio) || !ultimo.Bucket.Equal(fim.Add(-time.Hour)) || primeiro.Bucket.Location() != time.UTC {
		t.Errorf("buckets = %v, %v; queria %v, %v", primeiro.Bucket, ultimo.Bucket, inicio, fim.Add(-time.Hour))
	}
	if primeiro.Total != 60 || primeiro.OK != 59 || primeiro.P50 == nil || *primeiro.P50 != 80 || *primeiro.Max != 300 {
		t.Errorf("rollup = %+v", primeiro)
	}

	// Outro usuário não enxerga a série, nem vazia: é 404.
	intruso := a.usuario(t)
	if _, err := a.svc.Checks(ctx, intruso, alvo.ID, inicio, fim); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("série horária do intruso = %v, queria ErrNotFound", err)
	}
}

// headersGravados abre o que ficou no banco, pelo lado de fora da RLS.
func (a ambiente) headersGravados(t *testing.T, id uuid.UUID) map[string]string {
	t.Helper()
	var enc []byte
	if err := a.admin.QueryRow(context.Background(), `SELECT headers_enc FROM targets WHERE id = $1`, id).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	h, err := targets.OpenHeaders(caixa(t), id, enc)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// B1 (Plano 10): um PATCH que troca a origem da URL (esquema, host ou porta)
// sem mandar headers apaga os headers. Sem isso, quem tivesse um access token
// do dono (uma sessão roubada, ou encerrada há menos de 15 min) apontava o
// alvo para um host dele e recebia o Authorization do dono no próximo check,
// sem rastro. Mudar só o caminho mantém; mandar headers junto usa os novos.
func TestUpdateToAnotherOriginDropsTheHeaders(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)

	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	caminho := "https://api.exemplo.com:443/v2/health"
	got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &caminho})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.HeaderNames) != 1 || a.headersGravados(t, alvo.ID)["Authorization"] != "Bearer segredo-do-cliente" {
		t.Errorf("mudar só o caminho mexeu nos headers: %v", got.HeaderNames)
	}

	for _, outra := range []string{"https://atacante.example/", "https://api.exemplo.com:8443/", "http://api.exemplo.com/v2/health"} {
		t.Run(outra, func(t *testing.T) {
			alvo, err := a.svc.Create(ctx, dono, entrada())
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &outra})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.HeaderNames) != 0 {
				t.Errorf("header_names = %v, queria vazio", got.HeaderNames)
			}
			if h := a.headersGravados(t, alvo.ID); len(h) != 0 {
				t.Errorf("headers gravados = %v, queria nenhum", h)
			}
		})
	}

	// Com headers no mesmo PATCH, valem os que vieram: o dono que troca de
	// domínio redigita os dele.
	alvo, err = a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	outra := "https://novo.exemplo.com/"
	got, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{
		URL: &outra, SetHeaders: true, Headers: map[string]string{"X-Api-Key": "chave-nova"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h := a.headersGravados(t, alvo.ID); len(got.HeaderNames) != 1 || h["X-Api-Key"] != "chave-nova" || len(h) != 1 {
		t.Errorf("headers gravados = %v, queria só o X-Api-Key novo", h)
	}
}

// saude grava uma histerese no meio de uma sequência de falhas, como o
// sondador deixaria depois de duas falhas.
func (a ambiente) saude(t *testing.T, id uuid.UUID, desde, ultimo time.Time) {
	t.Helper()
	if _, err := a.admin.Exec(context.Background(), `
		INSERT INTO target_health (target_id, consecutive_failures, consecutive_successes, streak_since, last_checked_at)
		VALUES ($1, 2, 0, $2, $3)
		ON CONFLICT (target_id) DO UPDATE SET consecutive_failures = 2, consecutive_successes = 0,
		       streak_since = $2, last_checked_at = $3`, id, desde, ultimo); err != nil {
		t.Fatal(err)
	}
}

type histerese struct {
	falhas, sucessos int32
	desde, ultimo    *time.Time
}

func (h histerese) String() string {
	quando := func(t *time.Time) string {
		if t == nil {
			return "nulo"
		}
		return t.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("falhas=%d sucessos=%d desde=%s último=%s", h.falhas, h.sucessos, quando(h.desde), quando(h.ultimo))
}

func (a ambiente) lerSaude(t *testing.T, id uuid.UUID) histerese {
	t.Helper()
	var h histerese
	if err := a.admin.QueryRow(context.Background(), `
		SELECT consecutive_failures, consecutive_successes, streak_since, last_checked_at
		  FROM target_health WHERE target_id = $1`, id).Scan(&h.falhas, &h.sucessos, &h.desde, &h.ultimo); err != nil {
		t.Fatal(err)
	}
	return h
}

// C4 (Plano 10): trocar a URL ou o método troca o serviço sondado, e as
// falhas do anterior não podem somar com as do novo: a primeira falha
// passageira da URL nova abriria um incidente com o started_at da antiga. A
// histerese volta a "sem dados" (contadores zerados, sem início de
// sequência); o last_checked_at fica, porque é a guarda de check fora de
// ordem do detector. Outras mudanças (nome, intervalo, status esperado) não
// mexem nela.
func TestUpdateOfURLOrMethodResetsTheHysteresis(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	desde := a.clk.Now().Add(-2 * time.Minute)
	ultimo := a.clk.Now().Add(-time.Minute)

	nome, intervalo, status := "Outro nome", int32(120), int32(204)
	for _, p := range []targets.Patch{{Name: &nome}, {IntervalSec: &intervalo}, {ExpectedStatus: &status}} {
		a.saude(t, alvo.ID, desde, ultimo)
		if _, err := a.svc.Update(ctx, dono, alvo.ID, p); err != nil {
			t.Fatal(err)
		}
		if h := a.lerSaude(t, alvo.ID); h.falhas != 2 || h.desde == nil {
			t.Errorf("um PATCH que não troca o serviço zerou a histerese: %s", h)
		}
	}

	url, metodo := "https://api.exemplo.com/v2/health", "HEAD"
	for nomeDoCaso, p := range map[string]targets.Patch{"url": {URL: &url}, "método": {Method: &metodo}} {
		a.saude(t, alvo.ID, desde, ultimo)
		if _, err := a.svc.Update(ctx, dono, alvo.ID, p); err != nil {
			t.Fatal(err)
		}
		h := a.lerSaude(t, alvo.ID)
		if h.falhas != 0 || h.sucessos != 0 || h.desde != nil {
			t.Errorf("trocar %s: histerese = %s, queria zerada e sem início", nomeDoCaso, h)
		}
		if h.ultimo == nil || !h.ultimo.Equal(ultimo) {
			t.Errorf("trocar %s: last_checked_at = %v, queria %v intacto", nomeDoCaso, h.ultimo, ultimo)
		}
	}
}

// A troca de URL não resolve o incidente aberto (decisão do Plano 10): ele
// fecha pelos sucessos da URL nova, que é o caso comum (o dono corrigiu uma
// URL errada), e o aviso "voltou" sai quando o novo endereço provar que está
// no ar, não no PATCH. Os outros alvos do mesmo dono não são tocados.
func TestUpdateOfURLKeepsTheOpenIncidentAndOtherTargets(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	vizinho, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	desde, ultimo := a.clk.Now().Add(-2*time.Minute), a.clk.Now().Add(-time.Minute)
	a.saude(t, alvo.ID, desde, ultimo)
	a.saude(t, vizinho.ID, desde, ultimo)
	incidente := uuid.Must(uuid.NewV7())
	if _, err := a.admin.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, 'timeout')`,
		incidente, alvo.ID, desde); err != nil {
		t.Fatal(err)
	}

	url := "https://novo.exemplo.com/"
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &url}); err != nil {
		t.Fatal(err)
	}
	var aberto bool
	if err := a.admin.QueryRow(ctx, `SELECT resolved_at IS NULL FROM incidents WHERE id = $1`, incidente).Scan(&aberto); err != nil {
		t.Fatal(err)
	}
	if !aberto {
		t.Error("a troca de URL resolveu o incidente aberto")
	}
	if h := a.lerSaude(t, vizinho.ID); h.falhas != 2 || h.desde == nil {
		t.Errorf("a histerese de outro alvo do dono mudou: %s", h)
	}
}
