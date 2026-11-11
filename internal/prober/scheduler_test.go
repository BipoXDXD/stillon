package prober

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

const esperaMaxima = 5 * time.Second

type bancada struct {
	s    *Scheduler
	pool *pgxpool.Pool
	clk  *clock.Fake
	box  *crypto.Box
	m    *Metrics
	reg  *prometheus.Registry
	dono uuid.UUID
}

// novaBancada monta um agendador sobre o Postgres de teste. client nil usa
// o cliente de PRODUÇÃO (New); senão, newScheduler com o cliente dado.
func novaBancada(t *testing.T, client *http.Client, vagas int32) *bancada {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	if err := storage.EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	var s *Scheduler
	if client == nil {
		s = New(pool, box, clk, m, nil)
	} else {
		s = newScheduler(pool, box, clk, m, client, vagas)
	}
	// Registrado depois do testdb.New: cleanups rodam em ordem inversa, então
	// as sondas terminam antes de o banco ser restaurado.
	t.Cleanup(func() { s.Stop(esperaMaxima) })
	return &bancada{s: s, pool: pool, clk: clk, box: box, m: m, reg: reg, dono: dono}
}

func (b *bancada) alvo(t *testing.T, url string, mexer func(*db.InsertTargetParams)) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	p := db.InsertTargetParams{
		ID: id, UserID: b.dono, Name: "alvo", URL: url, Method: "GET",
		IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true,
		NextRunAt: b.clk.Now(), CreatedAt: b.clk.Now(),
	}
	if mexer != nil {
		mexer(&p)
	}
	if err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error { return q.InsertTarget(ctx, p) }); err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancada) checks(t *testing.T, alvo uuid.UUID) []db.ListChecksRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListChecksRow
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListChecks(ctx, db.ListChecksParams{
			UserID: b.dono, TargetID: alvo, FromAt: b.clk.Now().Add(-time.Hour), ToAt: b.clk.Now().Add(time.Hour),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (b *bancada) proximaExecucao(t *testing.T, alvo uuid.UUID) time.Time {
	t.Helper()
	ctx := context.Background()
	var next time.Time
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		row, err := q.GetTarget(ctx, db.GetTargetParams{UserID: b.dono, ID: alvo})
		next = row.NextRunAt
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// servidorLiberado sobe um httptest e devolve o cliente que o alcança.
func servidorLiberado(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, safedial.NewClientAllowing(0, enderecoDe(t, srv))
}

func esperar(t *testing.T, c <-chan struct{}, oque string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(esperaMaxima):
		t.Fatalf("esperando %s", oque)
	}
}

func TestTickProbesDueTargetAndRecordsCheck(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	n, err := b.s.Tick(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1 disparo", n, err)
	}
	b.s.Stop(esperaMaxima)

	rows := b.checks(t, alvo)
	if len(rows) != 1 {
		t.Fatalf("%d checks gravados, queria 1", len(rows))
	}
	c := rows[0]
	if c.Status != "up" || c.StatusCode == nil || *c.StatusCode != 200 || c.ErrorKind != nil {
		t.Errorf("check = %+v", c)
	}
	// D11: checked_at vem do relógio injetado.
	if !c.CheckedAt.Equal(b.clk.Now()) {
		t.Errorf("checked_at = %v, queria o relógio injetado %v", c.CheckedAt, b.clk.Now())
	}
	if v := testutil.ToFloat64(b.m.total.WithLabelValues("up")); v != 1 {
		t.Errorf("probe_total{status=up} = %v", v)
	}
	if v := testutil.ToFloat64(b.m.inflight); v != 0 {
		t.Errorf("scheduler_inflight = %v depois do Stop", v)
	}
}

func TestTickIgnoresTargetsNotDueOrDisabled(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) { p.NextRunAt = b.clk.Now().Add(time.Minute) })
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) { p.Enabled = false; p.NextRunAt = b.clk.Now().Add(-time.Hour) })

	if n, err := b.s.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("Tick = %d, %v; queria 0: nada vencido e ativo", n, err)
	}
	b.clk.Advance(2 * time.Minute)
	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1: só o ativo venceu", n, err)
	}
}

// Spec §6.2: sem jitter, alvos de intervalo igual disparam no mesmo segundo
// para sempre.
func TestNextRunIsJitteredWithinTenPercent(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	const total = 50
	alvos := make([]uuid.UUID, total)
	for i := range alvos {
		alvos[i] = b.alvo(t, srv.URL, nil)
	}
	agora := b.clk.Now()

	if n, err := b.s.Tick(context.Background()); err != nil || n != total {
		t.Fatalf("Tick = %d, %v", n, err)
	}
	b.s.Stop(esperaMaxima)

	distintos := map[time.Duration]bool{}
	for _, a := range alvos {
		d := b.proximaExecucao(t, a).Sub(agora)
		if d < 54*time.Second || d > 66*time.Second {
			t.Errorf("próxima execução em %v, fora de 60s ±10%%", d)
		}
		distintos[d] = true
	}
	if len(distintos) < total/2 {
		t.Errorf("só %d valores distintos em %d alvos: não há jitter", len(distintos), total)
	}
}

// Reservar as vagas antes de reivindicar: um alvo reivindicado sem vaga teria
// o next_run_at empurrado sem ter sido sondado.
func TestTickNeverClaimsMoreThanFreeSlots(t *testing.T) {
	liberar := make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-liberar:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, 2)
	var alvos []uuid.UUID
	for range 5 {
		alvos = append(alvos, b.alvo(t, srv.URL, nil))
	}
	agora := b.clk.Now()

	if n, err := b.s.Tick(context.Background()); err != nil || n != 2 {
		t.Fatalf("primeiro Tick = %d, %v; queria 2, o número de vagas", n, err)
	}
	if n, err := b.s.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("segundo Tick = %d, %v; queria 0, sem vaga livre", n, err)
	}

	vencidos := 0
	for _, a := range alvos {
		if !b.proximaExecucao(t, a).After(agora) {
			vencidos++
		}
	}
	if vencidos != 3 {
		t.Errorf("%d alvos continuam vencidos, queria 3: alvo reivindicado sem vaga", vencidos)
	}
	if v := testutil.ToFloat64(b.m.queueDepth); v != 3 {
		t.Errorf("scheduler_queue_depth = %v, queria 3", v)
	}
	// Inc é feito em Tick, antes de a goroutine nascer; as duas sondas estão
	// presas no servidor. Sem esta leitura, o "0 depois do Stop" de
	// TestTickProbesDueTargetAndRecordsCheck passaria sem Inc nem Dec.
	if v := testutil.ToFloat64(b.m.inflight); v != 2 {
		t.Errorf("scheduler_inflight = %v com duas sondas em voo, queria 2", v)
	}

	close(liberar)
	b.s.Stop(esperaMaxima)
}

// Spec §6.2 e §14: dois sondadores sobre o mesmo banco nunca sondam o mesmo
// alvo duas vezes.
//
// As duas reivindicações precisam se SOBREPOR no banco, e isso não acontece
// por acaso: cada Tick é uma transação de menos de um milissegundo, e dois
// Ticks largados juntos quase sempre rodam em série (a versão anterior deste
// teste, sem SKIP LOCKED na query, ficou vermelha 0 vezes em 100 com -race).
// Por isso uma transação do teste trava UM alvo. Sem SKIP LOCKED, as duas
// reivindicações bloqueiam, uma na trava do teste e a outra na da primeira,
// e o teste as vê paradas em pg_stat_activity antes de soltar; a segunda,
// com o snapshot de antes do commit da primeira, reivindica tudo de novo.
// Com SKIP LOCKED, nenhuma bloqueia: pulam o alvo travado e dividem o resto.
func TestTwoSchedulersNeverProbeTheSameTarget(t *testing.T) {
	var batidas sync.Map // caminho → *atomic.Int32
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		c, _ := batidas.LoadOrStore(r.URL.Path, new(atomic.Int32))
		c.(*atomic.Int32).Add(1)
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	outro := newScheduler(b.pool, b.box, b.clk, NewMetrics(prometheus.NewRegistry()), client, MaxInFlight)
	t.Cleanup(func() { outro.Stop(esperaMaxima) })

	const total = 40
	// Um caminho por alvo, para o servidor contar as batidas de cada um.
	for i := range total {
		b.alvo(t, fmt.Sprintf("%s/alvo-%d", srv.URL, i), nil)
	}

	ctx := context.Background()
	trava, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trava.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := trava.Exec(ctx, `SELECT id FROM targets ORDER BY id LIMIT 1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	var soma atomic.Int32
	var fim sync.WaitGroup
	acabaram := make(chan struct{})
	for _, s := range []*Scheduler{b.s, outro} {
		fim.Add(1)
		go func() {
			defer fim.Done()
			n, err := s.Tick(ctx)
			if err != nil {
				t.Error(err)
			}
			soma.Add(int32(n))
		}()
	}
	go func() {
		fim.Wait()
		close(acabaram)
	}()
	// Num Fatal abaixo, a trava ainda estaria de pé e os Ticks, presos nela.
	// Este cleanup roda antes dos Stop e do fechamento do pool.
	t.Cleanup(func() {
		_ = trava.Rollback(ctx)
		<-acabaram
	})

	// Espera um de dois desfechos: os dois Ticks terminaram (nenhum bloqueou)
	// ou as duas reivindicações estão paradas em lock.
	prazo := time.After(esperaMaxima)
	passo := time.NewTicker(5 * time.Millisecond)
	defer passo.Stop()
esperando:
	for {
		var bloqueadas int
		err := b.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> pg_backend_pid()
			   AND wait_event_type = 'Lock' AND query LIKE '%name: ClaimDueTargets%'`).Scan(&bloqueadas)
		if err != nil {
			t.Fatal(err)
		}
		if bloqueadas == 2 {
			break
		}
		select {
		case <-acabaram:
			break esperando
		case <-prazo:
			t.Fatalf("nem os Ticks terminaram nem as reivindicações bloquearam (%d bloqueadas)", bloqueadas)
		case <-passo.C:
		}
	}
	if err := trava.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	esperar(t, acabaram, "os dois Ticks terminarem")

	if soma.Load() != total-1 {
		t.Errorf("os dois dispararam %d, queria %d: todos menos o travado", soma.Load(), total-1)
	}
	// O alvo travado continua vencido, e o Tick seguinte o pega: SKIP LOCKED
	// adia, não perde.
	if n, err := b.s.Tick(ctx); err != nil || n != 1 {
		t.Errorf("Tick depois da trava = %d, %v; queria 1, o alvo que estava travado", n, err)
	}
	b.s.Stop(esperaMaxima)
	outro.Stop(esperaMaxima)

	caminhos := 0
	batidas.Range(func(caminho, c any) bool {
		caminhos++
		if n := c.(*atomic.Int32).Load(); n != 1 {
			t.Errorf("%s sondado %d vezes", caminho, n)
		}
		return true
	})
	if caminhos != total {
		t.Errorf("%d alvos sondados, queria %d", caminhos, total)
	}
}

// Um COMMIT que falha chega a Tick com os alvos já lidos pelo UPDATE, e
// nenhum deles é disparado. Se só as vagas que sobraram voltassem, as outras
// ficariam presas para sempre, e o agendador morreria de fome aos poucos.
//
// A falha vem de uma constraint trigger adiada, que só dispara no COMMIT: o
// UPDATE da reivindicação devolve as linhas normalmente. O papel da
// aplicação é dono das tabelas, e o snapshot restaurado no fim do teste leva
// a trigger embora.
func TestFailedClaimReturnsAllSlots(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, 1)
	alvo := b.alvo(t, srv.URL, nil)

	ctx := context.Background()
	if _, err := b.pool.Exec(ctx, `
		CREATE FUNCTION falhar_no_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'commit sabotado pelo teste';
		END $$;
		CREATE CONSTRAINT TRIGGER falhar_no_commit AFTER UPDATE ON targets
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION falhar_no_commit();`); err != nil {
		t.Fatal(err)
	}

	n, err := b.s.Tick(ctx)
	if err == nil || !strings.Contains(err.Error(), "commit sabotado pelo teste") || n != 0 {
		t.Fatalf("Tick = %d, %v; queria 0 e a falha do COMMIT", n, err)
	}

	if _, err := b.pool.Exec(ctx, `DROP TRIGGER falhar_no_commit ON targets`); err != nil {
		t.Fatal(err)
	}
	// Com uma vaga só, este Tick só dispara se a vaga do Tick que falhou voltou.
	if n, err := b.s.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("Tick depois da falha = %d, %v; queria 1: a vaga não voltou", n, err)
	}
	b.s.Stop(esperaMaxima)
	if rows := b.checks(t, alvo); len(rows) != 1 || rows[0].Status != "up" {
		t.Errorf("checks = %+v, queria um up", rows)
	}
}

// logSeguro é um buffer que aceita escrita concorrente: o agendador loga de
// goroutines de sonda.
type logSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSeguro) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSeguro) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func capturarLog(t *testing.T) *logSeguro {
	t.Helper()
	l := &logSeguro{}
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(l, nil)))
	t.Cleanup(func() { slog.SetDefault(antigo) })
	return l
}

// Review Focus 3. O dono apaga o alvo com a sonda em voo. Nada quebra,
// nenhum erro vai ao log, e a série de latência do alvo some.
func TestVanishedTargetIsDiscardedQuietly(t *testing.T) {
	log := capturarLog(t)
	chegou, liberar := make(chan struct{}), make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-liberar
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")

	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: alvo})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	close(liberar)
	b.s.Stop(esperaMaxima)

	if strings.Contains(log.String(), "gravar check") {
		t.Errorf("alvo apagado virou erro no log:\n%s", log)
	}
	if n := testutil.CollectAndCount(b.m.duration); n != 0 {
		t.Errorf("%d séries de latência sobraram para um alvo apagado (D9)", n)
	}
}

// Review Focus 4. SIGTERM com sonda em voo: ela é abortada ao fim do prazo
// e NÃO vira um down no histórico (D12).
func TestShutdownDiscardsAbortedProbes(t *testing.T) {
	chegou := make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-r.Context().Done()
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")
	b.s.Stop(50 * time.Millisecond)

	if rows := b.checks(t, alvo); len(rows) != 0 {
		t.Errorf("a sonda abortada pelo shutdown foi gravada: %+v", rows)
	}
	if v := testutil.ToFloat64(b.m.total.WithLabelValues("down")); v != 0 {
		t.Errorf("probe_total{status=down} = %v para uma sonda abortada", v)
	}
}

// O outro lado de D12: a sonda que termina DENTRO do prazo é gravada.
func TestShutdownWaitsForProbesThatFinishInTime(t *testing.T) {
	chegou, liberar := make(chan struct{}), make(chan struct{})
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		close(chegou)
		<-liberar
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	esperar(t, chegou, "a sonda chegar ao servidor")

	parou := make(chan struct{})
	go func() {
		b.s.Stop(esperaMaxima)
		close(parou)
	}()
	close(liberar)
	esperar(t, parou, "o Stop terminar")

	if rows := b.checks(t, alvo); len(rows) != 1 || rows[0].Status != "up" {
		t.Errorf("checks = %+v, queria um up", rows)
	}
}

func TestProbeUsesDecryptedHeaders(t *testing.T) {
	recebido := make(chan string, 1)
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		recebido <- r.Header.Get("X-Token")
		w.WriteHeader(http.StatusOK)
	})
	b := novaBancada(t, client, MaxInFlight)
	b.alvo(t, srv.URL, func(p *db.InsertTargetParams) {
		enc, err := targets.SealHeaders(b.box, p.ID, map[string]string{"X-Token": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		p.HeadersEnc = enc
	})

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)
	select {
	case got := <-recebido:
		if got != "abc" {
			t.Errorf("X-Token = %q", got)
		}
	case <-time.After(esperaMaxima):
		t.Fatal("a sonda não chegou ao servidor")
	}
}

// transporteContador anota cada requisição que chegaria à rede.
type transporteContador struct {
	mu       sync.Mutex
	recebido []*http.Request
}

func (c *transporteContador) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recebido = append(c.recebido, r)
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
}

func (c *transporteContador) requisicoes() []*http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.recebido)
}

// O domínio de uma URL gravada com ẞ depende da tabela do IDNA: o alvo não
// pode trocar de destino em silêncio numa atualização, levando os headers.
// Nada de rede, e o dono vê o alvo fora.
func TestTargetWithUnstableHostIsRefusedBeforeDialing(t *testing.T) {
	const (
		nomeSecreto  = "X-Canario-Nome"
		valorSecreto = "canario-valor-8f3a"
	)
	for _, caso := range []struct{ nome, url, trecho string }{
		{"ẞ no host", "https://cliente-STRAẞE.de/saude", "cliente-STRA"},
		{"ẞ percent-encoded", "https://cliente-stra%E1%BA%9Ee.de/saude", "cliente-stra"},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			log := capturarLog(t)
			transporte := &transporteContador{}
			b := novaBancada(t, &http.Client{Transport: transporte}, MaxInFlight)
			alvo := b.alvo(t, caso.url, func(p *db.InsertTargetParams) {
				enc, err := targets.SealHeaders(b.box, p.ID, map[string]string{nomeSecreto: valorSecreto})
				if err != nil {
					t.Fatal(err)
				}
				p.HeadersEnc = enc
			})

			if _, err := b.s.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			b.s.Stop(esperaMaxima)

			if got := transporte.requisicoes(); len(got) != 0 {
				t.Fatalf("a sonda foi à rede: %d requisições, primeira para %s", len(got), got[0].URL.Host)
			}
			rows := b.checks(t, alvo)
			if len(rows) != 1 || rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
				t.Errorf("checks = %+v, queria um down/blocked", rows)
			}
			registro := log.String()
			if !strings.Contains(registro, alvo.String()) {
				t.Errorf("o log não identifica o alvo: %s", registro)
			}
			for _, proibido := range []string{caso.trecho, "xn--", nomeSecreto, valorSecreto} {
				if strings.Contains(registro, proibido) {
					t.Errorf("o log traz %q: %s", proibido, registro)
				}
			}
		})
	}
}

// A sonda recusada conta nas métricas como qualquer outra.
func TestRefusedTargetIsObservedInMetrics(t *testing.T) {
	b := novaBancada(t, &http.Client{Transport: &transporteContador{}}, MaxInFlight)
	alvo := b.alvo(t, "https://STRAẞE.de/saude", nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	if vistos := b.m.alvosVistos(); !slices.Contains(vistos, alvo) {
		t.Errorf("alvos vistos = %v; a sonda recusada de %s não foi observada", vistos, alvo)
	}
}

// A guarda vem antes de abrir os headers: com ela depois, headers ilegíveis
// sairiam pelo caminho do defeito nosso, sem check, e o dono não veria o alvo
// fora.
func TestRefusedTargetIsRecordedEvenWithUnreadableHeaders(t *testing.T) {
	transporte := &transporteContador{}
	b := novaBancada(t, &http.Client{Transport: transporte}, MaxInFlight)
	alvo := b.alvo(t, "https://STRAẞE.de/saude", func(p *db.InsertTargetParams) {
		p.HeadersEnc = []byte("headers que não abrem")
	})

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	if got := transporte.requisicoes(); len(got) != 0 {
		t.Fatalf("a sonda foi à rede: %d requisições, primeira para %s", len(got), got[0].URL.Host)
	}
	rows := b.checks(t, alvo)
	if len(rows) != 1 || rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
		t.Errorf("checks = %+v, queria um down/blocked", rows)
	}
}

// A recusa é só do ẞ: ß continua sondando.
func TestTargetWithSharpSKeepsProbing(t *testing.T) {
	transporte := &transporteContador{}
	b := novaBancada(t, &http.Client{Transport: transporte}, MaxInFlight)
	alvo := b.alvo(t, "https://straße.de/", nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	if got := transporte.requisicoes(); len(got) != 1 {
		t.Fatalf("%d requisições, queria 1", len(got))
	}
	if rows := b.checks(t, alvo); len(rows) != 1 || rows[0].Status != "up" {
		t.Errorf("checks = %+v, queria um up", rows)
	}
}

// New monta o agendador com o cliente de PRODUÇÃO. Um alvo em loopback tem
// de terminar bloqueado, e gravado como tal.
func TestNewUsesTheSSRFGuardedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	b := novaBancada(t, nil, 0)
	alvo := b.alvo(t, srv.URL, nil)

	if _, err := b.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.s.Stop(esperaMaxima)

	rows := b.checks(t, alvo)
	if len(rows) != 1 || rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
		t.Errorf("checks = %+v, queria um down/blocked", rows)
	}
}

// transportePanicante entra em panic dentro do client.Do, que é o ponto em
// que uma sonda real chama código que não é nosso.
type transportePanicante struct{}

func (transportePanicante) RoundTrip(*http.Request) (*http.Response, error) {
	panic("transporte sabotado pelo teste")
}

// Um panic numa sonda não derruba o processo: vira log, e a vaga e o
// scheduler_inflight voltam como numa saída normal. Sem o recover, o panic
// na goroutine mata o binário de teste inteiro.
func TestProbePanicIsLoggedAndReturnsTheSlot(t *testing.T) {
	log := capturarLog(t)
	b := novaBancada(t, &http.Client{Transport: transportePanicante{}}, 1)
	alvo := b.alvo(t, "http://alvo.exemplo/", nil)

	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1 disparo", n, err)
	}
	b.s.Stop(esperaMaxima)

	saida := log.String()
	if !strings.Contains(saida, "panic na sonda") || !strings.Contains(saida, alvo.String()) ||
		!strings.Contains(saida, "transporte sabotado pelo teste") {
		t.Errorf("log sem o panic da sonda e o alvo:\n%s", saida)
	}
	if v := testutil.ToFloat64(b.m.inflight); v != 0 {
		t.Errorf("scheduler_inflight = %v depois do panic, queria 0", v)
	}
	if rows := b.checks(t, alvo); len(rows) != 0 {
		t.Errorf("a sonda que entrou em panic foi gravada: %+v", rows)
	}
	// Com uma vaga só, este Tick só dispara se a vaga voltou.
	b.clk.Advance(2 * time.Minute)
	if n, err := b.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick depois do panic = %d, %v; queria 1: a vaga não voltou", n, err)
	}
}

// A linha reivindicada vira a Spec da sonda com as unidades certas, e a regra
// de conteúdo com o ponteiro já decodificado em tokens.
func TestSpecComesFromTheClaimedRow(t *testing.T) {
	limiar := int32(250)
	texto := func(s string) *string { return &s }
	for _, tc := range []struct {
		nome                  string
		limiar                *int32
		quer                  time.Duration
		modo, valor, ponteiro *string
		querRegra             *RegraDaSonda
	}{
		{nome: "com limiar", limiar: &limiar, quer: 250 * time.Millisecond},
		{nome: "sem limiar"},
		{
			nome: "contains", modo: texto("contains"), valor: texto("tudo certo"),
			querRegra: &RegraDaSonda{Mode: targets.ContentContains, Value: []byte("tudo certo")},
		},
		{
			nome: "not_contains", modo: texto("not_contains"), valor: texto("erro"),
			querRegra: &RegraDaSonda{Mode: targets.ContentNotContains, Value: []byte("erro")},
		},
		{
			nome: "json_equals com escapes no ponteiro", modo: texto("json_equals"), valor: texto("ok"),
			ponteiro:  texto("/a~1b/m~0n/0"),
			querRegra: &RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("ok"), Pointer: []string{"a/b", "m~n", "0"}},
		},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			s := &Scheduler{}
			spec, err := s.especificar(db.ClaimDueTargetsRow{
				ID: uuid.New(), URL: "https://exemplo.com", Method: http.MethodHead,
				TimeoutMS: 1500, ExpectedStatus: 204, DegradedThresholdMS: tc.limiar,
				ContentMode: tc.modo, ContentValue: tc.valor, ContentJsonPointer: tc.ponteiro,
			})
			if err != nil {
				t.Fatal(err)
			}
			if spec.URL != "https://exemplo.com" || spec.Method != http.MethodHead || spec.Timeout != 1500*time.Millisecond ||
				spec.ExpectedStatus != 204 || spec.DegradedThreshold != tc.quer || spec.Headers != nil {
				t.Errorf("spec = %+v, queria limiar de %s", spec, tc.quer)
			}
			if !reflect.DeepEqual(spec.Content, tc.querRegra) {
				t.Errorf("regra = %+v, queria %+v", spec.Content, tc.querRegra)
			}
		})
	}
}

// Um ponteiro que não abre é defeito nosso, como os headers que não abrem: a
// sonda não sai e nada é gravado, em vez de um down que acusaria o alvo.
func TestSpecWithAnUnreadablePointerFails(t *testing.T) {
	modo, valor, ponteiro := "json_equals", "ok", "/a~2"
	s := &Scheduler{}
	if _, err := s.especificar(db.ClaimDueTargetsRow{
		ID: uuid.New(), URL: "https://exemplo.com", Method: http.MethodGet,
		ContentMode: &modo, ContentValue: &valor, ContentJsonPointer: &ponteiro,
	}); err == nil {
		t.Error("especificar aceitou o ponteiro /a~2")
	}
}

func TestInt32Limited(t *testing.T) {
	for _, tc := range []struct {
		entrada int64
		quer    int32
	}{
		{-1, 0},
		{0, 0},
		{1500, 1500},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
	} {
		if got := int32Limitado(tc.entrada); got != tc.quer {
			t.Errorf("int32Limitado(%d) = %d, queria %d", tc.entrada, got, tc.quer)
		}
	}
}

func TestLooksLikeAVanishedTarget(t *testing.T) {
	pg := func(codigo string) error { return &pgconn.PgError{Code: codigo} }
	for _, tc := range []struct {
		nome string
		err  error
		quer bool
	}{
		{"RLS", pg("42501"), true},
		{"chave estrangeira", pg("23503"), true},
		{"RLS embrulhada", fmt.Errorf("gravar: %w", pg("42501")), true},
		{"outro código", pg("23505"), false},
		{"erro que não é do Postgres", errors.New("rede"), false},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			if got := pareceAlvoSumido(tc.err); got != tc.quer {
				t.Errorf("pareceAlvoSumido = %v, queria %v", got, tc.quer)
			}
		})
	}
}

// Sem resposta, o check fica sem status_code: NULL, e não zero.
func TestCheckWithoutResponseHasNoStatusCode(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET", IntervalSec: 60}

	b.s.gravar(a, b.clk.Now(), Result{Status: StatusDown, ErrorKind: KindTimeout})

	rows := b.checks(t, alvo)
	if len(rows) != 1 || rows[0].StatusCode != nil {
		t.Fatalf("checks = %+v; queria um, sem status_code", rows)
	}
}

// I1 da spec do PR 5. O corpo da resposta e o valor da regra são texto de
// terceiros e do dono: nenhum dos dois sai da sonda. Depois de três falhas
// por content_mismatch, com incidente aberto e aviso enfileirado, nenhum
// canário aparece no banco do sondador (checks, incidents, notifications), no
// log nem no /metrics.
func TestProbeBodyCanaryNeverLeaves(t *testing.T) {
	log := capturarLog(t)
	canarioDoCorpo := "CANARY-" + uuid.NewString()
	canarioDaRegra := "CANARY-" + uuid.NewString()
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, canarioDoCorpo)
	})
	b := novaBancada(t, client, MaxInFlight)
	b.canal(t, true, true)
	valor := "outra coisa " + canarioDaRegra
	alvo := b.alvo(t, srv.URL, alvoComContains(valor))
	for range 3 {
		b.rodada(t)
	}
	b.s.Stop(esperaMaxima)

	rows := b.checks(t, alvo)
	if len(rows) != 3 || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "content_mismatch" {
		t.Fatalf("checks = %+v; queria três content_mismatch", rows)
	}
	var linhas []string
	b.comoDono(t, func(tx pgx.Tx) {
		for _, tabela := range []string{"checks", "incidents", "notifications"} {
			var n int
			var texto string
			err := tx.QueryRow(context.Background(),
				`SELECT count(*), coalesce(string_agg(row_to_json(t)::text, E'\n'), '') FROM `+tabela+` t`).Scan(&n, &texto)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatalf("nenhuma linha em %s: o teste não olharia nada", tabela)
			}
			linhas = append(linhas, texto)
		}
	})

	metricas := textoDoMetrics(t, b.reg)
	for _, canario := range []string{canarioDoCorpo, canarioDaRegra} {
		for i, tabela := range []string{"checks", "incidents", "notifications"} {
			if strings.Contains(linhas[i], canario) {
				t.Errorf("o canário %s chegou a %s:\n%s", canario, tabela, linhas[i])
			}
		}
		if strings.Contains(log.String(), canario) {
			t.Errorf("o canário %s chegou ao log:\n%s", canario, log)
		}
		if strings.Contains(metricas, canario) {
			t.Errorf("o canário %s chegou ao /metrics", canario)
		}
	}
}

// textoDoMetrics devolve o texto que o /metrics serviria com o registro dado.
func textoDoMetrics(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	w := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "probe_total") {
		t.Fatalf("/metrics = %d, sem as séries do sondador:\n%s", w.Code, w.Body)
	}
	return w.Body.String()
}
