package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Escrita por comando (00034): checks e incidents só ganham linha pelo
// sondador, ninguém os apaga pela RLS (a retenção derruba partição), e o dono
// só altera incidente para reconhecê-lo. target_tags é só do dono.

// garantirParticaoDeAgora cria a partição mensal de agora, como o tick diário.
func garantirParticaoDeAgora(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatalf("criar a partição de agora: %v", err)
	}
}

func checkDoSondador(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	garantirParticaoDeAgora(t, pool)
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: id, TargetID: alvo, CheckedAt: agora, Status: "up", LatencyMS: 10,
		})
	})
	if err != nil {
		t.Fatalf("sondador gravando check: %v", err)
	}
	return id
}

func incidenteDoSondador(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "timeout"})
	})
	if err != nil {
		t.Fatalf("sondador abrindo incidente: %v", err)
	}
	return id
}

// apagarEmEscopo devolve quantas linhas o DELETE cru levou; tabela é sempre
// um literal do próprio teste.
func apagarEmEscopo(t *testing.T, pool *pgxpool.Pool, guc, valor, tabela string, id uuid.UUID) int64 {
	t.Helper()
	var n int64
	err := emEscopo(t, pool, guc, valor, func(tx pgx.Tx) error {
		tag, err := tx.Exec(context.Background(), `DELETE FROM `+tabela+` WHERE id = $1`, id)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		t.Fatalf("DELETE em %s: %v", tabela, err)
	}
	return n
}

// particaoDoCheck é o nome da partição que guarda o check, para o DELETE que
// nomeia a partição direto.
func particaoDoCheck(t *testing.T, pool *pgxpool.Pool, check uuid.UUID) string {
	t.Helper()
	var nome string
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT tableoid::regclass::text FROM checks WHERE id = $1`, check).Scan(&nome)
	})
	if err != nil {
		t.Fatalf("achar a partição do check: %v", err)
	}
	return nome
}

func TestSondadorNaoApagaCheckNemIncidente(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	check := checkDoSondador(t, pool, alvo)
	incidente := incidenteDoSondador(t, pool, alvo)
	hb := novoHeartbeat(t, pool, dono, "hb")
	incidenteHB := novoIncidenteDeHeartbeat(t, pool, hb)

	particao := particaoDoCheck(t, pool, check)
	if n := apagarEmEscopo(t, pool, "app.scope", "prober", "checks", check); n != 0 {
		t.Errorf("sondador apagou %d checks pela mãe", n)
	}
	if n := apagarEmEscopo(t, pool, "app.scope", "prober", particao, check); n != 0 {
		t.Errorf("sondador apagou %d checks pela partição %s", n, particao)
	}
	if n := apagarEmEscopo(t, pool, "app.scope", "prober", "incidents", incidente); n != 0 {
		t.Errorf("sondador apagou %d incidentes de alvo", n)
	}
	if n := apagarEmEscopo(t, pool, "app.scope", "prober", "incidents", incidenteHB); n != 0 {
		t.Errorf("sondador apagou %d incidentes de heartbeat", n)
	}
	if n := contarComEscopo(t, pool, dono, "checks"); n != 1 {
		t.Errorf("o dono vê %d checks depois das tentativas; queria 1", n)
	}
	if n := contarComEscopo(t, pool, dono, "incidents"); n != 2 {
		t.Errorf("o dono vê %d incidentes depois das tentativas; queria 2", n)
	}
}

func TestDonoNaoApagaCheckNemIncidente(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	check := checkDoSondador(t, pool, alvo)
	incidente := incidenteDoSondador(t, pool, alvo)

	if n := apagarEmEscopo(t, pool, "app.user_id", dono.String(), "checks", check); n != 0 {
		t.Errorf("o dono apagou %d checks", n)
	}
	if n := apagarEmEscopo(t, pool, "app.user_id", dono.String(), "incidents", incidente); n != 0 {
		t.Errorf("o dono apagou %d incidentes", n)
	}
}

func TestDonoNaoForjaCheckNemIncidente(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	hb := novoHeartbeat(t, pool, dono, "hb")
	garantirParticaoDeAgora(t, pool)

	casos := map[string]func(pgx.Tx) error{
		"check": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO checks (id, target_id, checked_at, status, latency_ms)
				VALUES ($1, $2, $3, 'up', 1)`, novoID(), alvo, agora)
			return err
		},
		"incidente de alvo": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, cause)
				VALUES ($1, $2, $3, 'timeout')`, novoID(), alvo, agora)
			return err
		},
		"incidente de heartbeat": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO incidents (id, heartbeat_id, started_at, cause)
				VALUES ($1, $2, $3, 'heartbeat_late')`, novoID(), hb, agora)
			return err
		},
	}
	for nome, inserir := range casos {
		t.Run(nome, func(t *testing.T) {
			err := emEscopo(t, pool, "app.user_id", dono.String(), inserir)
			if codigoPG(err) != "42501" {
				t.Errorf("o dono inserindo %s deu %v, queria 42501", nome, err)
			}
		})
	}
}

func TestTagsDoAlvoSaoSoDoDono(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: []string{"prod"}, UserID: dono, TargetID: alvo})
	})
	if err != nil {
		t.Fatal(err)
	}

	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO target_tags (target_id, tag) VALUES ($1, 'sondador')`, alvo)
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("sondador gravando tag deu %v, queria 42501", err)
	}
	var apagadas int64
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM target_tags WHERE target_id = $1`, alvo)
		apagadas = tag.RowsAffected()
		return err
	})
	if err != nil || apagadas != 0 {
		t.Errorf("sondador apagou %d tags (erro %v)", apagadas, err)
	}
	if n := contarEmEscopo(t, pool, "app.scope", "prober", "target_tags"); n != 0 {
		t.Errorf("sondador viu %d tags", n)
	}
	if n := contarComEscopo(t, pool, dono, "target_tags"); n != 1 {
		t.Errorf("o dono vê %d tags; queria 1", n)
	}
}

// Os caminhos legítimos continuam abertos: o sondador grava check, abre,
// avisa e fecha incidente; o dono reconhece incidente de alvo e de heartbeat.
func TestCaminhosLegitimosDeEscritaContinuam(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	checkDoSondador(t, pool, alvo)
	incidente := incidenteDoSondador(t, pool, alvo)
	hb := novoHeartbeat(t, pool, dono, "hb")
	incidenteHB := novoIncidenteDeHeartbeat(t, pool, hb)

	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		if _, err := q.AckIncident(ctx, db.AckIncidentParams{Now: agora, UserID: dono, ID: incidente}); err != nil {
			return err
		}
		_, err := q.AckHeartbeatIncident(ctx, db.AckHeartbeatIncidentParams{
			Now: agora, UserID: dono, HeartbeatID: hb, ID: incidenteHB,
		})
		return err
	})
	if err != nil {
		t.Fatalf("o dono reconhecendo incidentes: %v", err)
	}

	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		if n, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{Now: agora, ID: incidente}); err != nil || n != 1 {
			t.Errorf("marcar aviso de abertura: %d linhas, erro %v", n, err)
		}
		n, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ResolvedAt: agora, ID: incidente})
		if err == nil && n != 1 {
			t.Errorf("fechar incidente alterou %d linhas", n)
		}
		return err
	})
	if err != nil {
		t.Fatalf("sondador avisando e fechando incidente: %v", err)
	}
}

// Uma partição que já existia antes da migration das políticas por comando
// sai dela com as mesmas políticas da mãe: a checks_via_target antiga cai e
// entram a de leitura e a de gravação do sondador.
func TestEscritaPorComandoAlcancaParticaoAnterior(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, "rls_escrita_por_comando")
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao-1); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatal(err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("up da %d: %v", versao, err)
	}
	exigirParticaoProtegida(t, pool, particaoDoMesDeAgora)
}
