package storage_test

import (
	"context"
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

func novoAlvo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, proximo time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, Name: "alvo", URL: "https://exemplo.com",
			Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200,
			Enabled: true, NextRunAt: proximo, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatalf("criar alvo: %v", err)
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
