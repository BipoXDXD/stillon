package targets_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopoNoProjeto é o escopo de userID no projeto, lido de storage.InProjectTx
// (com o papel gravado no banco).
func escopoNoProjeto(t *testing.T, a ambiente, userID, projeto uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	var sc tenancy.ProjectScope
	err := storage.InProjectTx(context.Background(), a.pool, userID, projeto, func(_ *db.Queries, lido tenancy.ProjectScope) error {
		sc = lido
		return nil
	})
	if err != nil {
		t.Fatalf("escopo de %s no projeto %s: %v", userID, projeto, err)
	}
	return sc
}

// O teto é do titular, somando os projetos dele: o member de A, que nem
// enxerga B, não cria alvo em A quando B já tem o teto inteiro.
func TestMemberQuotaCountsTitularProjects(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	u := a.usuario(t)
	pa := testdb.ProjetoPadrao(t, u)
	pb := testdb.NovoProjeto(t, u, "B")
	if _, err := a.admin.Exec(ctx, `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms, expected_status, next_run_at)
		SELECT gen_random_uuid(), $1, $2, 'alvo ' || i, 'https://exemplo.com/' || i, 'GET', 60, 10000, 200, now()
		  FROM generate_series(1, $3::int) i`, u, pb, targets.MaxTargetsPerUser); err != nil {
		t.Fatal(err)
	}
	m := a.usuario(t)
	testdb.Membro(t, pa, m, "member")

	if _, err := a.svc.Create(ctx, escopoNoProjeto(t, a, m, pa), entrada()); !errors.Is(err, targets.ErrLimitReached) {
		t.Fatalf("o member de A criou alvo com o titular no teto em B: %v; queria ErrLimitReached", err)
	}
	var emA int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM targets WHERE project_id = $1`, pa).Scan(&emA); err != nil {
		t.Fatal(err)
	}
	if emA != 0 {
		t.Errorf("a recusa deixou %d alvo(s) em A", emA)
	}
}

// discordAceita é o teste de webhook que sempre passa.
type discordAceita struct{}

func (discordAceita) SendTest(context.Context, crypto.Secret) error { return nil }

// esperaPelaTrava espera, com prazo generoso, que algum backend fique
// bloqueado pela transação segurador (pg_blocking_pids). Se o Create terminar
// antes, ele não esperou a trava: falha e devolve false.
func esperaPelaTrava(t *testing.T, a ambiente, segurador int32, fim <-chan error) bool {
	t.Helper()
	prazo := time.Now().Add(15 * time.Second)
	for time.Now().Before(prazo) {
		select {
		case err := <-fim:
			t.Errorf("Create terminou (erro %v) sem esperar a trava do binário anterior: as chaves não são as mesmas", err)
			return false
		default:
		}
		var esperando bool
		if err := a.admin.QueryRow(context.Background(), `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			                WHERE wait_event_type = 'Lock' AND $1::int = ANY (pg_blocking_pids(pid)))`,
			segurador).Scan(&esperando); err != nil {
			t.Fatal(err)
		}
		if esperando {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("nenhum backend esperou a trava do binário anterior no prazo")
	return false
}

// A trava do titular usa a mesma chave do binário anterior, que travava pelo
// usuário (o titular, único membro): durante a release os dois binários
// contam em fila. A transação admin segura o SQL literal do binário anterior;
// o Create no escopo do titular fica bloqueado por ela (pg_blocking_pids) e
// passa depois do commit.
func TestTitularLockSharesKeyWithPreviousBinary(t *testing.T) {
	const webhook = "https://discord.com/api/webhooks/123456789/token-do-webhook"
	box := caixa(t)
	criarCanal := func(ctx context.Context, a ambiente, sc tenancy.ProjectScope) error {
		_, err := notify.NewChannelService(a.pool, box, a.clk, discordAceita{}, nil, "https://exemplo.com").
			Create(ctx, sc, notify.ChannelInput{Kind: notify.KindDiscord, URL: webhook})
		return err
	}
	casos := []struct {
		nome  string
		trava string
		criar func(ctx context.Context, a ambiente, sc tenancy.ProjectScope) error
	}{
		{"alvo", `SELECT pg_advisory_xact_lock(hashtextextended('stillon.targets:' || $1::uuid::text, 0))`,
			func(ctx context.Context, a ambiente, sc tenancy.ProjectScope) error {
				_, err := a.svc.Create(ctx, sc, entrada())
				return err
			}},
		{"heartbeat", `SELECT pg_advisory_xact_lock(hashtextextended('stillon.heartbeats:' || $1::uuid::text, 0))`,
			func(ctx context.Context, a ambiente, sc tenancy.ProjectScope) error {
				hora, err := heartbeat.NewPeriod(time.Hour)
				if err != nil {
					return err
				}
				_, _, err = heartbeat.NewService(a.pool, a.clk).Create(ctx, sc,
					heartbeat.Input{Name: "h", Schedule: hora, Grace: 5 * time.Minute, Enabled: true})
				return err
			}},
		// O binário anterior trava a linha de users com FOR UPDATE. Aqui ela vai
		// com FOR NO KEY UPDATE, que conflita com o FOR UPDATE de LockUser mas
		// não com o KEY SHARE da FK no INSERT do canal: com FOR UPDATE, o INSERT
		// (depois da contagem) também esperaria, e o teste passaria sem a trava.
		{"canal_linha_de_users", `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`,
			criarCanal},
		{"canal_chave_nova", `SELECT pg_advisory_xact_lock(hashtextextended('stillon.channels:' || $1::uuid::text, 0))`,
			criarCanal},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			fundo := context.Background()
			u := a.usuario(t)
			sc := targets.EscopoDe(t, a.pool, u)

			tx, err := a.admin.Begin(fundo)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(fundo)
			var segurador int32
			if err := tx.QueryRow(fundo, `SELECT pg_backend_pid()`).Scan(&segurador); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(fundo, c.trava, u); err != nil {
				t.Fatalf("segurar a trava do binário anterior: %v", err)
			}

			fim := make(chan error, 1)
			go func() { fim <- c.criar(fundo, a, sc) }()
			if !esperaPelaTrava(t, a, segurador, fim) {
				return
			}
			if err := tx.Commit(fundo); err != nil {
				t.Fatal(err)
			}
			if err := <-fim; err != nil {
				t.Errorf("Create depois do commit da trava: %v", err)
			}
		})
	}
}
