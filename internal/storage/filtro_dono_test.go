package storage_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Estes testes são o espelho de rls_test.go: lá a política segura uma query
// sem WHERE; aqui o WHERE segura uma conexão sem política. Rodam como
// superusuário, que atravessa a RLS, para que só o filtro por dono na própria
// SQL separe A de B (spec §5.5: as duas barreiras são independentes).

// conexaoSemRLS abre uma conexão que atravessa a RLS. Chame testdb.New antes:
// o Close daqui roda primeiro (Cleanup é LIFO), e o snapshot só volta com o
// banco sem conexões. Se o papel não atravessar a RLS, o teste falha: sem
// isso, os testes deste espelho ficariam verdes só pela política, sem testar
// o WHERE.
func conexaoSemRLS(t *testing.T) *pgx.Conn {
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
	var atravessaRLS bool
	if err := conn.QueryRow(ctx,
		`SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&atravessaRLS); err != nil {
		t.Fatalf("ler os atributos do papel: %v", err)
	}
	if !atravessaRLS {
		t.Fatal("o papel de AdminDSN não tem rolsuper nem rolbypassrls: a RLS mascararia a falta do filtro por dono")
	}
	return conn
}

// queriesSemRLS devolve as queries geradas numa conexão de superusuário.
func queriesSemRLS(t *testing.T) *db.Queries {
	t.Helper()
	return db.New(conexaoSemRLS(t))
}

// exigirNenhumaLinha falha se a leitura de B achou a linha de A, ou se ela
// falhou por outro motivo, que não prova nada sobre o filtro.
func exigirNenhumaLinha(t *testing.T, err error, leitura string) {
	t.Helper()
	switch {
	case err == nil:
		t.Errorf("%s: B achou o recurso de A sem RLS, queria pgx.ErrNoRows", leitura)
	case !errors.Is(err, pgx.ErrNoRows):
		t.Errorf("%s: erro inesperado %v, queria pgx.ErrNoRows", leitura, err)
	}
}

func TestGetTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	_, err := q.GetTarget(ctx, db.GetTargetParams{UserID: b, ID: alvoA})
	exigirNenhumaLinha(t, err, "GetTarget")
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

// atualizacaoDoAlvo copia o alvo com outro nome, para o UPDATE de quem chama.
func atualizacaoDoAlvo(alvo db.Target, dono uuid.UUID, nome string) db.UpdateTargetParams {
	return db.UpdateTargetParams{
		UserID: dono, ID: alvo.ID, Name: nome, URL: alvo.URL, Method: alvo.Method,
		IntervalSec: alvo.IntervalSec, TimeoutMS: alvo.TimeoutMS, ExpectedStatus: alvo.ExpectedStatus,
		HeadersEnc: alvo.HeadersEnc, DegradedThresholdMS: alvo.DegradedThresholdMS,
		Enabled: alvo.Enabled, NextRunAt: alvo.NextRunAt,
	}
}

func TestUpdateTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)
	alvoDeA := func() db.Target {
		t.Helper()
		alvo, err := q.GetTarget(ctx, db.GetTargetParams{UserID: a, ID: alvoA})
		if err != nil {
			t.Fatal(err)
		}
		return alvo
	}
	original := alvoDeA()

	if err := q.UpdateTarget(ctx, atualizacaoDoAlvo(original, b, "de B")); err != nil {
		t.Fatal(err)
	}
	if nome := alvoDeA().Name; nome != original.Name {
		t.Errorf("B renomeou o alvo de A sem RLS para %q, queria %q", nome, original.Name)
	}
	if err := q.UpdateTarget(ctx, atualizacaoDoAlvo(original, a, "novo")); err != nil {
		t.Fatal(err)
	}
	if nome := alvoDeA().Name; nome != "novo" {
		t.Errorf("A renomeou o próprio alvo e ficou %q, queria \"novo\"", nome)
	}
}

func TestResetTargetHealthFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	if _, err := conn.Exec(ctx, `INSERT INTO target_health (target_id, consecutive_failures, streak_since)
		VALUES ($1, 3, $2)`, alvoA, agora); err != nil {
		t.Fatal(err)
	}
	falhas := func() int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT consecutive_failures FROM target_health WHERE target_id = $1`, alvoA).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{TargetID: alvoA, UserID: b}); err != nil {
		t.Fatal(err)
	}
	if n := falhas(); n != 3 {
		t.Errorf("B zerou a histerese do alvo de A sem RLS: %d falhas, queria 3", n)
	}
	if err := q.ResetTargetHealth(ctx, db.ResetTargetHealthParams{TargetID: alvoA, UserID: a}); err != nil {
		t.Fatal(err)
	}
	if n := falhas(); n != 0 {
		t.Errorf("A zerou a própria histerese e ficaram %d falhas, queria 0", n)
	}
}

// comTagDoAlvo dá ao alvo de A a tag "prod", pelo caminho normal (com RLS).
func comTagDoAlvo(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"prod"}, UserID: dono, TargetID: alvo})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// tagsDoAlvo lê as tags do alvo como o dono, sem RLS.
func tagsDoAlvo(t *testing.T, q *db.Queries, dono, alvo uuid.UUID) []string {
	t.Helper()
	tags, err := q.ListTargetTags(context.Background(), db.ListTargetTagsParams{UserID: dono, TargetID: alvo})
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

func TestListTargetTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	comTagDoAlvo(t, pool, a, alvoA)
	q := queriesSemRLS(t)

	if tags := tagsDoAlvo(t, q, b, alvoA); len(tags) != 0 {
		t.Errorf("B leu as tags %v do alvo de A sem RLS", tags)
	}
	if tags := tagsDoAlvo(t, q, a, alvoA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("A leu %v, queria [prod]", tags)
	}
}

func TestDeleteTargetTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	comTagDoAlvo(t, pool, a, alvoA)
	q := queriesSemRLS(t)

	if err := q.DeleteTargetTags(ctx, db.DeleteTargetTagsParams{UserID: b, TargetID: alvoA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoAlvo(t, q, a, alvoA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("depois do DELETE de B sem RLS, o alvo de A ficou com %v, queria [prod]", tags)
	}
	if err := q.DeleteTargetTags(ctx, db.DeleteTargetTagsParams{UserID: a, TargetID: alvoA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoAlvo(t, q, a, alvoA); len(tags) != 0 {
		t.Errorf("A apagou as próprias tags e sobrou %v", tags)
	}
}

func TestInsertTargetTagsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	comTagDoAlvo(t, pool, a, alvoA)
	q := queriesSemRLS(t)

	if err := q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"intrusa"}, UserID: b, TargetID: alvoA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoAlvo(t, q, a, alvoA); !slices.Equal(tags, []string{"prod"}) {
		t.Errorf("B pôs tag no alvo de A sem RLS: %v, queria [prod]", tags)
	}
	if err := q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"api"}, UserID: a, TargetID: alvoA}); err != nil {
		t.Fatal(err)
	}
	if tags := tagsDoAlvo(t, q, a, alvoA); !slices.Equal(tags, []string{"api", "prod"}) {
		t.Errorf("A pôs a tag api e ficou %v, queria [api prod]", tags)
	}
}
