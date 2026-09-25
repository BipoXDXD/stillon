package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// O caminho do Plano 4 no processo real, papel all: os checks de uma hora
// já fechada viram rollup na manutenção que roda ao subir (sem esperar os
// 15 minutos do ticker), e /p/{slug} mostra o uptime deles sem
// autenticação. Prova o wiring de RunMaintenance, de StatusPages no Deps e
// da cadeia /p/.
//
// O alvo está desativado: o sondador não o toca, e o estado atual sai como
// "Sem dados", mas o uptime dos 90 dias vem dos rollups.
func TestAllRoleConsolidatesAndServesThePublicPage(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono, alvo, pagina := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'p4@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	agora := time.Now()
	hora := agora.UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "nome-interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: false, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: pagina, UserID: dono, Slug: "empresa-e2e", Title: "Status E2E", Published: true, CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pagina, TargetID: alvo, DisplayName: "API pública", Position: 0,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureChecksPartitions(ctx, pool, hora); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		p := db.InsertCheckParams{ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: hora.Add(time.Duration(i) * time.Minute), Status: "up", LatencyMS: 80}
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
			t.Fatal(err)
		}
	}

	addr := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleAll, DatabaseURL: testdb.DSN(t), HTTPAddr: addr, MetricsAddr: portaLivre(t),
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave, ShutdownGrace: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	// Espera o sinal certo antes de ler a página: o rollup da hora no banco.
	// Ler /p/ antes dele põe no cache (30 s) uma página sem o uptime, e um
	// poll rápido de /p/ esgota o limitador da página (burst 60, 2/s): sob
	// carga, as duas coisas faziam o teste falhar com 429 ou por prazo.
	// A leitura é como superusuário, por fora da RLS.
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	prazo := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM check_rollup_1h WHERE target_id = $1`, alvo).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(prazo) {
			t.Fatal("a manutenção não consolidou a hora em 30s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Com o rollup gravado, a primeira resposta 200 já tem de trazer o
	// uptime. O poll a 500 ms (o ritmo de reposição do limitador) só espera o
	// servidor HTTP subir; não esgota o burst nem em 15 s.
	var corpo string
	prazo = time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(prazo) {
			t.Fatalf("a página não respondeu 200 em 15s:\n%s", corpo)
		}
		resp, err := http.Get("http://" + addr + "/p/empresa-e2e")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		corpo = string(b)
		if resp.StatusCode == http.StatusOK {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(corpo, "100,00% no ar em 90 dias") {
		t.Errorf("a página não mostrou o uptime consolidado:\n%s", corpo)
	}
	for _, quer := range []string{"Status E2E", "API pública", "Sem dados", "latência típica 80 ms"} {
		if !strings.Contains(corpo, quer) {
			t.Errorf("a página não tem %q", quer)
		}
	}
	if strings.Contains(corpo, "nome-interno") || strings.Contains(corpo, "127.0.0.1") {
		t.Error("a página pública vazou o nome interno ou a URL do alvo")
	}

	cancel()
	select {
	case err := <-fim:
		if err != nil {
			t.Errorf("run encerrou com erro: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run não encerrou depois do cancelamento")
	}
}
