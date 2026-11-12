package prober

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Apagar um alvo PARADO não passa pelo sondador. A série
// dele só some na reconciliação.
func TestReconcileForgetsSeriesOfDeletedTargets(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	vivo := b.alvo(t, srv.URL, nil)
	apagado := b.alvo(t, srv.URL, nil)
	b.rodada(t)
	if n := testutil.CollectAndCount(b.m.duration); n != 2 {
		t.Fatalf("%d séries depois de sondar dois alvos", n)
	}

	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: apagado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(b.m.duration); n != 1 {
		t.Errorf("%d séries depois da reconciliação, queria 1", n)
	}
	if vistos := b.m.alvosVistos(); len(vistos) != 1 || vistos[0] != vivo {
		t.Errorf("alvos vistos = %v, queria só o vivo %s", vistos, vivo)
	}
}

func TestReconcileReportsOpenIncidents(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	for range 2 {
		alvo := b.alvo(t, "https://exemplo.com", nil)
		err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
			return q.OpenIncident(ctx, db.OpenIncidentParams{
				ID: uuid.Must(uuid.NewV7()), TargetID: alvo, StartedAt: b.clk.Now(), Cause: "dns",
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.incidentsOpen); v != 2 {
		t.Errorf("incidents_open = %v, queria 2", v)
	}
}

// O log de sinais é um acúmulo: a métrica mostra se o teto e a retenção seguram.
func TestReconcileReportsTheHeartbeatEventLogSize(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: b.dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: b.clk.Now(), DeadlineAt: b.clk.Now().Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	ping := heartbeat.NewPingService(b.pool, b.clk)
	for _, sig := range []heartbeat.Signal{heartbeat.SignalStart, heartbeat.SignalSuccess, heartbeat.SignalFail} {
		if err := ping.Ping(ctx, token, sig, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.heartbeatEvents); v != 3 {
		t.Errorf("heartbeat_events = %v, queria 3", v)
	}
}

// O tamanho do log é só uma métrica: se a contagem falhar, o forget e o
// incidents_open do ciclo acontecem do mesmo jeito, o erro vai ao log e o gauge
// de tamanho fica com o último valor bom, sem um 0 falso.
func TestReconcileKeepsGoingWhenTheHeartbeatEventCountFails(t *testing.T) {
	log := capturarLog(t)
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	vivo := b.alvo(t, srv.URL, nil)
	apagado := b.alvo(t, srv.URL, nil)
	b.rodada(t)

	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: vivo, StartedAt: b.clk.Now(), Cause: "dns",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: apagado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	b.m.heartbeatEvents.Set(7)
	// DDL literal só no banco descartável do teste: a contagem passa a falhar
	// com 42P01 e nenhuma outra consulta do ciclo toca a tabela.
	if _, err := b.pool.Exec(ctx, `ALTER TABLE heartbeat_events RENAME TO heartbeat_events_indisponivel`); err != nil {
		t.Fatal(err)
	}

	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile = %v; a falha da contagem não pode abortar o ciclo", err)
	}

	if n := testutil.CollectAndCount(b.m.duration); n != 1 {
		t.Errorf("%d séries depois do ciclo, queria 1 (forget do alvo apagado)", n)
	}
	if v := testutil.ToFloat64(b.m.incidentsOpen); v != 1 {
		t.Errorf("incidents_open = %v, queria 1", v)
	}
	if v := testutil.ToFloat64(b.m.heartbeatEvents); v != 7 {
		t.Errorf("heartbeat_events = %v, queria 7 (o último valor bom)", v)
	}
	for _, trecho := range []string{"level=ERROR", `msg="contar eventos de heartbeat"`, "42P01"} {
		if !strings.Contains(log.String(), trecho) {
			t.Errorf("log sem %q:\n%s", trecho, log.String())
		}
	}
}

// Acima de uns 17 mil registros o JIT entra no count e passa a ser a maior parte
// do tempo dele; a contagem, com ou sem JIT, continua a exata.
func TestReconcileReportsTheExactHeartbeatEventLogSizeAboveTheJitThreshold(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	_, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: b.dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: b.clk.Now(), DeadlineAt: b.clk.Now().Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	// gen_random_uuid e não uuidv7(): o CI roda o Postgres 17, que não tem a segunda.
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind)
		SELECT gen_random_uuid(), $1, $2, 'success' FROM generate_series(1, 20000)`, id, b.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.heartbeatEvents); v != 20000 {
		t.Errorf("heartbeat_events = %v, queria 20000", v)
	}
}

// espiaoDeTx observa o valor de jit na conexão logo antes de cada instrução que
// passa por ela, sem alterar a instrução.
type espiaoDeTx struct {
	pgx.Tx
	jit *[]string
}

func (e espiaoDeTx) observar(ctx context.Context) {
	var v string
	if err := e.Tx.QueryRow(ctx, `SELECT current_setting('jit')`).Scan(&v); err != nil {
		v = err.Error()
	}
	*e.jit = append(*e.jit, v)
}

func (e espiaoDeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.observar(ctx)
	return e.Tx.Exec(ctx, sql, args...)
}

func (e espiaoDeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.observar(ctx)
	return e.Tx.Query(ctx, sql, args...)
}

func (e espiaoDeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	e.observar(ctx)
	return e.Tx.QueryRow(ctx, sql, args...)
}

type espiaoDeConexao struct {
	*pgxpool.Conn
	jit *[]string
}

func (c espiaoDeConexao) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := c.Conn.Begin(ctx)
	return espiaoDeTx{Tx: tx, jit: c.jit}, err
}

// O JIT fica desligado só durante a contagem (SET LOCAL na mesma transação e na
// mesma conexão) e a conexão devolvida ao pool volta ao valor de antes.
func TestHeartbeatEventCountRunsWithJitOffOnlyInsideItsTransaction(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	jitDaConexao := func() string {
		var v string
		if err := conn.QueryRow(ctx, `SELECT current_setting('jit')`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := jitDaConexao(); v != "on" {
		t.Fatalf("jit antes = %q, queria on", v)
	}

	var vistos []string
	total, err := contarEventosDeHeartbeat(ctx, espiaoDeConexao{Conn: conn, jit: &vistos})
	if err != nil {
		t.Fatal(err)
	}

	if total != 0 {
		t.Errorf("total = %d, queria 0", total)
	}
	// Três instruções na transação: o escopo da RLS, o SET LOCAL e o count.
	if want := []string{"on", "on", "off"}; !slices.Equal(vistos, want) {
		t.Errorf("jit antes de cada instrução = %v, queria %v", vistos, want)
	}
	if v := jitDaConexao(); v != "on" {
		t.Errorf("jit da conexão depois do commit = %q, queria on", v)
	}
}

// Uma transição avisa o laço de Run, para que incidents_open não espere a
// reconciliação periódica de 30s.
func TestTransitionSignalsTheReconcileLoop(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET"}
	falha := Result{Status: StatusDown, ErrorKind: KindDNS}

	b.s.gravar(a, b.clk.Now(), falha)
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 0 {
		t.Fatal("duas falhas, sem transição, já avisaram o laço")
	}
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 1 {
		t.Error("a abertura do incidente não avisou o laço de reconciliação")
	}
}
