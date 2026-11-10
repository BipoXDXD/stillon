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

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
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

// O alvo com ẞ nunca nasce pelo Create: a exceção de URL já gravada é do Update.
func TestCreateRefusesASharpSURLWithoutWritingARow(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	in := entrada()
	in.URL = "https://STRAẞE.de/"

	_, err := a.svc.Create(context.Background(), dono, in)

	var verr *targets.ValidationError
	if !errors.As(err, &verr) || len(verr.Fields) != 1 || verr.Fields[0].Field != "url" || verr.Fields[0].Reason != targets.ReasonInvalid {
		t.Errorf("erro = %v, queria ValidationError invalid em url", err)
	}
	var linhas int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&linhas); err != nil {
		t.Fatal(err)
	}
	if linhas != 0 {
		t.Errorf("%d linhas gravadas, queria nenhuma", linhas)
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

// alvoGravadoComSharpS põe na linha, sem passar pela validação, uma URL cujo
// domínio depende da tabela do IDNA.
func (a ambiente) alvoGravadoComSharpS(t *testing.T, dono uuid.UUID, url string) targets.Target {
	t.Helper()
	alvo, err := a.svc.Create(context.Background(), dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.Exec(context.Background(), `UPDATE targets SET url = $2 WHERE id = $1`, alvo.ID, url); err != nil {
		t.Fatal(err)
	}
	return alvo
}

// O dono ainda administra o alvo que o sondador recusa: pausar e renomear não
// tocam na URL. Quem a recusa é a sondagem, e os headers ficam.
func TestUpdateOfAStoredSharpSTargetLeavesTheURLAlone(t *testing.T) {
	const gravada = "https://STRAẞE.de/saude"
	nome, pausado, mesma, mesmaComEspaco := "Novo nome", false, gravada, "  "+gravada+"  "
	for _, caso := range []struct {
		nome  string
		patch targets.Patch
	}{
		{"pausar", targets.Patch{Enabled: &pausado}},
		{"renomear", targets.Patch{Name: &nome}},
		{"reenviar a mesma URL", targets.Patch{URL: &mesma}},
		{"reenviar a mesma URL com espaços", targets.Patch{URL: &mesmaComEspaco}},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			ctx := context.Background()
			dono := a.usuario(t)
			alvo := a.alvoGravadoComSharpS(t, dono, gravada)

			got, err := a.svc.Update(ctx, dono, alvo.ID, caso.patch)
			if err != nil {
				t.Fatal(err)
			}
			if got.URL != gravada {
				t.Errorf("url = %q", got.URL)
			}
			if len(got.HeaderNames) != 1 || a.headersGravados(t, alvo.ID)["Authorization"] != "Bearer segredo-do-cliente" {
				t.Errorf("os headers mudaram: %v", got.HeaderNames)
			}
		})
	}

	t.Run("pausar vale", func(t *testing.T) {
		a := novoAmbiente(t)
		dono := a.usuario(t)
		alvo := a.alvoGravadoComSharpS(t, dono, gravada)
		got, err := a.svc.Update(context.Background(), dono, alvo.ID, targets.Patch{Enabled: &pausado})
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled {
			t.Error("o alvo continuou ativo")
		}
	})
}

// O SameOrigin compara pela tabela do IDNA do binário, e o domínio de uma URL
// com ẞ depende dela: em qualquer troca, ele daria "mesma origem" para um dos
// destinos abaixo. Os headers não vão junto em nenhum dos dois.
func TestEditingAStoredSharpSURLDropsTheHeaders(t *testing.T) {
	for _, caso := range []struct{ nome, gravada, nova string }{
		{"ẞ para ß", "https://STRAẞE.de/saude", "https://straße.de/saude"},
		{"percent-encoded para ß", "https://stra%E1%BA%9Ee.de/saude", "https://straße.de/saude"},
		{"com espaços", "https://STRAẞE.de/saude", "  https://straße.de/saude  "},
		{"para ss", "https://STRAẞE.de/saude", "https://strasse.de/saude"},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			dono := a.usuario(t)
			alvo := a.alvoGravadoComSharpS(t, dono, caso.gravada)

			got, err := a.svc.Update(context.Background(), dono, alvo.ID, targets.Patch{URL: &caso.nova})
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

	t.Run("headers no mesmo PATCH valem", func(t *testing.T) {
		a := novoAmbiente(t)
		dono := a.usuario(t)
		alvo := a.alvoGravadoComSharpS(t, dono, "https://STRAẞE.de/saude")
		nova := "https://straße.de/saude"
		got, err := a.svc.Update(context.Background(), dono, alvo.ID, targets.Patch{
			URL: &nova, SetHeaders: true, Headers: map[string]string{"X-Nova": "redigitado"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.HeaderNames) != 1 || a.headersGravados(t, alvo.ID)["X-Nova"] != "redigitado" {
			t.Errorf("os headers do PATCH não valeram: %v", got.HeaderNames)
		}
	})
}

// A exceção é só para a URL que já estava lá: gravar ẞ continua proibido.
func TestUpdateCannotWriteASharpSURL(t *testing.T) {
	outra := "https://outro-STRAẞE.de/"
	cifrada := "https://outro-stra%E1%BA%9Ee.de/"
	caminho := "https://STRAẞE.de/outro-caminho"
	for _, caso := range []struct {
		nome, gravada string
		nova          *string
	}{
		{"de URL comum", "https://api.exemplo.com/health", &outra},
		{"de URL comum, percent-encoded", "https://api.exemplo.com/health", &cifrada},
		{"de outro ẞ", "https://STRAẞE.de/saude", &outra},
		{"outro caminho do mesmo host", "https://STRAẞE.de/saude", &caminho},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			dono := a.usuario(t)
			alvo := a.alvoGravadoComSharpS(t, dono, caso.gravada)

			_, err := a.svc.Update(context.Background(), dono, alvo.ID, targets.Patch{URL: caso.nova})
			var verr *targets.ValidationError
			if !errors.As(err, &verr) || verr.Fields[0].Field != "url" || verr.Fields[0].Reason != targets.ReasonInvalid {
				t.Fatalf("erro = %v, queria ValidationError em url", err)
			}
			if h := a.headersGravados(t, alvo.ID); len(h) != 1 {
				t.Errorf("a recusa mexeu nos headers: %v", h)
			}
		})
	}
}

// gravarValidade grava a linha de target_tls do alvo como o sondador grava:
// no escopo dele, o único que a escreve em produção.
func (a ambiente) gravarValidade(t *testing.T, id uuid.UUID, notAfter, observedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		return q.SaveTargetTLS(ctx, db.SaveTargetTLSParams{TargetID: id, NotAfter: notAfter, ObservedAt: observedAt})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// temValidade olha target_tls por fora da RLS, como um dump.
func (a ambiente) temValidade(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM target_tls WHERE target_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// A validade é do certificado do host cadastrado. Mudar só o caminho mantém a
// leitura; outro host é outro certificado, e a leitura do anterior sumiria só
// na próxima sonda se o PATCH não a apagasse.
func TestPatchToAnotherHostForgetsTheCertificate(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	alvo, err := a.svc.Create(ctx, dono, entrada())
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC)
	a.gravarValidade(t, alvo.ID, notAfter, observedAt)

	caminho := "https://api.exemplo.com/v2/health"
	got, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &caminho})
	if err != nil {
		t.Fatal(err)
	}
	if !a.temValidade(t, alvo.ID) {
		t.Error("mudar só o caminho apagou a validade")
	}
	if got.TLS == nil || !got.TLS.NotAfter.Equal(notAfter) || !got.TLS.ObservedAt.Equal(observedAt) {
		t.Errorf("TLS devolvido = %+v, queria not_after %v e observed_at %v", got.TLS, notAfter, observedAt)
	}

	// Outro host com o PATCH recusado: nada muda, nem a validade.
	outro := "https://outro.exemplo.com/health"
	timeoutInvalido := int32(0)
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &outro, TimeoutMS: &timeoutInvalido}); err == nil {
		t.Fatal("PATCH com timeout_ms 0 passou")
	}
	if !a.temValidade(t, alvo.ID) {
		t.Error("o PATCH recusado apagou a validade")
	}

	got, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{URL: &outro})
	if err != nil {
		t.Fatal(err)
	}
	if a.temValidade(t, alvo.ID) {
		t.Error("outro host manteve a validade do certificado anterior")
	}
	if got.TLS != nil {
		t.Errorf("TLS devolvido = %+v, queria nil depois de trocar de host", got.TLS)
	}
}

func regraDeStatus() *targets.ContentRule {
	return &targets.ContentRule{Mode: targets.ContentJSONEquals, Value: `"ok"`, JSONPointer: "/status"}
}

func mesmaRegraDeTeste(a, b *targets.ContentRule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// temErroDeCampo diz se err é um ValidationError com o motivo no campo.
func temErroDeCampo(err error, campo, motivo string) bool {
	var verr *targets.ValidationError
	if !errors.As(err, &verr) {
		return false
	}
	for _, f := range verr.Fields {
		if f.Field == campo && f.Reason == motivo {
			return true
		}
	}
	return false
}

// A regra vai e volta pelas três colunas: o cadastro a grava, a leitura e a
// lista a devolvem, um PATCH que não a cita a mantém e um PATCH com
// SetContentRule e nil a limpa.
func TestContentRuleRoundTrip(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.ContentRule = regraDeStatus()
	criado, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}
	if !mesmaRegraDeTeste(criado.ContentRule, regraDeStatus()) {
		t.Errorf("Create devolveu a regra %+v, queria %+v", criado.ContentRule, regraDeStatus())
	}
	lido, err := a.svc.Get(ctx, dono, criado.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !mesmaRegraDeTeste(lido.ContentRule, regraDeStatus()) {
		t.Errorf("Get devolveu a regra %+v, queria %+v", lido.ContentRule, regraDeStatus())
	}
	lista, err := a.svc.List(ctx, dono, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || !mesmaRegraDeTeste(lista[0].ContentRule, regraDeStatus()) {
		t.Errorf("List devolveu %+v, queria um alvo com a regra", lista)
	}

	nome := "Outro nome"
	mantido, err := a.svc.Update(ctx, dono, criado.ID, targets.Patch{Name: &nome})
	if err != nil {
		t.Fatal(err)
	}
	if !mesmaRegraDeTeste(mantido.ContentRule, regraDeStatus()) {
		t.Errorf("Update sem SetContentRule devolveu a regra %+v, queria %+v", mantido.ContentRule, regraDeStatus())
	}
	if lido, err = a.svc.Get(ctx, dono, criado.ID); err != nil {
		t.Fatal(err)
	}
	if !mesmaRegraDeTeste(lido.ContentRule, regraDeStatus()) {
		t.Errorf("depois de um PATCH sem SetContentRule a regra gravada é %+v, queria %+v", lido.ContentRule, regraDeStatus())
	}

	limpo, err := a.svc.Update(ctx, dono, criado.ID, targets.Patch{SetContentRule: true})
	if err != nil {
		t.Fatal(err)
	}
	if limpo.ContentRule != nil {
		t.Errorf("Update com SetContentRule e nil devolveu a regra %+v", limpo.ContentRule)
	}
	if lido, err = a.svc.Get(ctx, dono, criado.ID); err != nil {
		t.Fatal(err)
	}
	if lido.ContentRule != nil {
		t.Errorf("depois de limpar, a regra gravada é %+v", lido.ContentRule)
	}
}

// HEAD não traz corpo: um PATCH só de método num alvo com regra é recusado, e
// nada muda no banco.
func TestPatchToHeadWithRuleIsRefused(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.ContentRule = regraDeStatus()
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}

	head := "HEAD"
	_, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{Method: &head})
	if !temErroDeCampo(err, "content_rule", targets.ReasonForbidden) {
		t.Errorf("erro = %v, queria ValidationError content_rule forbidden", err)
	}
	relido, err := a.svc.Get(ctx, dono, alvo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if relido.Method != "GET" || !mesmaRegraDeTeste(relido.ContentRule, regraDeStatus()) {
		t.Errorf("relido: método %q, regra %+v; queria GET e a regra intacta", relido.Method, relido.ContentRule)
	}
}

// Com o Accept-Encoding do dono, o transporte entrega o corpo comprimido e a
// regra julgaria bytes que ninguém escreveu.
func TestPatchAddingAcceptEncodingWithRuleIsRefused(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.ContentRule = regraDeStatus()
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.svc.Update(ctx, dono, alvo.ID, targets.Patch{
		SetHeaders: true, Headers: map[string]string{"Accept-Encoding": "br"},
	})
	if !temErroDeCampo(err, "content_rule", targets.ReasonForbidden) {
		t.Errorf("erro = %v, queria ValidationError content_rule forbidden", err)
	}
	if h := a.headersGravados(t, alvo.ID); len(h) != 1 || h["Authorization"] != "Bearer segredo-do-cliente" {
		t.Errorf("headers gravados = %v, queria só o Authorization original", h)
	}
}

// Outra regra é outro critério de saúde: as falhas pela regra antiga não somam
// com as da nova, e a sonda sai já, como em qualquer mudança do que é sondado.
func TestRuleChangeResetsHealthAndProbesNow(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	in := entrada()
	in.ContentRule = regraDeStatus()
	alvo, err := a.svc.Create(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}
	a.clk.Advance(10 * time.Minute)
	a.saude(t, alvo.ID, a.clk.Now().Add(-2*time.Minute), a.clk.Now().Add(-time.Minute))
	if _, err := a.admin.Exec(ctx, `UPDATE targets SET next_run_at = $2 WHERE id = $1`,
		alvo.ID, a.clk.Now().Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}

	outra := &targets.ContentRule{Mode: targets.ContentJSONEquals, Value: `"pronto"`, JSONPointer: "/status"}
	if _, err := a.svc.Update(ctx, dono, alvo.ID, targets.Patch{SetContentRule: true, ContentRule: outra}); err != nil {
		t.Fatal(err)
	}

	if h := a.lerSaude(t, alvo.ID); h.falhas != 0 || h.sucessos != 0 || h.desde != nil {
		t.Errorf("trocar a regra: histerese = %s, queria zerada e sem início", h)
	}
	var proximo time.Time
	if err := a.admin.QueryRow(ctx, `SELECT next_run_at FROM targets WHERE id = $1`, alvo.ID).Scan(&proximo); err != nil {
		t.Fatal(err)
	}
	if agora := time.Date(2026, 9, 22, 12, 10, 0, 0, time.UTC); !proximo.Equal(agora) {
		t.Errorf("next_run_at = %v, queria %v", proximo.UTC(), agora)
	}
}
