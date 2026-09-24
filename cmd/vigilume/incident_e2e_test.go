package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// O caminho do Plano 3 no processo real, com o cliente e o validador de
// PRODUÇÃO: um alvo que falha abre incidente depois de três sondas, a
// abertura vai para a outbox, e o dispatcher tenta o canal Discord. O canal
// tem uma URL interna, que só entra por fora da API; o envio a recusa, falha
// de vez e audita. incidents_open aparece em /metrics.
//
// Duas guardas recusam essa URL: a revalidação da allowlist no envio e,
// atrás dela, o safedial. O teste prova o fluxo, não qual das duas agiu; a
// revalidação sozinha é travada por TestDiscordRevalidatesTheURLBeforeSending.
//
// Relógio real, como TestProberRoleProbesWithTheProductionClient. Para não
// esperar três intervalos de 60s, o teste devolve o alvo à fila (next_run_at
// no passado) a cada check gravado.
func TestIncidentFlowsFromProbeToAuditedNotification(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'e2e@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(chave)
	if err != nil {
		t.Fatal(err)
	}

	alvo, canal := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agora := time.Now()
	// O AAD é contrato do dado gravado (notify.configAAD); o teste o repete
	// porque a cifra é interna ao pacote.
	sealed, err := box.Seal([]byte("http://127.0.0.1:1/api/webhooks/1/tok"), crypto.AAD("notif_channels", "config_enc", canal))
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, Name: "interno", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora.Add(-time.Minute), CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: canal, UserID: dono, Kind: "discord", ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	metricas := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleProber, DatabaseURL: testdb.DSN(t), MetricsAddr: metricas,
		MasterKey: chave, ShutdownGrace: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	prazo := time.After(30 * time.Second)
	passo := time.NewTicker(100 * time.Millisecond)
	defer passo.Stop()
	vistos := 0
	for {
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes do fim do fluxo: %v", err)
		case <-prazo:
			t.Fatalf("o fluxo não terminou em 30s: %d checks, estado %+v", vistos, estadoDoFluxo(t, pool, dono, canal))
		case <-passo.C:
		}
		if n := quantosChecks(t, pool, dono, alvo); n > vistos {
			vistos = n
			devolverAFila(t, pool, dono, alvo)
		}
		e := estadoDoFluxo(t, pool, dono, canal)
		if e.incidentes == 1 && e.notificacao == "failed" && e.auditorias == 1 && strings.Contains(lerMetricas(t, metricas), "\nincidents_open 1\n") {
			break
		}
	}

	e := estadoDoFluxo(t, pool, dono, canal)
	if e.erro != "invalid_channel" {
		t.Errorf("last_error = %q, queria invalid_channel", e.erro)
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

func portaLivre(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func lerMetricas(t *testing.T, addr string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		return "" // o servidor pode ainda não ter subido
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func quantosChecks(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) int {
	t.Helper()
	var n int
	escopoDoDono(t, pool, dono, `SELECT count(*) FROM checks WHERE target_id = $1`, []any{alvo}, &n)
	return n
}

// devolverAFila põe o alvo vencido de novo, para a próxima sonda sair no
// próximo tick em vez de daqui a um intervalo.
func devolverAFila(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE targets SET next_run_at = now() - interval '1 minute' WHERE id = $1`, alvo); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

type fluxo struct {
	incidentes  int
	notificacao string
	erro        string
	auditorias  int
}

func estadoDoFluxo(t *testing.T, pool *pgxpool.Pool, dono, canal uuid.UUID) fluxo {
	t.Helper()
	var f fluxo
	escopoDoDono(t, pool, dono, `SELECT
		(SELECT count(*) FROM incidents WHERE resolved_at IS NULL),
		coalesce((SELECT status FROM notifications WHERE channel_id = $1 AND event = 'opened'), ''),
		coalesce((SELECT last_error FROM notifications WHERE channel_id = $1 AND event = 'opened'), ''),
		(SELECT count(*) FROM audit_log WHERE action = 'notification.failed' AND entity_id = $1)`,
		[]any{canal}, &f.incidentes, &f.notificacao, &f.erro, &f.auditorias)
	return f
}

func escopoDoDono(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, sql string, args []any, dst ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, sql, args...).Scan(dst...); err != nil {
		t.Fatal(err)
	}
}

// O papel api com as dependências dos Planos 3 e 4 ligadas: sem Channels,
// Incidents ou StatusPages no Deps, as rotas cairiam em panic (nil) na primeira chamada
// autenticada; sem Ready, /readyz responderia 503.
func TestAPIRoleWiresIncidentsChannelsAndReadyz(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'api@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	addr := portaLivre(t)
	cfg := &config.Config{
		Role: config.RoleAPI, DatabaseURL: testdb.DSN(t), HTTPAddr: addr,
		PublicBaseURL: "http://localhost", CORSOrigin: "http://localhost:5173",
		MasterKey: chave, JWTSecret: chave, IPHashKey: chave, ShutdownGrace: time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	tok, err := auth.NewAccessToken(chave, dono, uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pedir := func(caminho string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+caminho, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	prazo := time.Now().Add(10 * time.Second)
	for pedir("/readyz") != http.StatusOK {
		if time.Now().After(prazo) {
			t.Fatal("/readyz não ficou 200 em 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, caminho := range []string{"/api/v1/channels", "/api/v1/incidents", "/api/v1/status-pages"} {
		if s := pedir(caminho); s != http.StatusOK {
			t.Errorf("GET %s deu %d, queria 200", caminho, s)
		}
	}
	cancel()
	if err := <-fim; err != nil {
		t.Errorf("run encerrou com erro: %v", err)
	}
}
