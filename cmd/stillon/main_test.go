package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

var chave = []byte("chave-mestra-de-teste-32-bytes!!")

// O teste que junta tudo, com o cliente de PRODUÇÃO: um alvo cadastrado
// apontando para 127.0.0.1 tem de ser sondado pelo processo real e gravado
// como bloqueado. Se o wiring trocasse o cliente, ou esquecesse o sondador,
// ou as partições, este teste pegaria.
//
// Usa relógio real, porque run monta clock.Real. A espera é por sondagem
// com prazo, não por sleep fixo.
func TestProberRoleProbesWithTheProductionClient(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'e2e@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	alvo := uuid.Must(uuid.NewV7())
	agora := time.Now()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora.Add(-time.Minute), CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t),
		MasterKey: chave, ShutdownGrace: 2 * time.Second,
		// MetricsAddr vazio: sem servidor de métricas neste teste.
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	var rows []db.ListChecksRow
	prazo := time.After(15 * time.Second)
	passo := time.NewTicker(100 * time.Millisecond)
	defer passo.Stop()
	for len(rows) == 0 {
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes de sondar: %v", err)
		case <-prazo:
			t.Fatal("nenhum check gravado em 15s: o sondador não está no wiring")
		case <-passo.C:
		}
		err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
			var err error
			rows, err = q.ListChecks(ctx, db.ListChecksParams{
				UserID: dono, TargetID: alvo, FromAt: agora.Add(-time.Hour), ToAt: time.Now().Add(time.Hour),
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if rows[0].Status != "down" || rows[0].ErrorKind == nil || *rows[0].ErrorKind != "blocked" {
		t.Errorf("check = %+v, queria down/blocked: o cliente do wiring não é o do safedial", rows[0])
	}

	cancel()
	select {
	case err := <-fim:
		if err != nil {
			t.Errorf("run encerrou com erro: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run não encerrou depois do cancelamento")
	}
}

func TestRunRefusesARoleThatBypassesRLS(t *testing.T) {
	testdb.New(t) // o cleanup restaura o banco
	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.AdminDSN(t),
		MasterKey: chave, ShutdownGrace: time.Second,
	}
	// Com prazo: sem a checagem, run subiria normalmente e só voltaria no
	// cancelamento. O teste ficaria pendurado em vez de vermelho.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := run(ctx, cfg)
	if !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("run com superusuário = %v, queria ErrRoleBypassesRLS", err)
	}
}

// O boot recusa o papel ANTES de migrar. Na ordem inversa, o primeiro boot
// como superusuário num banco vazio criaria todas as tabelas com o dono
// errado e só depois recusaria; trocar para o papel comum encontraria
// tabelas alheias e migrations já marcadas como aplicadas. Só o erro não
// basta: ele sai igual nas duas ordens. O que trava a ordem é o banco
// continuar vazio.
func TestRunRefusesTheRoleBeforeMigrating(t *testing.T) {
	// Banco vazio e não migrado, acessado como superusuário.
	vazio, err := url.Parse(testdb.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := url.Parse(testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	vazio.User = admin.User

	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: vazio.String(),
		MasterKey: chave, ShutdownGrace: time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, cfg); !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("run com superusuário = %v, queria ErrRoleBypassesRLS", err)
	}

	conn, err := pgx.Connect(ctx, vazio.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tabelas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(tabelas) != 0 {
		t.Errorf("o boot recusado criou tabelas no banco vazio: %v", tabelas)
	}
}

// Um subsistema que cai derruba o processo. Com a porta da API ocupada, run
// tem de VOLTAR com erro, e não ficar vivo servindo só métricas e sondas.
func TestRunFailsWhenAServerCannotStart(t *testing.T) {
	testdb.New(t)
	ocupada, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ocupada.Close()

	cfg := &config.Config{
		Role: config.RoleAll, DatabaseURL: testdb.DSN(t), HTTPAddr: ocupada.Addr().String(),
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave,
		ShutdownGrace: time.Second,
	}
	fim := make(chan error, 1)
	go func() { fim <- run(context.Background(), cfg) }()

	select {
	case err := <-fim:
		// Qualquer erro de boot (banco fora, papel recusado) também faria
		// run voltar com erro. O teste exige o erro CERTO: a porta ocupada,
		// e na API.
		if !errors.Is(err, syscall.EADDRINUSE) || !strings.Contains(err.Error(), "servidor api") {
			t.Errorf("run = %v, queria o EADDRINUSE do servidor api", err)
		}
		// A dica do METRICS_ADDR é só da porta de métricas (M7, Plano 9).
		if err != nil && strings.Contains(err.Error(), "METRICS_ADDR") {
			t.Errorf("a porta da API ocupada sugeriu o METRICS_ADDR: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run seguiu vivo com a API morta")
	}
}

// M7 (Plano 9, §3.4): com a porta de métricas ocupada (outro processo do
// Vigilume no mesmo host, o caso de --role=api e --role=prober), o boot cai
// com o EADDRINUSE do servidor de métricas, e a mensagem sugere o
// METRICS_ADDR. A perna oposta (a porta da API ocupada não sugere) está em
// TestRunFailsWhenAServerCannotStart.
func TestBusyMetricsPortSuggestsMetricsAddr(t *testing.T) {
	testdb.New(t)
	ocupada, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ocupada.Close()

	cfg := &config.Config{
		Role: config.RoleAPI, DatabaseURL: testdb.DSN(t), HTTPAddr: "127.0.0.1:0",
		MetricsAddr:   ocupada.Addr().String(),
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave,
		ShutdownGrace: time.Second,
	}
	fim := make(chan error, 1)
	go func() { fim <- run(context.Background(), cfg) }()

	select {
	case err := <-fim:
		if !errors.Is(err, syscall.EADDRINUSE) || !strings.Contains(err.Error(), "servidor métricas") {
			t.Fatalf("run = %v, queria o EADDRINUSE do servidor de métricas", err)
		}
		if !strings.Contains(err.Error(), "METRICS_ADDR") {
			t.Errorf("a mensagem não sugere o METRICS_ADDR: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run seguiu vivo com as métricas mortas")
	}
}

// O sondador inteiro tem de caber no ShutdownGrace: Stop leva até
// grace + PersistTimeout, então o wiring desconta PersistTimeout.
func TestProberGraceFitsTheShutdownBudget(t *testing.T) {
	for _, c := range []struct{ total, quer time.Duration }{
		{15 * time.Second, 10 * time.Second},
		// Não cabe (só em teste): o grace inteiro, nunca zero ou negativo.
		{prober.PersistTimeout, prober.PersistTimeout},
		{2 * time.Second, 2 * time.Second},
	} {
		if got := graceDoSondador(c.total); got != c.quer {
			t.Errorf("graceDoSondador(%v) = %v, queria %v", c.total, got, c.quer)
		}
	}
}
