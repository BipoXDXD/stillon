package prober

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

const esperaMaxima = 5 * time.Second

type bancada struct {
	s    *Scheduler
	pool *pgxpool.Pool
	clk  *clock.Fake
	box  *crypto.Box
	m    *Metrics
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

	m := NewMetrics(prometheus.NewRegistry())
	var s *Scheduler
	if client == nil {
		s = New(pool, box, clk, m)
	} else {
		s = newScheduler(pool, box, clk, m, client, vagas)
	}
	// Registrado depois do testdb.New: cleanups rodam em ordem inversa, então
	// as sondas terminam antes de o banco ser restaurado.
	t.Cleanup(func() { s.Stop(esperaMaxima) })
	return &bancada{s: s, pool: pool, clk: clk, box: box, m: m, dono: dono}
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

	close(liberar)
	b.s.Stop(esperaMaxima)
}

// Spec §6.2 e §14: dois sondadores sobre o mesmo banco nunca sondam o mesmo
// alvo duas vezes.
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

	var largada sync.WaitGroup
	largada.Add(1)
	var soma atomic.Int32
	var fim sync.WaitGroup
	for _, s := range []*Scheduler{b.s, outro} {
		fim.Add(1)
		go func() {
			defer fim.Done()
			largada.Wait()
			n, err := s.Tick(context.Background())
			if err != nil {
				t.Error(err)
			}
			soma.Add(int32(n))
		}()
	}
	largada.Done()
	fim.Wait()
	b.s.Stop(esperaMaxima)
	outro.Stop(esperaMaxima)

	if soma.Load() != total {
		t.Errorf("os dois dispararam %d, queria %d", soma.Load(), total)
	}
	batidas.Range(func(caminho, c any) bool {
		if n := c.(*atomic.Int32).Load(); n != 1 {
			t.Errorf("%s sondado %d vezes", caminho, n)
		}
		return true
	})
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
	if got := <-recebido; got != "abc" {
		t.Errorf("X-Token = %q", got)
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
