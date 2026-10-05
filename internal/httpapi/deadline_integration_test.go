package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercita Routes, NewServer, autenticação, serviço, transação e PostgreSQL
// reais. Dez consultas presas no advisory lock lotam o pool; duas esperam
// conexão. O prazo precisa interromper ambos sem confirmar novos alvos.
func TestDeadlineCancelaSQLEEsperaDoPool(t *testing.T) {
	a := newAPI(t)
	token := accessTokenValido(t, a)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stillon.targets:' || $1::uuid::text, 0))`, a.userID); err != nil {
		t.Fatal(err)
	}
	// Quando os alvos cancelarem, o pool poderá repor conexões antes do
	// prazo das requisições verify iniciadas depois. A trava da tabela
	// mantém essas consultas presas até o seu próprio cancelamento.
	if _, err := holder.Exec(ctx, `LOCK TABLE notif_channels IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	srv := httpapi.NewServer(&config.Config{HTTPAddr: "127.0.0.1:0"}, a.srv.Config.Handler)
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	type resultado struct {
		status  int
		body    []byte
		trace   string
		err     error
		elapsed time.Duration
		html    bool
		path    string
	}
	const apiTotal = 12
	const total = apiTotal + 3
	results := make(chan resultado, total)
	inicio := time.Now()
	for i := range apiTotal {
		go func() {
			r, err := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/api/v1/targets", bytes.NewBufferString(fmt.Sprintf(`{"name":"deadline-%d","url":"https://example.com","enabled":true}`, i)))
			if err != nil {
				results <- resultado{err: err}
				return
			}
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Requested-With", "stillon")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://app.exemplo.com")
			resp, err := client.Do(r)
			out := resultado{err: err, elapsed: time.Since(inicio)}
			if resp != nil {
				out.status, out.trace = resp.StatusCode, resp.Header.Get(middleware.TraceHeader)
				out.body, out.err = io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.Header.Get("Access-Control-Allow-Origin") != "https://app.exemplo.com" || resp.Header.Get("Access-Control-Allow-Credentials") != "true" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
					out.err = fmt.Errorf("headers da API ausentes: %v", resp.Header)
				}
			}
			results <- out
		}()
	}
	blocked := func() int {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var n int
		if err := admin.QueryRow(c, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%stillon.targets:%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for limite := time.Now().Add(5 * time.Second); blocked() != 10; {
		if time.Now().After(limite) {
			t.Fatalf("não reproduziu saturação: SQL=%d acquired=%d", blocked(), a.pool.Stat().AcquiredConns())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("pool saturado: SQL bloqueadas=%d acquired=%d", blocked(), a.pool.Stat().AcquiredConns())
	// As duas rotas de verificação e a página pública também precisam de
	// prazo real enquanto aguardam uma conexão, preservando resposta HTML.
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/verify/" + strings.Repeat("A", 43)},
		{http.MethodPost, "/verify/" + strings.Repeat("A", 43)},
		{http.MethodGet, "/p/deadline-ausente"},
	} {
		go func() {
			r, err := http.NewRequest(route.method, "http://"+ln.Addr().String()+route.path, nil)
			if err != nil {
				results <- resultado{err: err, html: true}
				return
			}
			resp, err := client.Do(r)
			out := resultado{err: err, elapsed: time.Since(inicio), html: true, path: route.path}
			if resp != nil {
				out.status, out.trace = resp.StatusCode, resp.Header.Get(middleware.TraceHeader)
				out.body, out.err = io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
					out.err = fmt.Errorf("Content-Type=%s", resp.Header.Get("Content-Type"))
				}
			}
			results <- out
		}()
	}
	// O watchdog é apenas do teste: no código original libera a trava para
	// encerrar os handlers e produzir RED sem deixar goroutines penduradas.
	watchdog := time.NewTimer(time.Until(inicio.Add(18 * time.Second)))
	defer watchdog.Stop()
	for range total {
		var out resultado
		select {
		case out = <-results:
		case <-watchdog.C:
			t.Error("requisições não terminaram dentro do prazo de produção")
			if err := holder.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			out = <-results
		}
		if out.html {
			if out.err != nil || out.status != http.StatusServiceUnavailable || out.trace == "" || !strings.Contains(string(out.body), "Tente de novo em instantes.") {
				t.Errorf("HTML: status=%d erro=%v body=%s trace=%q", out.status, out.err, out.body, out.trace)
			}
		} else {
			var envelope struct {
				Error   struct{ Code, Message string }
				TraceID string `json:"trace_id"`
			}
			if err := json.Unmarshal(out.body, &envelope); err != nil {
				t.Errorf("envelope inválido: %s (%v)", out.body, err)
			}
			if out.err != nil || out.status != http.StatusServiceUnavailable || envelope.Error.Code != "unavailable" || envelope.Error.Message != "Serviço indisponível." || envelope.TraceID == "" || envelope.TraceID != out.trace {
				t.Errorf("resposta: status=%d erro=%v body=%s trace=%q", out.status, out.err, out.body, out.trace)
			}
		}
		minimo, maximo := 14*time.Second, 18*time.Second
		if strings.HasPrefix(out.path, "/p/") {
			minimo, maximo = 4*time.Second, 8*time.Second
		}
		if out.elapsed < minimo || out.elapsed > maximo {
			t.Errorf("prazo de produção inesperado em %q: %v", out.path, out.elapsed)
		}
		if out.html {
			t.Logf("HTML %s: status=%d elapsed=%v", out.path, out.status, out.elapsed)
		}
	}
	for limite := time.Now().Add(3 * time.Second); blocked() != 0 || a.pool.Stat().AcquiredConns() != 0; {
		if time.Now().After(limite) {
			t.Fatalf("recursos retidos: SQL=%d acquired=%d", blocked(), a.pool.Stat().AcquiredConns())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := holder.Rollback(ctx); err != nil { // RED pode ter liberado antes.
		if !t.Failed() {
			t.Fatal(err)
		}
	}
	var inserted int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM targets WHERE user_id=$1`, a.userID).Scan(&inserted); err != nil {
		t.Fatal(err)
	}
	if inserted != 0 {
		t.Errorf("alvos confirmados após cancelamento: %d", inserted)
	}
	var abandoned int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename='stillon_app' AND state LIKE 'idle in transaction%'`).Scan(&abandoned); err != nil {
		t.Fatal(err)
	}
	if abandoned != 0 {
		t.Errorf("transações abandonadas: %d", abandoned)
	}
	pingCtx, cancelPing := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPing()
	if err := a.pool.Ping(pingCtx); err != nil {
		t.Fatalf("pool não recuperou conexão saudável: %v", err)
	}
	t.Logf("após prazo: SQL=%d acquired=%d targets=%d elapsed=%v", blocked(), a.pool.Stat().AcquiredConns(), inserted, time.Since(inicio))
}

func TestVerificacaoDeadlineRespondeHTML503Preserva404(t *testing.T) {
	a := newAPI(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, token := range []string{strings.Repeat("A", 43), "invalido"} {
			t.Run(method+"/"+token, func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, "/verify/"+token, nil).WithContext(ctx)
				a.srv.Config.Handler.ServeHTTP(w, r)
				want := http.StatusServiceUnavailable
				if token == "invalido" {
					want = http.StatusNotFound
				}
				if w.Code != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get(middleware.TraceHeader) == "" {
					t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
				}
				if token != "invalido" && !strings.Contains(w.Body.String(), "Tente de novo em instantes.") {
					t.Fatalf("HTML de erro mudou: %s", w.Body.String())
				}
			})
		}
	}
}

func TestReadyzMantemPrazoMenorDeDoisSegundos(t *testing.T) {
	var prazo time.Duration
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Ready = func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("readyz sem prazo")
				return nil
			}
			prazo = time.Until(deadline)
			return nil
		}
	})
	w := httptest.NewRecorder()
	a.srv.Config.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 200 || prazo < 1800*time.Millisecond || prazo > 2*time.Second {
		t.Fatalf("status=%d prazo=%v", w.Code, prazo)
	}
}
