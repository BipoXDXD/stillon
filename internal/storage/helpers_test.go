package storage_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

var agora = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// users não tem RLS; a aplicação é dona da tabela.
func novoUsuario(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// novoAlvo grava só as colunas que targets tem desde a 00004, e não pelo
// InsertTarget gerado: os testes de migration o chamam com o banco parado numa
// versão antiga, sem as colunas que vieram depois (a regra de conteúdo, por
// exemplo). O escopo é o mesmo de storage.InUserTx, sob a RLS.
func novoAlvo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, proximo time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		VALUES ($1, $2, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $3, $4)`,
		id, dono, proximo, agora); err != nil {
		t.Fatalf("criar alvo: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func novoCheck(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, em time.Time) {
	t.Helper()
	ctx := context.Background()
	code := int32(200)
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: em,
			Status: "up", LatencyMS: 12, StatusCode: &code,
		})
	})
	if err != nil {
		t.Fatalf("gravar check: %v", err)
	}
}

// contarComEscopo roda um SELECT count(*) SEM WHERE sob o escopo de um
// usuário: é a consulta que "esqueceu o filtro", e só a política a contém.
func contarComEscopo(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, tabela string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	// tabela é sempre um literal do próprio teste.
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// novoCanal cria um canal do dono. tokenHash nil cria um Discord já
// verificado; com hash, um e-mail pendente que vence em expira.
func novoCanal(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, tokenHash []byte, expira time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	p := db.InsertChannelParams{ID: id, UserID: dono, Kind: "discord", ConfigEnc: []byte{1}, CreatedAt: agora}
	if tokenHash == nil {
		p.VerifiedAt = &agora
	} else {
		p.Kind, p.VerifyTokenHash, p.VerifyExpiresAt = "email", tokenHash, &expira
	}
	if err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) }); err != nil {
		t.Fatalf("criar canal: %v", err)
	}
	return id
}

func novoIncidente(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "timeout"})
	})
	if err != nil {
		t.Fatalf("abrir incidente: %v", err)
	}
	return id
}

func novaNotificacao(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: "opened", Now: agora,
		})
	})
	if err != nil {
		t.Fatalf("enfileirar notificação: %v", err)
	}
	return id
}

func novoID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// arquivoDaMigration acha migrations/*_<sufixo>.sql pelo nome, e não pelo
// número: a numeração é a do próximo número livre na hora do merge, e um
// número fixo no teste quebraria a cada renumeração.
func arquivoDaMigration(t *testing.T, sufixo string) string {
	t.Helper()
	achados, err := filepath.Glob("migrations/*_" + sufixo + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(achados) != 1 {
		t.Fatalf("migration *_%s.sql: achei %v, queria exatamente uma", sufixo, achados)
	}
	return filepath.Base(achados[0])
}

// versaoDaMigration devolve o número da migration *_<sufixo>.sql.
func versaoDaMigration(t *testing.T, sufixo string) int64 {
	t.Helper()
	nome := arquivoDaMigration(t, sufixo)
	numero, _, _ := strings.Cut(nome, "_")
	versao, err := strconv.ParseInt(numero, 10, 64)
	if err != nil {
		t.Fatalf("migration %s sem número no começo: %v", nome, err)
	}
	return versao
}
