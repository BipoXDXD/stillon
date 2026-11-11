package targets_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// encherAte cria n alvos do dono direto no banco (o Create inteiro, n vezes,
// seria lento à toa).
func (a ambiente) encherAte(t *testing.T, dono uuid.UUID, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $2::int) i`, dono, n); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// O 100º entra; o 101º é ErrLimitReached; o teto é por usuário.
//
// Duas barreiras fazem a contagem ser por usuário: o WHERE user_id de
// CountTargets e a RLS da InUserTx. Sabotar só uma deixa o teste verde; o
// relatório da Task 8 registra a sabotagem das duas juntas.
func TestCreateStopsAtTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	if _, err := a.svc.Create(ctx, dono, entrada()); err != nil {
		t.Fatalf("o 100º alvo foi recusado: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, entrada()); !errors.Is(err, targets.ErrLimitReached) {
		t.Fatalf("o 101º alvo deu %v, queria ErrLimitReached", err)
	}
	if _, err := a.svc.Create(ctx, outro, entrada()); err != nil {
		t.Errorf("o teto de um usuário barrou outro: %v", err)
	}
}

// Dois POST simultâneos com 99 alvos: um entra, o outro bate no teto. O gancho
// segura cada criação entre a contagem e a inserção até as duas chegarem ali,
// ou por meio segundo: com a trava, a segunda nem conta antes de a primeira
// comitar; sem ela, as duas contam 99 e as duas inserem.
func TestConcurrentCreatesRespectTheLimit(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, int(targets.MaxTargetsPerUser)-1)

	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	a.svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = a.svc.Create(context.Background(), dono, entrada())
		}()
	}
	wg.Wait()

	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != int(targets.MaxTargetsPerUser) {
		t.Errorf("%d alvos depois de dois POST simultâneos (erros: %v), queria %d", n, erros, targets.MaxTargetsPerUser)
	}
}

// D-T3: sem opção, o serviço parte de 5 alvos com regra por host, o mesmo
// literal do padrão de CONTENT_RULES_PER_HOST no config.
func TestDefaultContentRulesPerHostIsFive(t *testing.T) {
	if targets.DefaultContentRulesPerHost != 5 {
		t.Errorf("DefaultContentRulesPerHost = %d, queria 5", targets.DefaultContentRulesPerHost)
	}
}

// encherComRegra cria n alvos do dono com regra contains direto no banco, em
// urlBase/1 … urlBase/n, e devolve os ids na ordem de i.
func (a ambiente) encherComRegra(t *testing.T, dono uuid.UUID, urlBase string, n int, habilitado bool) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status,
		                                            next_run_at, enabled, content_mode, content_value)
		SELECT gen_random_uuid(), $1, 'regra ' || i, $2 || '/' || i, 'GET', 60, 10000, 200, now(), $4, 'contains', 'ok'
		  FROM generate_series(1, $3::int) i
		 ORDER BY i
		RETURNING id`, dono, urlBase, n, habilitado)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return ids
}

// regrasDoDono conta por fora da RLS os alvos do dono com regra de conteúdo.
func (a ambiente) regrasDoDono(t *testing.T, dono uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM targets WHERE user_id = $1 AND content_mode IS NOT NULL`, dono).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func regraOK() *targets.ContentRule {
	return &targets.ContentRule{Mode: targets.ContentContains, Value: "ok"}
}

func comRegra(url string) targets.Input {
	in := entrada()
	in.URL = url
	in.ContentRule = regraOK()
	return in
}

func semRegra(url string) targets.Input {
	in := entrada()
	in.URL = url
	return in
}

// D-T1: o teto conta alvos com regra pela chave do host, sem porta, caixa nem
// ponto final; alvo sem regra não conta, e subdomínio é outro host.
func TestCreateWithRuleStopsAtTheHostCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", 4, true)

	if _, err := a.svc.Create(ctx, dono, semRegra("https://example.com/x")); err != nil {
		t.Fatalf("alvo sem regra no host recusado: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, comRegra("https://EXAMPLE.com./saude")); err != nil {
		t.Fatalf("a 5ª regra no host foi recusada: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, comRegra("https://example.com:8443/")); err != targets.ErrContentHostLimit {
		t.Fatalf("a 6ª regra no host (outra porta) deu %v, queria ErrContentHostLimit sem embrulho", err)
	}
	if _, err := a.svc.Create(ctx, dono, comRegra("https://a.example.com/")); err != nil {
		t.Fatalf("regra em subdomínio recusada: %v", err)
	}
	if n := a.regrasDoDono(t, dono); n != 6 {
		t.Errorf("regras do dono = %d, queria 6", n)
	}
	var total int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Errorf("alvos do dono = %d, queria 7", total)
	}
}

// D-T1 (opção A): a contagem só vê as linhas do dono.
func TestHostCapCountsOnlyThisUsersRules(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	a.encherComRegra(t, outro, "https://example.com", 5, true)

	if _, err := a.svc.Create(ctx, dono, comRegra("https://example.com/a")); err != nil {
		t.Fatalf("as regras de outro usuário barraram o dono: %v", err)
	}
	if n := a.regrasDoDono(t, outro); n != 5 {
		t.Errorf("regras de outro = %d, queria 5", n)
	}
	rows, err := a.admin.Query(ctx, `SELECT url FROM targets WHERE user_id = $1 ORDER BY url`, outro)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	quer := []string{"https://example.com/1", "https://example.com/2", "https://example.com/3", "https://example.com/4", "https://example.com/5"}
	if !slices.Equal(urls, quer) {
		t.Errorf("URLs de outro = %v, queria %v", urls, quer)
	}
}

// D-T1: pausar não libera vaga.
func TestHostCapCountsPausedTargets(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", 5, false)

	if _, err := a.svc.Create(context.Background(), dono, comRegra("https://example.com/a")); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("com 5 regras pausadas no host, Create deu %v, queria ErrContentHostLimit", err)
	}
}

func TestHostCapIsConfigurable(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	svc := targets.NewService(a.pool, caixa(t), a.clk, targets.ComTetoDeRegrasPorHost(1))

	if _, err := svc.Create(ctx, dono, comRegra("https://example.com/1")); err != nil {
		t.Fatalf("a 1ª regra com teto 1 foi recusada: %v", err)
	}
	if _, err := svc.Create(ctx, dono, comRegra("https://example.com/2")); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("a 2ª regra com teto 1 deu %v, queria ErrContentHostLimit", err)
	}
}

// Review Focus 3: com o host cheio, corpo inválido é 400, não 409, nos dois
// caminhos.
func TestValidationComesBeforeTheHostCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	x, err := a.svc.Create(ctx, dono, semRegra("https://example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	a.encherComRegra(t, dono, "https://example.com", 5, true)

	var verr *targets.ValidationError
	head := comRegra("https://example.com/h")
	head.Method = "HEAD"
	if _, err := a.svc.Create(ctx, dono, head); !errors.As(err, &verr) {
		t.Errorf("Create com HEAD e regra no host cheio deu %v, queria ValidationError", err)
	}
	vazia := &targets.ContentRule{Mode: targets.ContentContains, Value: ""}
	if _, err := a.svc.Update(ctx, dono, x.ID, targets.Patch{SetContentRule: true, ContentRule: vazia}); !errors.As(err, &verr) {
		t.Errorf("PATCH com valor vazio no host cheio deu %v, queria ValidationError", err)
	}
}

// D-T2: o PATCH que põe regra num host cheio é recusado antes de qualquer
// escrita: nem a regra, nem a histerese, nem next_run_at, nem a validade do
// certificado mudam.
func TestPatchIntoAFullHostIsRefused(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	x, err := a.svc.Create(ctx, dono, semRegra("https://example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := a.svc.Create(ctx, dono, comRegra("https://other.org/"))
	if err != nil {
		t.Fatal(err)
	}
	a.encherComRegra(t, dono, "https://example.com", 5, true)
	desde, ultimo := a.clk.Now().Add(-3*time.Minute), a.clk.Now().Add(-time.Minute)
	a.saude(t, x.ID, desde, ultimo)
	proximo := a.clk.Now().Add(42 * time.Minute)
	if _, err := a.admin.Exec(ctx, `UPDATE targets SET next_run_at = $2 WHERE id = $1`, x.ID, proximo); err != nil {
		t.Fatal(err)
	}

	if _, err := a.svc.Update(ctx, dono, x.ID, targets.Patch{SetContentRule: true, ContentRule: regraOK()}); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("PATCH pondo regra em X no host cheio deu %v, queria ErrContentHostLimit", err)
	}
	var semModo bool
	var proximoGravado time.Time
	if err := a.admin.QueryRow(ctx, `SELECT content_mode IS NULL, next_run_at FROM targets WHERE id = $1`, x.ID).Scan(&semModo, &proximoGravado); err != nil {
		t.Fatal(err)
	}
	if !semModo {
		t.Error("a regra recusada foi gravada em X")
	}
	if !proximoGravado.Equal(proximo) {
		t.Errorf("next_run_at de X = %s, queria %s", proximoGravado.UTC().Format(time.RFC3339), proximo.Format(time.RFC3339))
	}
	if h := a.lerSaude(t, x.ID); h.falhas != 2 {
		t.Errorf("histerese de X depois da recusa: %s, queria falhas=2", h)
	}

	// Trocar de host com a regra: o mesmo teto; a URL e a validade do
	// certificado do host anterior ficam.
	a.gravarValidade(t, y.ID, a.clk.Now().Add(30*24*time.Hour), a.clk.Now())
	if _, err := a.svc.Update(ctx, dono, y.ID, targets.Patch{URL: ptr("https://example.com/y")}); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("PATCH movendo Y com regra para o host cheio deu %v, queria ErrContentHostLimit", err)
	}
	var url string
	if err := a.admin.QueryRow(ctx, `SELECT url FROM targets WHERE id = $1`, y.ID).Scan(&url); err != nil {
		t.Fatal(err)
	}
	if url != "https://other.org/" {
		t.Errorf("URL de Y depois da recusa = %q, queria https://other.org/", url)
	}
	if !a.temValidade(t, y.ID) {
		t.Error("a recusa apagou a validade do certificado de Y")
	}
}

// D-T2 e D-T4: o teto barra o aumento, não o estado. Num host acima do teto,
// todo PATCH que não põe o alvo num host novo passa, e nem conta.
func TestPatchThatDoesNotGrowTheHostPasses(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	ids := a.encherComRegra(t, dono, "https://example.com", 7, true)
	z := ids[0]
	a.svc.SetDepoisDeContar(func() { t.Error("PATCH que não aumenta o host contou") })

	patches := []struct {
		nome string
		p    targets.Patch
	}{
		{"nome", targets.Patch{Name: ptr("outro nome")}},
		{"valor", targets.Patch{SetContentRule: true, ContentRule: &targets.ContentRule{Mode: targets.ContentContains, Value: "saudavel"}}},
		{"modo", targets.Patch{SetContentRule: true, ContentRule: &targets.ContentRule{Mode: targets.ContentNotContains, Value: "saudavel"}}},
		{"url no mesmo host", targets.Patch{URL: ptr("https://Example.com./outra")}},
		{"pausa", targets.Patch{Enabled: ptr(false)}},
		{"sem regra", targets.Patch{SetContentRule: true, ContentRule: nil}},
	}
	for _, c := range patches {
		if _, err := a.svc.Update(ctx, dono, z, c.p); err != nil {
			t.Errorf("PATCH de %s num host acima do teto deu %v", c.nome, err)
		}
	}

	a.svc.SetDepoisDeContar(nil)
	if _, err := a.svc.Update(ctx, dono, z, targets.Patch{SetContentRule: true, ContentRule: regraOK()}); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Errorf("devolver a regra a Z com 6 outras no host deu %v, queria ErrContentHostLimit", err)
	}
}

func TestPatchRemovingARuleFreesTheSlot(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	ids := a.encherComRegra(t, dono, "https://example.com", 5, true)

	if _, err := a.svc.Update(ctx, dono, ids[0], targets.Patch{SetContentRule: true, ContentRule: nil}); err != nil {
		t.Fatalf("tirar a regra deu %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, comRegra("https://example.com/nova")); err != nil {
		t.Fatalf("a vaga liberada não foi aceita: %v", err)
	}
	if _, err := a.svc.Create(ctx, dono, comRegra("https://example.com/outra")); !errors.Is(err, targets.ErrContentHostLimit) {
		t.Fatalf("a 6ª regra deu %v, queria ErrContentHostLimit", err)
	}
}

// Alheio é 404 antes do teto: o 409 diria que o alvo existe.
func TestPatchOnAnotherUsersTargetIs404BeforeTheCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, outro := a.usuario(t), a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", 5, true)
	w, err := a.svc.Create(ctx, outro, semRegra("https://other.org/w"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.svc.Update(ctx, dono, w.ID, targets.Patch{URL: ptr("https://example.com/w"), SetContentRule: true, ContentRule: regraOK()})
	if !errors.Is(err, targets.ErrNotFound) {
		t.Fatalf("PATCH do dono no alvo de outro deu %v, queria ErrNotFound", err)
	}
	var semModo bool
	var url string
	if err := a.admin.QueryRow(ctx, `SELECT content_mode IS NULL, url FROM targets WHERE id = $1`, w.ID).Scan(&semModo, &url); err != nil {
		t.Fatal(err)
	}
	if !semModo || url != "https://other.org/w" {
		t.Errorf("o alvo de outro mudou: sem regra = %v, url = %q", semModo, url)
	}
}

// D-T2: no Create, com os dois tetos estourados, vale o de alvos (V6).
func TestTargetLimitComesBeforeTheHostCap(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherAte(t, dono, 95)
	a.encherComRegra(t, dono, "https://example.com", 5, true)

	if _, err := a.svc.Create(context.Background(), dono, comRegra("https://example.com/z")); !errors.Is(err, targets.ErrLimitReached) {
		t.Fatalf("com 100 alvos e o host cheio, Create deu %v, queria ErrLimitReached", err)
	}
}

// Review Focus 4: uma URL gravada que não se lê não casa com host nenhum.
func TestUnreadableStoredURLDoesNotCount(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
		expected_status, next_run_at, content_mode, content_value)
		VALUES (gen_random_uuid(), $1, 'antigo', '::nao e url', 'GET', 60, 10000, 200, now(), 'contains', 'ok')`, dono); err != nil {
		t.Fatal(err)
	}
	svc := targets.NewService(a.pool, caixa(t), a.clk, targets.ComTetoDeRegrasPorHost(1))

	if _, err := svc.Create(context.Background(), dono, comRegra("https://example.com/")); err != nil {
		t.Fatalf("a URL ilegível gravada barrou a regra: %v", err)
	}
}

// segurarAteAsDuas arma o gancho como TestConcurrentCreatesRespectTheLimit:
// cada chamada espera a outra chegar ao mesmo ponto, ou meio segundo.
func (a ambiente) segurarAteAsDuas() {
	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	a.svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})
}

func umaRecusaDoHost(t *testing.T, erros []error) {
	t.Helper()
	var ok, recusas int
	for _, err := range erros {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, targets.ErrContentHostLimit):
			recusas++
		}
	}
	if ok != 1 || recusas != 1 {
		t.Errorf("erros = %v, queria um nil e um ErrContentHostLimit", erros)
	}
}

func TestConcurrentCreatesRespectTheHostCap(t *testing.T) {
	a := novoAmbiente(t)
	dono := a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", 4, true)
	a.segurarAteAsDuas()

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Go(func() {
			_, erros[i] = a.svc.Create(context.Background(), dono, comRegra("https://example.com/c"))
		})
	}
	wg.Wait()

	if n := a.regrasDoDono(t, dono); n != 5 {
		t.Errorf("%d regras depois de dois POST simultâneos (erros: %v), queria 5", n, erros)
	}
	umaRecusaDoHost(t, erros)
}

func TestConcurrentPatchesRespectTheHostCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	a.encherComRegra(t, dono, "https://example.com", 4, true)
	x, err := a.svc.Create(ctx, dono, semRegra("https://example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := a.svc.Create(ctx, dono, semRegra("https://example.com/y"))
	if err != nil {
		t.Fatal(err)
	}
	a.segurarAteAsDuas()

	ids := []uuid.UUID{x.ID, y.ID}
	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i := range erros {
		wg.Go(func() {
			_, erros[i] = a.svc.Update(ctx, dono, ids[i], targets.Patch{SetContentRule: true, ContentRule: regraOK()})
		})
	}
	wg.Wait()

	if n := a.regrasDoDono(t, dono); n != 5 {
		t.Errorf("%d regras depois de dois PATCH simultâneos (erros: %v), queria 5", n, erros)
	}
	umaRecusaDoHost(t, erros)
}
