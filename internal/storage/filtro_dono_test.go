package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Estes testes são o espelho de rls_test.go: lá a política segura uma query
// sem WHERE; aqui o WHERE segura uma conexão sem política. Rodam como
// superusuário, que atravessa a RLS, para que só o filtro por dono na própria
// SQL separe A de B (spec §5.5: as duas barreiras são independentes).

// queriesSemRLS devolve as queries numa conexão de superusuário. Chame
// testdb.New antes: o Close daqui roda primeiro (Cleanup é LIFO), e o
// snapshot só volta com o banco sem conexões.
func queriesSemRLS(t *testing.T) *db.Queries {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatalf("conectar como superusuário: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Errorf("fechar a conexão de superusuário: %v", err)
		}
	})
	return db.New(conn)
}

func TestGetTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	if _, err := q.GetTarget(ctx, db.GetTargetParams{UserID: b, ID: alvoA}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B leu o alvo de A sem RLS: err = %v, queria pgx.ErrNoRows", err)
	}
	alvo, err := q.GetTarget(ctx, db.GetTargetParams{UserID: a, ID: alvoA})
	if err != nil {
		t.Fatalf("A lendo o próprio alvo: %v", err)
	}
	if alvo.ID != alvoA || alvo.UserID != a {
		t.Errorf("A leu id %s do dono %s, queria %s de %s", alvo.ID, alvo.UserID, alvoA, a)
	}
}

func TestListTargetsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	deB, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: b, MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deB) != 0 {
		t.Errorf("B listou %d alvos sem RLS, queria 0 (o alvo de A vazou)", len(deB))
	}
	deA, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: a, MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deA) != 1 || deA[0].ID != alvoA {
		t.Errorf("A listou %v, queria só o alvo %s", deA, alvoA)
	}
}

func TestDeleteTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	apagadas, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b, ID: alvoA})
	if err != nil {
		t.Fatal(err)
	}
	if apagadas != 0 {
		t.Errorf("B apagou %d linhas sem RLS, queria 0", apagadas)
	}
	if _, err := q.GetTarget(ctx, db.GetTargetParams{UserID: a, ID: alvoA}); err != nil {
		t.Errorf("o alvo de A sumiu depois do DELETE de B: %v", err)
	}
	apagadas, err = q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: a, ID: alvoA})
	if err != nil {
		t.Fatal(err)
	}
	if apagadas != 1 {
		t.Errorf("A apagou %d linhas do próprio alvo, queria 1", apagadas)
	}
}

func TestListChecksFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvoA, agora)
	q := queriesSemRLS(t)
	janela := func(dono uuid.UUID) db.ListChecksParams {
		return db.ListChecksParams{UserID: dono, TargetID: alvoA, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour)}
	}

	deB, err := q.ListChecks(ctx, janela(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(deB) != 0 {
		t.Errorf("B leu %d checks do alvo de A sem RLS, queria 0", len(deB))
	}
	deA, err := q.ListChecks(ctx, janela(a))
	if err != nil {
		t.Fatal(err)
	}
	if len(deA) != 1 || !deA[0].CheckedAt.Equal(agora) || deA[0].Status != "up" {
		t.Errorf("A leu %v, queria um check up em %s", deA, agora)
	}
}
