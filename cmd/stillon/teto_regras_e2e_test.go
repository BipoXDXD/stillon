package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// CONTENT_RULES_PER_HOST chega ao serviço de alvos do processo real. Com teto
// 2 (o padrão é 5), a 2ª regra no host passa e a 3ª é recusada sem gravar: se
// o run montasse o serviço com o padrão, a 3ª passaria.
func TestAPIRoleAppliesTheConfiguredContentRulesPerHost(t *testing.T) {
	const teto = 2
	if teto >= targets.DefaultContentRulesPerHost {
		t.Fatalf("o teto do teste (%d) tem de ficar abaixo do padrão (%d)", teto, targets.DefaultContentRulesPerHost)
	}
	pool := testdb.New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'teto@exemplo.com', 'x')`, dono); err != nil {
		t.Fatal(err)
	}
	addr := portaLivre(t)
	cfg := configDeTeste(t, config.RoleAPI, testdb.DSN(t))
	cfg.HTTPAddr, cfg.ShutdownGrace = addr, time.Second
	cfg.ContentRulesPerHost = teto
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	_, sessao, err := auth.NewManager(auth.NewPGStore(pool), clock.Real{}, chave).
		Issue(ctx, dono, auth.PasswordStamp("x"), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.NewAccessToken(chave, dono, sessao.FamilyID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	esperarReadyz(t, addr)

	criar := func(n int) (int, string) {
		corpo := fmt.Sprintf(`{"name":"alvo %d","url":"https://example.com/%d","content_rule":{"mode":"contains","value":"ok"}}`, n, n)
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/targets", strings.NewReader(corpo))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "stillon")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(b)
	}

	for i := 1; i <= teto; i++ {
		if s, corpo := criar(i); s != http.StatusCreated {
			t.Fatalf("regra %d de %d no host: %d %s, queria 201", i, teto, s, corpo)
		}
	}
	if s, corpo := criar(teto + 1); s != http.StatusConflict || !strings.Contains(corpo, "content_rule_host_limit_reached") {
		t.Errorf("regra %d com teto %d: %d %s, queria 409 content_rule_host_limit_reached", teto+1, teto, s, corpo)
	}
	// A RLS só mostra os alvos no escopo do dono.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM targets WHERE user_id = $1`, dono).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != teto {
		t.Errorf("%d alvos gravados, queria %d", n, teto)
	}

	cancel()
	if err := <-fim; err != nil {
		t.Errorf("run encerrou com erro: %v", err)
	}
}

func esperarReadyz(t *testing.T, addr string) {
	t.Helper()
	prazo := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(prazo) {
			t.Fatal("/readyz não ficou 200 em 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
