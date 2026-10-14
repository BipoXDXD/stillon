package prober

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
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
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: "GET", IntervalSec: 60}
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

	gravou := make(chan struct{})
	go func() {
		defer close(gravou)
		a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: urlVelha, Method: "GET", IntervalSec: 60}
		b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})
	}()
	esperarBloqueio(ctx, t, b, gravou)

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	esperar(t, gravou, "a gravação terminar depois do commit do PATCH")

	if n := b.falhasSeguidas(t, alvo); n != 0 {
		t.Errorf("%d falhas seguidas; a gravação leu a URL velha antes do commit do PATCH", n)
	}
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

// esperarBloqueio volta quando alguma conexão do banco de teste espera uma
// trava de linha; falha se a gravação terminar antes disso.
func esperarBloqueio(ctx context.Context, t *testing.T, b *bancada, gravou <-chan struct{}) {
	t.Helper()
	espera, parar := context.WithTimeout(ctx, esperaMaxima)
	defer parar()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var bloqueada bool
		if err := b.pool.QueryRow(espera, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> pg_backend_pid()
			   AND wait_event_type = 'Lock')`).Scan(&bloqueada); err != nil {
			t.Fatalf("observar as conexões: %v", err)
		}
		if bloqueada {
			return
		}
		select {
		case <-gravou:
			t.Fatal("a gravação terminou com o PATCH aberto; deveria esperar a linha do alvo")
		case <-tick.C:
		case <-espera.Done():
			t.Fatal("a gravação não chegou a esperar o PATCH")
		}
	}
}
