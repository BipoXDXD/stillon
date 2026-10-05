package prober_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

func TestProbeInvalidRequestPublicComplementar(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	t.Cleanup(client.CloseIdleConnections)
	for _, spec := range []prober.Spec{
		{URL: "://url-invalida", Method: "GET", Timeout: time.Second},
		{URL: srv.URL, Method: "GET\n", Timeout: time.Second},
	} {
		got := prober.Probe(t.Context(), client, spec)
		want := prober.Result{Status: prober.StatusDown, ErrorKind: prober.KindUnknown}
		if got != want {
			t.Errorf("requisição inválida = %+v; queria %+v", got, want)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requisições chegaram ao servidor para entradas inválidas", n)
	}
}

func TestClassifyGenericTimeoutPublicComplementar(t *testing.T) {
	for _, err := range []error{
		os.ErrDeadlineExceeded,
		&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
		fmt.Errorf("sentinela-privada: %w", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}),
	} {
		if got := prober.Classify(err); got != prober.KindTimeout {
			t.Errorf("timeout genérico classificado como %q; queria timeout", got)
		}
	}
}

// Só APIs exportadas constroem e observam esta bancada. O cliente de New
// permanece o guarded de produção; loopback é bloqueado antes da rede.
type bancadaPublica struct {
	pool  *pgxpool.Pool
	clk   *clock.Fake
	box   *crypto.Box
	reg   *prometheus.Registry
	s     *prober.Scheduler
	owner uuid.UUID
}

func novaBancadaPublica(t *testing.T) *bancadaPublica {
	t.Helper()
	b := &bancadaPublica{pool: testdb.New(t), clk: clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)), reg: prometheus.NewRegistry(), owner: uuid.Must(uuid.NewV7())}
	if err := storage.EnsureChecksPartitions(t.Context(), b.pool, b.clk.Now()); err != nil {
		t.Fatal(err)
	}
	var err error
	b.box, err = crypto.NewBox([]byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.pool.Exec(t.Context(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, b.owner, b.owner.String()+"@exemplo.test"); err != nil {
		t.Fatal(err)
	}
	b.s = prober.New(b.pool, b.box, b.clk, prober.NewMetrics(b.reg))
	t.Cleanup(func() { b.s.Stop(5 * time.Second) })
	return b
}

func (b *bancadaPublica) alvo(t *testing.T, enabled bool, headers []byte) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	b.inserirAlvo(t, id, enabled, headers)
	return id
}

func (b *bancadaPublica) inserirAlvo(t *testing.T, id uuid.UUID, enabled bool, headers []byte) {
	t.Helper()
	p := db.InsertTargetParams{ID: id, UserID: b.owner, Name: "alvo público", URL: "http://127.0.0.1:1", Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200, Enabled: enabled, HeadersEnc: headers, NextRunAt: b.clk.Now(), CreatedAt: b.clk.Now()}
	if err := storage.InUserTx(t.Context(), b.pool, b.owner, func(q *db.Queries) error { return q.InsertTarget(t.Context(), p) }); err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaPublica) checks(t *testing.T, id uuid.UUID) []db.ListChecksRow {
	t.Helper()
	var rows []db.ListChecksRow
	err := storage.InUserTx(t.Context(), b.pool, b.owner, func(q *db.Queries) error {
		var err error
		rows, err = q.ListChecks(t.Context(), db.ListChecksParams{UserID: b.owner, TargetID: id, FromAt: b.clk.Now().Add(-time.Hour), ToAt: b.clk.Now().Add(time.Hour)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func metricasPublicas(t *testing.T, reg *prometheus.Registry, name string) []*dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f.Metric
		}
	}
	return nil
}

func gaugePublico(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	ms := metricasPublicas(t, reg, name)
	if len(ms) != 1 || ms[0].Gauge == nil {
		t.Fatalf("gauge público %s ausente ou ambíguo", name)
	}
	return ms[0].Gauge.GetValue()
}

func esperarEstadoPublico(t *testing.T, prazo time.Duration, desc string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(prazo)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("estado público não observado em %s: %s", prazo, desc)
		case <-poll.C:
		}
	}
}

func (b *bancadaPublica) rodada(t *testing.T, want int) {
	t.Helper()
	if n, err := b.s.Tick(t.Context()); err != nil || n != want {
		t.Fatalf("Tick = %d, %v; queria %d", n, err, want)
	}
	esperarEstadoPublico(t, 5*time.Second, "sondas concluídas", func() bool { return gaugePublico(t, b.reg, "scheduler_inflight") == 0 })
}

func (b *bancadaPublica) incidente(t *testing.T, id uuid.UUID) {
	t.Helper()
	err := storage.InProberTx(t.Context(), b.pool, func(q *db.Queries) error {
		return q.OpenIncident(t.Context(), db.OpenIncidentParams{ID: uuid.Must(uuid.NewV7()), TargetID: id, StartedAt: b.clk.Now(), Cause: "blocked"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaPublica) apagar(t *testing.T, id uuid.UUID) {
	t.Helper()
	err := storage.InUserTx(t.Context(), b.pool, b.owner, func(q *db.Queries) error {
		n, err := q.DeleteTarget(t.Context(), db.DeleteTargetParams{UserID: b.owner, ID: id})
		if err == nil && n != 1 {
			return fmt.Errorf("DeleteTarget removeu %d linhas", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func iniciarRunPublico(t *testing.T, s *prober.Scheduler) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, 5*time.Second) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run ao cancelar = %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("Run não encerrou depois do cancelamento")
		}
	})
	return done
}

type handlerPublico struct{ messages chan string }

func (h handlerPublico) Enabled(context.Context, slog.Level) bool { return true }
func (h handlerPublico) Handle(_ context.Context, r slog.Record) error {
	select {
	case h.messages <- r.Message:
	default:
	}
	return nil
}
func (h handlerPublico) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h handlerPublico) WithGroup(string) slog.Handler      { return h }

func TestRunContinuesAfterPublicPoolFailureComplementar(t *testing.T) {
	b := novaBancadaPublica(t)
	h := handlerPublico{messages: make(chan string, 16)}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	b.pool.Close()
	done := iniciarRunPublico(t, b.s)
	seen := map[string]bool{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !seen["reconciliar métricas"] || !seen["despachar sondas"] {
		select {
		case msg := <-h.messages:
			seen[msg] = true
		case err := <-done:
			t.Fatalf("Run encerrou com contexto vivo: %v", err)
		case <-deadline.C:
			t.Fatalf("Run não reportou falhas públicas: %v", seen)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("Run não permaneceu ativo após falha: %v", err)
	default:
	}
}

func TestRunPeriodicReconcilePublicComplementar(t *testing.T) {
	b := novaBancadaPublica(t)
	series := b.alvo(t, true, nil)
	b.rodada(t, 1)
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
		t.Fatalf("%d séries antes de Run; queria 1", n)
	}
	// O alvo do incidente fica desabilitado: nenhum Tick pode produzir
	// transição. Gauge 1 é a barreira observável da reconciliação inicial.
	b.incidente(t, b.alvo(t, false, nil))
	iniciarRunPublico(t, b.s)
	esperarEstadoPublico(t, 5*time.Second, "reconciliação inicial", func() bool { return gaugePublico(t, b.reg, "incidents_open") == 1 })
	b.apagar(t, series)
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
		t.Fatalf("série sumiu antes do período: %d", n)
	}
	// Até um ciclo de despacho completo, a série deve sobreviver: apagar
	// pelo serviço do banco não envia a transição privada do scheduler.
	until := time.NewTimer(1500 * time.Millisecond)
	defer until.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
watch:
	for {
		select {
		case <-until.C:
			break watch
		case <-poll.C:
			if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
				t.Fatalf("série removida antes da reconciliação periódica: %d", n)
			}
		}
	}
	// Custo intencional: período real de 30 s, sem alterar constante ou
	// relógio interno. Timeout é uma falha explícita do estado observado.
	esperarEstadoPublico(t, 35*time.Second, "série apagada pela reconciliação periódica", func() bool { return len(metricasPublicas(t, b.reg, "probe_duration_seconds")) == 0 })
}

func TestRunReconcilesPublicIncidentTransitionComplementar(t *testing.T) {
	b := novaBancadaPublica(t)
	id := b.alvo(t, true, nil)
	for range 2 {
		b.rodada(t, 1)
		// O intervalo tem jitter de até 10%; dois minutos vencem qualquer
		// agendamento sem atravessar a lacuna de três intervalos.
		b.clk.Advance(2 * time.Minute)
	}
	b.incidente(t, b.alvo(t, false, nil))
	iniciarRunPublico(t, b.s)
	esperarEstadoPublico(t, 5*time.Second, "reconciliação inicial com incidente extra", func() bool { return gaugePublico(t, b.reg, "incidents_open") == 1 })
	// A terceira rodada vem de Run: não disputamos sua reivindicação
	// com um Tick manual concorrente. A persistência é observável no banco.
	esperarEstadoPublico(t, 5*time.Second, "terceira falha gravada por Run", func() bool { return len(b.checks(t, id)) == 3 })
	esperarEstadoPublico(t, 5*time.Second, "transição refletida antes dos 30 s", func() bool { return gaugePublico(t, b.reg, "incidents_open") == 2 })
}

func TestReconcilePreservesSeriesAfterPublicQueryFailureComplementar(t *testing.T) {
	b := novaBancadaPublica(t)
	id := b.alvo(t, true, nil)
	b.rodada(t, 1)
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
		t.Fatalf("%d séries antes da falha; queria 1", n)
	}
	// DDL literal somente no banco descartável, depois das sondas. O nome
	// não vem de dados externos. Restaurar precede cleanup/Restore do banco.
	if _, err := b.pool.Exec(t.Context(), `ALTER TABLE targets RENAME TO targets_fixture_indisponivel`); err != nil {
		t.Fatal(err)
	}
	renamed := true
	t.Cleanup(func() {
		if renamed {
			if _, err := b.pool.Exec(context.Background(), `ALTER TABLE targets_fixture_indisponivel RENAME TO targets`); err != nil {
				t.Errorf("restaurar relação da fixture: %v", err)
			}
		}
	})
	err := b.s.Reconcile(t.Context())
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "42P01" || !strings.Contains(err.Error(), "reconciliar métricas") {
		t.Fatalf("Reconcile não preservou contexto/causa SQL: %v", err)
	}
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
		t.Fatalf("falha de consulta apagou séries: %d", n)
	}
	if _, err := b.pool.Exec(t.Context(), `ALTER TABLE targets_fixture_indisponivel RENAME TO targets`); err != nil {
		t.Fatal(err)
	}
	renamed = false
	if err := b.s.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile após recovery: %v", err)
	}
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 1 {
		t.Fatalf("recovery apagou série viva: %d", n)
	}
	b.apagar(t, id)
	if err := b.s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 0 {
		t.Fatalf("recovery não removeu série de alvo apagado: %d", n)
	}
}

func TestSchedulerWrongKeyReturnsPublicSlotsWithoutChecksComplementar(t *testing.T) {
	b := novaBancadaPublica(t)
	other, err := crypto.NewBox([]byte(strings.Repeat("b", 32)))
	if err != nil {
		t.Fatal(err)
	}
	bad := make([]uuid.UUID, prober.MaxInFlight)
	for i := range bad {
		bad[i] = uuid.Must(uuid.NewV7())
		sealed, err := targets.SealHeaders(other, bad[i], map[string]string{"X-Token": "sentinela-da-fixture"})
		if err != nil {
			t.Fatal(err)
		}
		b.inserirAlvo(t, bad[i], true, sealed)
	}
	b.rodada(t, len(bad))
	for _, id := range bad {
		if n := len(b.checks(t, id)); n != 0 {
			t.Errorf("headers indecifráveis geraram %d checks", n)
		}
	}
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != 0 {
		t.Fatalf("headers indecifráveis geraram %d séries", n)
	}
	good := make([]uuid.UUID, prober.MaxInFlight)
	for i := range good {
		good[i] = b.alvo(t, true, nil)
	}
	// Stop só roda no cleanup: a segunda rodada precisa usar o mesmo
	// scheduler/contexto para demonstrar que todas as vagas voltaram.
	b.rodada(t, len(good))
	for _, id := range good {
		rows := b.checks(t, id)
		if len(rows) != 1 || rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
			t.Errorf("rodada válida pelo cliente guarded não gravou bloqueio: %+v", rows)
		}
	}
	if n := len(metricasPublicas(t, b.reg, "probe_duration_seconds")); n != len(good) {
		t.Errorf("%d séries após vagas devolvidas; queria %d", n, len(good))
	}
}
