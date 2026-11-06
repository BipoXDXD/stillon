package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// esperaDoAvaliador passa do intervalo do avaliador: o teste de ausência só
// prova algo se um avaliador ligado por engano tivesse tido tempo de agir.
const esperaDoAvaliador = 6 * time.Second

// heartbeatVencido cria um heartbeat com o prazo uma hora no passado.
func heartbeatVencido(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	dono, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	agora := time.Now()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: agora, DeadlineAt: agora.Add(-time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func contarComoSondador(t *testing.T, pool *pgxpool.Pool, sql string, id uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, sql, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const (
	incidentesDoHeartbeat = `SELECT count(*) FROM incidents WHERE heartbeat_id = $1`
	eventosDoHeartbeat    = `SELECT count(*) FROM heartbeat_events WHERE heartbeat_id = $1`
)

func TestProberRoleEvaluatesHeartbeats(t *testing.T) {
	pool := testdb.New(t)
	id, _ := heartbeatVencido(t, pool)
	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t), MetricsAddr: portaLivre(t),
		MasterKey: chave, ShutdownGrace: time.Second,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	prazo := time.Now().Add(20 * time.Second)
	for contarComoSondador(t, pool, incidentesDoHeartbeat, id) != 1 {
		if time.Now().After(prazo) {
			t.Fatal("o papel prober não abriu o incidente em 20s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-fim; err != nil {
		t.Errorf("run: %v", err)
	}
}

// O papel api recebe o ping e só registra: quem abre incidente é o prober.
func TestAPIRoleRecordsPingsButDoesNotEvaluate(t *testing.T) {
	pool := testdb.New(t)
	id, token := heartbeatVencido(t, pool)
	addr := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleAPI, DatabaseURL: testdb.DSN(t), HTTPAddr: addr,
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave, ShutdownGrace: time.Second,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	cliente := clienteDeE2E()
	pedir := func(metodo, caminho string) int {
		req, err := http.NewRequest(metodo, "http://"+addr+caminho, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := cliente.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	prazo := time.Now().Add(10 * time.Second)
	for pedir(http.MethodGet, "/readyz") != http.StatusOK {
		if time.Now().After(prazo) {
			t.Fatal("/readyz não ficou 200 em 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := pedir(http.MethodPost, "/ping/"+token+"/fail"); s != http.StatusOK {
		t.Fatalf("ping deu %d", s)
	}
	if n := contarComoSondador(t, pool, eventosDoHeartbeat, id); n != 1 {
		t.Errorf("%d eventos, queria 1", n)
	}
	time.Sleep(esperaDoAvaliador)
	if n := contarComoSondador(t, pool, incidentesDoHeartbeat, id); n != 0 {
		t.Errorf("o papel api abriu %d incidentes", n)
	}
	cancel()
	if err := <-fim; err != nil {
		t.Errorf("run: %v", err)
	}
}
