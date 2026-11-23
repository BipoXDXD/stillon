package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/turnstile"
	"github.com/BipoXDXD/stillon/internal/turnstile/turnstiletest"
)

// Foco 3 da revisão: com o teto diário dos e-mails de conta esgotado, o aviso
// de incidente por e-mail sai assim mesmo, e o e-mail de conta não. Processo
// real, papel all, com o Resend e o siteverify falsos em httptest.
func TestAvisoSaiComTetoDeContaEsgotado(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	rf := novoResendFalso(t)
	anterior := novoTurnstile
	novoTurnstile = func(secret crypto.Secret, _ string) *turnstile.Verificador {
		return turnstiletest.Verificador(t, string(secret.Reveal()))
	}
	t.Cleanup(func() { novoTurnstile = anterior })

	const emailDoDono, emailDoCanal = "dono-teto@exemplo.com", "canal-teto@exemplo.com"
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, email_verified_at) VALUES ($1, $2, 'x', now())`, dono, emailDoDono); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(chave)
	if err != nil {
		t.Fatal(err)
	}
	alvo, canal := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	agora := time.Now()
	sealed, err := box.Seal([]byte(emailDoCanal), crypto.AAD("notif_channels", "config_enc", canal))
	if err != nil {
		t.Fatal(err)
	}
	err = testdb.NoProjetoPadrao(t, pool, dono, func(q *db.Queries, projeto uuid.UUID) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: dono, ProjectID: projeto, Name: "cai", URL: "http://127.0.0.1:1/",
			Method: "GET", IntervalSec: 60, TimeoutMS: 1000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: agora.Add(-time.Minute), CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: canal, UserID: dono, ProjectID: projeto, Kind: "email", ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// O teto do dia UTC já esgotado, gravado como superusuário (a RLS só abre
	// a tabela ao escopo do teto).
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })
	if _, err := admin.Exec(ctx, `INSERT INTO account_email_daily (day, sent) VALUES ($1::date, 50)`, agora.UTC().Format(time.DateOnly)); err != nil {
		t.Fatal(err)
	}

	addr := portaLivre(t)
	cfg := configDeTeste(t, config.RoleAll, testdb.DSN(t))
	cfg.HTTPAddr, cfg.ShutdownGrace = addr, 2*time.Second
	cfg.ResendAPIKey, cfg.EmailFrom, cfg.AccountEmailFrom = chaveResendDeTeste, "avisos@exemplo.test", "conta@exemplo.test"
	cfg.TurnstileSiteKey, cfg.TurnstileSecretKey = turnstiletest.SiteKeyPassa, turnstiletest.SecretPassa
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fim := make(chan error, 1)
	go func() { fim <- run(runCtx, cfg) }()

	// 1. O alvo cai, o incidente abre e o aviso chega ao Resend falso.
	prazo := time.After(30 * time.Second)
	passo := time.NewTicker(100 * time.Millisecond)
	defer passo.Stop()
	vistos := 0
	for !chegouPara(rf, emailDoCanal) {
		select {
		case err := <-fim:
			t.Fatalf("run terminou antes do aviso: %v", err)
		case <-prazo:
			t.Fatalf("o aviso não chegou em 30 s: %d checks, estado %+v", vistos, estadoDoFluxo(t, pool, dono, canal))
		case <-passo.C:
		}
		if n := quantosChecks(t, pool, dono, alvo); n > vistos {
			vistos = n
			devolverAFila(t, pool, dono, alvo)
		}
	}
	for _, e := range rf.lista() {
		if slices.Contains(e.To, emailDoCanal) && e.From != cfg.EmailFrom {
			t.Errorf("aviso saiu de %q, queria o remetente dos avisos %q", e.From, cfg.EmailFrom)
		}
	}

	// 2. O forgot de uma conta existente responde 202, mas o e-mail de conta
	// esbarra no teto e não chega ao Resend.
	corpo := `{"email":"` + emailDoDono + `","turnstile_token":"` + turnstiletest.TokenFalso + `"}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/auth/password/forgot", strings.NewReader(corpo))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("forgot = %d, queria 202", resp.StatusCode)
	}

	// O enviador gasta o balde do destinatário antes de olhar o teto: a
	// linha do balde prova que o e-mail já passou pela fila.
	esperarBaldeDoDestino(t, admin)
	time.Sleep(300 * time.Millisecond)
	if chegouPara(rf, emailDoDono) {
		t.Error("o e-mail de conta chegou ao Resend com o teto esgotado")
	}
	var enviados int
	if err := admin.QueryRow(ctx, `SELECT sent FROM account_email_daily WHERE day = $1::date`, agora.UTC().Format(time.DateOnly)).Scan(&enviados); err != nil {
		t.Fatal(err)
	}
	if enviados != 50 {
		t.Errorf("account_email_daily.sent = %d, queria 50 (o aviso não conta no teto)", enviados)
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

func chegouPara(rf *resendFalso, para string) bool {
	for _, e := range rf.lista() {
		if slices.Contains(e.To, para) {
			return true
		}
	}
	return false
}

func esperarBaldeDoDestino(t *testing.T, admin *pgx.Conn) {
	t.Helper()
	prazo := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM ip_rate_limits WHERE bucket = 'conta_destino'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(prazo) {
			t.Fatal("o e-mail de conta não passou pelo balde do destinatário em 10 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
