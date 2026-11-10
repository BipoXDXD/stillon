package prober

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

const (
	urlVelha = "https://velha.exemplo.com/saude"
	urlNova  = "https://nova.exemplo.com/saude"
)

// transporteFunc deixa o teste decidir quando a sonda termina, sem rede: a
// URL do alvo pode ser pública (o PATCH a valida) e a sonda nunca sai da
// máquina.
type transporteFunc func(*http.Request) (*http.Response, error)

func (f transporteFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// clienteSegurado responde 500 a toda sonda, mas só depois que liberar
// fechar; chegou fecha quando a primeira sonda está em voo.
func clienteSegurado() (client *http.Client, chegou, liberar chan struct{}) {
	chegou, liberar = make(chan struct{}), make(chan struct{})
	avisou := false
	client = &http.Client{Transport: transporteFunc(func(r *http.Request) (*http.Response, error) {
		if !avisou {
			avisou = true
			close(chegou)
		}
		<-liberar
		return &http.Response{
			StatusCode: http.StatusInternalServerError, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader("")), Request: r,
		}, nil
	})}
	return client, chegou, liberar
}

func (b *bancada) falhasSeguidas(t *testing.T, alvo uuid.UUID) int32 {
	t.Helper()
	var n int32
	b.comoDono(t, func(tx pgx.Tx) {
		err := tx.QueryRow(context.Background(),
			`SELECT consecutive_failures FROM target_health WHERE target_id = $1`, alvo).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
	})
	return n
}

// semearFalhas grava duas falhas da URL velha, um passo antes do limiar de
// abertura: se a sonda velha contar, a URL nova herda a sequência.
func (b *bancada) semearFalhas(t *testing.T, alvo uuid.UUID) {
	t.Helper()
	b.semearFalhasDa(t, db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: "GET", IntervalSec: 60})
}

// semearFalhasDa grava as duas falhas como sondas da reivindicação a, que tem
// de bater com o alvo gravado, regra de conteúdo inclusive.
func (b *bancada) semearFalhasDa(t *testing.T, a db.ClaimDueTargetsRow) {
	t.Helper()
	alvo := a.ID
	falha := Result{Status: StatusDown, ErrorKind: KindTimeout}
	t0 := b.clk.Now()
	b.s.gravar(a, t0.Add(-2*time.Minute), falha)
	b.s.gravar(a, t0.Add(-time.Minute), falha)
	if n := b.falhasSeguidas(t, alvo); n != 2 {
		t.Fatalf("semeadura: %d falhas seguidas; queria 2", n)
	}
}

// E2 da revisão de 2026-10-14. O PATCH troca a URL com uma sonda da URL
// velha em voo; a sonda termina DEPOIS do PATCH. O resultado dela é sobre
// outro serviço: não pode entrar na histerese recém-zerada, nem no histórico
// de checks do alvo com a URL nova.
func TestProbeOfTheOldURLFinishingAfterThePatchIsDiscarded(t *testing.T) {
	client, chegou, liberar := clienteSegurado()
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	b.semearFalhas(t, alvo)

	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria uma sonda", n, err)
	}
	esperar(t, chegou, "a sonda da URL velha entrar em voo")

	nova := urlNova
	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, targets.Patch{URL: &nova}); err != nil {
		t.Fatal(err)
	}
	close(liberar)
	b.s.Stop(esperaMaxima)

	if n := b.falhasSeguidas(t, alvo); n != 0 {
		t.Errorf("%d falhas seguidas depois do PATCH; a sonda da URL velha contou para a nova", n)
	}
	if c := b.checks(t, alvo); len(c) != 2 {
		t.Errorf("%d checks no histórico; queria só os 2 semeados, sem o da sonda velha", len(c))
	}
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Errorf("incidentes = %+v; queria nenhum", inc)
	}
}

// A gravação que começa com o PATCH ainda aberto espera por ele e enxerga a
// URL nova. Sem a trava da linha do alvo, ela leria a URL velha antes do
// commit e somaria a falha à histerese que o PATCH acabou de zerar.
func TestProbeWriteWaitsForAnOpenPatchAndSeesTheNewURL(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	b.semearFalhas(t, alvo)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	q := db.New(tx)
	if _, err := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: b.dono, ID: alvo}); err != nil {
		t.Fatal(err)
	}
	if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{UserID: b.dono, TargetID: alvo}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE targets SET url = $2 WHERE id = $1`, alvo, urlNova); err != nil {
		t.Fatal(err)
	}

	gravou := make(chan struct{}, 1)
	go func() {
		defer func() { gravou <- struct{}{} }()
		a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: "GET", IntervalSec: 60}
		b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}()
	if !testdb.EsperarBloqueados(t, b.pool, 1, gravou) {
		t.Fatal("a gravação terminou com o PATCH aberto; deveria esperar a linha do alvo")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	esperar(t, gravou, "a gravação terminar depois do commit do PATCH")

	if n := b.falhasSeguidas(t, alvo); n != 0 {
		t.Errorf("%d falhas seguidas; a gravação leu a URL velha antes do commit do PATCH", n)
	}
}

// conferirSemRastroDaSonda afirma que a terceira falha, a que abriria o
// incidente, não deixou nada além das duas semeadas.
func (b *bancada) conferirSemRastroDaSonda(t *testing.T, alvo uuid.UUID) {
	t.Helper()
	if n := b.falhasSeguidas(t, alvo); n != 2 {
		t.Errorf("%d falhas seguidas; queria as 2 semeadas, sem a da sonda em voo", n)
	}
	if c := b.checks(t, alvo); len(c) != 2 {
		t.Errorf("%d checks no histórico; queria só os 2 semeados", len(c))
	}
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Errorf("incidentes = %+v; queria nenhum", inc)
	}
	if avisos := b.notificacoes(t); len(avisos) != 0 {
		t.Errorf("notificações = %+v; queria nenhuma", avisos)
	}
}

// O dono pausa um alvo que começou a falhar, com a sonda da terceira falha em
// voo. Um alvo pausado não abre incidente: sem sonda, nada o fecharia, e o
// aviso chegaria depois de o dono já ter dito que não quer monitorar.
func TestPauseWithProbeInFlightDiscardsTheResult(t *testing.T) {
	client, chegou, liberar := clienteSegurado()
	b := novaBancada(t, client, MaxInFlight)
	b.canal(t, true, true)
	alvo := b.alvo(t, urlVelha, nil)
	b.semearFalhas(t, alvo)

	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria uma sonda", n, err)
	}
	esperar(t, chegou, "a sonda entrar em voo")

	pausado := false
	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, targets.Patch{Enabled: &pausado}); err != nil {
		t.Fatal(err)
	}
	close(liberar)
	b.s.Stop(esperaMaxima)

	b.conferirSemRastroDaSonda(t, alvo)
}

// A gravação que começa com a pausa ainda aberta espera o commit e enxerga o
// alvo pausado. Sem a trava da linha, ela leria enabled verdadeiro antes do
// commit e abriria o incidente logo depois dele.
func TestPauseWithProbeInFlightIsAwaitedByTheWrite(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	b.canal(t, true, true)
	alvo := b.alvo(t, urlVelha, nil)
	b.semearFalhas(t, alvo)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(tx).GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: b.dono, ID: alvo}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE targets SET enabled = false WHERE id = $1`, alvo); err != nil {
		t.Fatal(err)
	}

	gravou := make(chan struct{}, 1)
	go func() {
		defer func() { gravou <- struct{}{} }()
		a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: http.MethodGet, IntervalSec: 60}
		b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}()
	if !testdb.EsperarBloqueados(t, b.pool, 1, gravou) {
		t.Fatal("a gravação terminou com a pausa aberta; deveria esperar a linha do alvo")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	esperar(t, gravou, "a gravação terminar depois do commit da pausa")

	b.conferirSemRastroDaSonda(t, alvo)
}

// Trocar só o método também é outro serviço (C4 do Plano 10): a sonda com o
// método velho é descartada como a da URL velha.
func TestProbeWithTheOldMethodIsDiscarded(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, urlVelha, nil)
	b.semearFalhas(t, alvo)

	head := http.MethodHead
	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, targets.Patch{Method: &head}); err != nil {
		t.Fatal(err)
	}
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: http.MethodGet, IntervalSec: 60}
	b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})

	if n := b.falhasSeguidas(t, alvo); n != 0 {
		t.Errorf("%d falhas seguidas; a sonda com o método velho contou", n)
	}
}

// alvoComContains grava o alvo com a regra contains valor.
func alvoComContains(valor string) func(*db.InsertTargetParams) {
	return func(p *db.InsertTargetParams) {
		modo := string(targets.ContentContains)
		p.ContentMode, p.ContentValue = &modo, &valor
	}
}

func regraContains(valor string) targets.Patch {
	return targets.Patch{SetContentRule: true, ContentRule: &targets.ContentRule{Mode: targets.ContentContains, Value: valor}}
}

// T2 da spec do PR 5. Trocar a regra também é julgar outra coisa: a sonda que
// saiu com contains "a" e termina depois do PATCH para contains "b" é
// descartada, como a da URL velha.
func TestProbeDiscardedWhenRuleChanged(t *testing.T) {
	client, chegou, liberar := clienteSegurado()
	b := novaBancada(t, client, MaxInFlight)
	b.canal(t, true, true)
	alvo := b.alvo(t, urlVelha, alvoComContains("a"))
	modo, valor := string(targets.ContentContains), "a"
	b.semearFalhasDa(t, db.ClaimDueTargetsRow{
		ID: alvo, UserID: b.dono, URL: urlVelha, Method: http.MethodGet, IntervalSec: 60,
		ContentMode: &modo, ContentValue: &valor,
	})

	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria uma sonda", n, err)
	}
	esperar(t, chegou, "a sonda da regra velha entrar em voo")

	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, regraContains("b")); err != nil {
		t.Fatal(err)
	}
	close(liberar)
	b.s.Stop(esperaMaxima)

	if n := b.falhasSeguidas(t, alvo); n != 0 {
		t.Errorf("%d falhas seguidas depois do PATCH; a sonda da regra velha contou para a nova", n)
	}
	if c := b.checks(t, alvo); len(c) != 2 {
		t.Errorf("%d checks no histórico; queria só os 2 semeados, sem o da sonda velha", len(c))
	}
	if inc := b.incidentes(t); len(inc) != 0 {
		t.Errorf("incidentes = %+v; queria nenhum", inc)
	}
}

// Foco 5 da spec do PR 5. Com um incidente content_mismatch aberto, o dono
// troca a regra. Só sucessos da regra nova fecham o incidente, e são os 5 do
// incident.Default (CloseAfter): uma sonda da regra velha em voo durante a
// troca, que PASSARIA pela regra velha, não conta como o primeiro deles.
func TestRuleChangeClosesTheOpenIncidentOnlyWithNewRuleSuccesses(t *testing.T) {
	chegou, liberar := make(chan struct{}), make(chan struct{})
	var liberou sync.Once
	soltar := func() { liberou.Do(func() { close(liberar) }) }
	t.Cleanup(soltar) // um Fatal no meio não deixa a sonda presa
	var segurar atomic.Bool
	var corpo atomic.Pointer[string]
	fora := "xyz" // sem "a" nem "b"
	corpo.Store(&fora)
	// Transporte em vez de httptest: o PATCH revalida a URL, e loopback é
	// recusado no cadastro.
	client := &http.Client{Transport: transporteFunc(func(r *http.Request) (*http.Response, error) {
		if segurar.CompareAndSwap(true, false) {
			close(chegou)
			<-liberar
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(*corpo.Load())), Request: r,
		}, nil
	})}
	b := novaBancada(t, client, MaxInFlight)
	b.canal(t, true, true)
	alvo := b.alvo(t, urlVelha, alvoComContains("a"))
	for range 3 {
		b.rodada(t)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].Cause != "content_mismatch" || inc[0].ResolvedAt != nil {
		t.Fatalf("incidentes = %+v; queria um aberto por content_mismatch", inc)
	}

	// O corpo passa nas duas regras; a sonda da regra velha fica em voo
	// enquanto o PATCH troca a regra.
	ambas := "a b"
	corpo.Store(&ambas)
	segurar.Store(true)
	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria uma sonda", n, err)
	}
	esperar(t, chegou, "a sonda da regra velha entrar em voo")
	svc := targets.NewService(b.pool, b.box, b.clk)
	if _, err := svc.Update(context.Background(), b.dono, alvo, regraContains("b")); err != nil {
		t.Fatal(err)
	}
	soltar()
	b.s.emVoo.Wait()
	b.clk.Advance(2 * time.Minute)

	const sucessosParaFechar = 5 // incident.Default.CloseAfter
	primeiro := b.rodada(t)
	for range sucessosParaFechar - 2 {
		b.rodada(t)
	}
	if inc := b.incidentes(t); len(inc) != 1 || inc[0].ResolvedAt != nil {
		t.Fatalf("%d sucessos da regra nova: incidentes = %+v; queria o mesmo, ainda aberto (a sonda velha contou?)",
			sucessosParaFechar-1, inc)
	}
	b.rodada(t)

	inc := b.incidentes(t)
	if len(inc) != 1 || inc[0].ResolvedAt == nil || !inc[0].ResolvedAt.Equal(primeiro) {
		t.Fatalf("%d sucessos da regra nova: incidentes = %+v; queria resolvido em %v (o primeiro deles)",
			sucessosParaFechar, inc, primeiro)
	}
}
