package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de TestRLSIsolatesTargetsEvenWithTheWrongFilter: aqui a RLS fica de
// fora (conexão de superusuário) e só o filtro por user_id separa os donos. As
// três queries são as que o PATCH usa; sem este teste, apagar o user_id = $1 de
// qualquer uma deixaria a suíte verde, porque a RLS ainda barraria o intruso.
func TestTargetQueriesFilterByOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	proximo := agora.Add(42 * time.Minute)
	alvoDeA := novoAlvo(t, pool, a, proximo)

	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	desde := agora.Add(-3 * time.Minute)
	if _, err := admin.Exec(ctx, `
		INSERT INTO target_health (target_id, consecutive_failures, consecutive_successes, streak_since, last_checked_at)
		VALUES ($1, 2, 0, $2, $2)`, alvoDeA, desde); err != nil {
		t.Fatal(err)
	}
	q := db.New(admin)

	tentar := func(dono uuid.UUID) error {
		t.Helper()
		_, errGet := q.GetTargetForUpdate(ctx, db.GetTargetForUpdateParams{UserID: dono, ID: alvoDeA})
		if err := q.UpdateTarget(ctx, db.UpdateTargetParams{
			UserID: dono, ID: alvoDeA, Name: "roubado", URL: "https://outro.exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora,
		}); err != nil {
			t.Fatalf("UpdateTarget: %v", err)
		}
		if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{UserID: dono, TargetID: alvoDeA}); err != nil {
			t.Fatalf("ResetTargetHealth: %v", err)
		}
		return errGet
	}
	type estado struct {
		nome           string
		proximo        time.Time
		falhas         int32
		sequenciaDesde *time.Time
	}
	ler := func() estado {
		t.Helper()
		var e estado
		if err := admin.QueryRow(ctx, `
			SELECT t.name, t.next_run_at, h.consecutive_failures, h.streak_since
			  FROM targets t JOIN target_health h ON h.target_id = t.id
			 WHERE t.id = $1`, alvoDeA).Scan(&e.nome, &e.proximo, &e.falhas, &e.sequenciaDesde); err != nil {
			t.Fatal(err)
		}
		return e
	}

	if err := tentar(b); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetTargetForUpdate de B sobre o alvo de A deu %v, queria ErrNoRows", err)
	}
	if e := ler(); e.nome != "alvo" || !e.proximo.Equal(proximo) {
		t.Errorf("UpdateTarget de B mexeu no alvo de A: nome %q, next_run_at %s", e.nome, e.proximo.UTC().Format(time.RFC3339))
	}
	if e := ler(); e.falhas != 2 || e.sequenciaDesde == nil {
		t.Errorf("ResetTargetHealth de B zerou a histerese de A: falhas %d, streak_since %v", e.falhas, e.sequenciaDesde)
	}

	// Controle: com o dono certo, as três alcançam a linha. Sem isto, uma
	// conexão que não atravessasse a RLS deixaria o teste verde à toa.
	if err := tentar(a); err != nil {
		t.Fatalf("GetTargetForUpdate do dono deu %v", err)
	}
	if e := ler(); e.nome != "roubado" || e.falhas != 0 || e.sequenciaDesde != nil {
		t.Errorf("com o dono, UpdateTarget e ResetTargetHealth não agiram: %+v", e)
	}
}
