package storage_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// novoHeartbeat cria um heartbeat de período cujo token é o próprio nome.
func novoHeartbeat(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: dono, Name: nome, PingTokenHash: hashDe(nome), PeriodSec: &periodo,
			GraceSec: 300, Enabled: true, CreatedAt: agora, DeadlineAt: agora.Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatalf("criar heartbeat: %v", err)
	}
	return id
}

func novoIncidenteDeHeartbeat(t *testing.T, pool *pgxpool.Pool, heartbeat uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
			ID: id, HeartbeatID: heartbeat, StartedAt: agora, Cause: "heartbeat_late",
		})
	})
	if err != nil {
		t.Fatalf("abrir incidente de heartbeat: %v", err)
	}
	return id
}

// emEscopo roda fn numa transação com um GUC ligado, com SQL cru: é o que uma
// query que "esqueceu o filtro" faria, e só a política a contém.
func emEscopo(t *testing.T, pool *pgxpool.Pool, guc, valor string, fn func(pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, guc, valor); err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func novoEvento(t *testing.T, pool *pgxpool.Pool, heartbeat uuid.UUID) {
	t.Helper()
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO heartbeat_events (id, heartbeat_id, at, kind) VALUES ($1, $2, $3, 'success')`,
			novoID(), heartbeat, agora)
		return err
	})
	if err != nil {
		t.Fatalf("gravar evento: %v", err)
	}
}

func contarEmEscopo(t *testing.T, pool *pgxpool.Pool, guc, valor, tabela string) int {
	t.Helper()
	var n int
	err := emEscopo(t, pool, guc, valor, func(tx pgx.Tx) error {
		// tabela é sempre um literal do próprio teste.
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+tabela).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hashHex(s string) string { return hex.EncodeToString(hashDe(s)) }

func TestHeartbeatsAreIsolatedByOwner(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hbA, hbB := novoHeartbeat(t, pool, a, "a"), novoHeartbeat(t, pool, b, "b")
	novoEvento(t, pool, hbA)
	novoIncidenteDeHeartbeat(t, pool, hbA)

	for _, tabela := range []string{"heartbeats", "heartbeat_events", "incidents"} {
		if n := contarComEscopo(t, pool, a, tabela); n != 1 {
			t.Errorf("A viu %d linhas de %s, queria 1", n, tabela)
		}
	}
	for _, tabela := range []string{"heartbeat_events", "incidents"} {
		if n := contarComEscopo(t, pool, b, tabela); n != 0 {
			t.Errorf("B viu %d linhas de %s do heartbeat de A", n, tabela)
		}
	}

	err := emEscopo(t, pool, "app.user_id", a.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind) VALUES ($1, $2, $3, 'fail')`,
			novoID(), hbB, agora)
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A gravando evento no heartbeat de B deu %v, queria 42501", err)
	}
	err = storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		return q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
			ID: novoID(), HeartbeatID: hbB, StartedAt: agora, Cause: "heartbeat_late",
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A abrindo incidente no heartbeat de B deu %v, queria 42501", err)
	}
}

// O avaliador lê e atualiza; criar e apagar heartbeat é só do dono.
func TestProberCannotCreateOrDeleteHeartbeats(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")

	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO heartbeats (id, user_id, name, ping_token_hash, period_sec, grace_sec, created_at, deadline_at)
			VALUES ($1, $2, 'x', $3, 60, 60, now(), now())`, novoID(), dono, hashDe("x"))
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("sondador criando heartbeat deu %v, queria 42501", err)
	}
	var apagadas int64
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM heartbeats WHERE id = $1`, hb)
		apagadas = tag.RowsAffected()
		return err
	})
	if err != nil || apagadas != 0 {
		t.Errorf("sondador apagou %d heartbeats (erro %v)", apagadas, err)
	}
}

// O token de A enxerga a linha de A e nada mais, mesmo sem WHERE.
func TestPingScopeSeesOnlyTheTokenRow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hbA, hbB := novoHeartbeat(t, pool, a, "a"), novoHeartbeat(t, pool, b, "b")
	novoEvento(t, pool, hbA)
	novoEvento(t, pool, hbB)

	for tabela, want := range map[string]int{"heartbeats": 1, "heartbeat_events": 1} {
		if n := contarEmEscopo(t, pool, "app.ping_token_hash", hashHex("a"), tabela); n != want {
			t.Errorf("escopo do ping viu %d linhas de %s, queria %d", n, tabela, want)
		}
	}
	// Sem o GUC, ou com ele vazio, não enxerga nada.
	for _, tabela := range []string{"heartbeats", "heartbeat_events"} {
		if n := contarEmEscopo(t, pool, "app.ping_token_hash", "", tabela); n != 0 {
			t.Errorf("GUC vazio viu %d linhas de %s", n, tabela)
		}
	}

	var alteradas int64
	err := emEscopo(t, pool, "app.ping_token_hash", hashHex("a"), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE heartbeats SET name = 'invadido'`)
		alteradas = tag.RowsAffected()
		return err
	})
	if err != nil || alteradas != 1 {
		t.Fatalf("UPDATE sem WHERE alterou %d linhas (erro %v), queria só a do token", alteradas, err)
	}
	var nome string
	err = emEscopo(t, pool, "app.user_id", b.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM heartbeats WHERE id = $1`, hbB).Scan(&nome)
	})
	if err != nil || nome != "b" {
		t.Errorf("o heartbeat de B ficou com nome %q (erro %v)", nome, err)
	}

	err = emEscopo(t, pool, "app.ping_token_hash", hashHex("a"), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind) VALUES ($1, $2, $3, 'fail')`,
			novoID(), hbB, agora)
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("token de A gravando evento em B deu %v, queria 42501", err)
	}
}

// O token de uma tarefa não abre, não fecha e não lê incidente, nem do próprio
// heartbeat: quem cuida disso é o avaliador.
func TestPingScopeCannotTouchIncidents(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	incidente := novoIncidenteDeHeartbeat(t, pool, hb)

	if n := contarEmEscopo(t, pool, "app.ping_token_hash", hashHex("a"), "incidents"); n != 0 {
		t.Errorf("escopo do ping viu %d incidentes", n)
	}
	var alteradas int64
	err := emEscopo(t, pool, "app.ping_token_hash", hashHex("a"), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE incidents SET resolved_at = now() WHERE id = $1`, incidente)
		alteradas = tag.RowsAffected()
		return err
	})
	if err != nil || alteradas != 0 {
		t.Errorf("escopo do ping fechou %d incidentes (erro %v)", alteradas, err)
	}
	err = emEscopo(t, pool, "app.ping_token_hash", hashHex("a"), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO incidents (id, heartbeat_id, started_at, cause) VALUES ($1, $2, now(), 'heartbeat_failed')`,
			novoID(), hb)
		return err
	})
	if codigoPG(err) != "42501" {
		t.Errorf("escopo do ping abrindo incidente deu %v, queria 42501", err)
	}
}

// Os escopos que já existiam continuam sem enxergar heartbeat: nem a página
// pública, nem o link de verificação de canal.
func TestPublicAndVerifyScopesSeeNoHeartbeats(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	novoEvento(t, pool, hb)
	novoIncidenteDeHeartbeat(t, pool, hb)
	novaPagina(t, pool, dono, "publica", true, novoAlvo(t, pool, dono, agora))

	for _, tabela := range []string{"heartbeats", "heartbeat_events", "incidents"} {
		if n := contarPublico(t, pool, "publica", tabela); n != 0 {
			t.Errorf("escopo público viu %d linhas de %s", n, tabela)
		}
		if n := contarEmEscopo(t, pool, "app.verify_token_hash", hashHex("a"), tabela); n != 0 {
			t.Errorf("escopo de verificação viu %d linhas de %s", n, tabela)
		}
	}
}

func TestInPingTxRefusesAMalformedHash(t *testing.T) {
	pool := testdb.New(t)
	err := storage.InPingTx(context.Background(), pool, []byte("curto"), func(*db.Queries) error { return nil })
	if !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("hash curto deu %v, queria ErrNoToken", err)
	}
}

func TestIncidentHasExactlyOneSubject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo, hb := novoAlvo(t, pool, dono, agora), novoHeartbeat(t, pool, dono, "a")

	inserir := func(alvoID, hbID *uuid.UUID) error {
		return emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO incidents (id, target_id, heartbeat_id, started_at, cause)
				VALUES ($1, $2, $3, now(), 'unknown')`, novoID(), alvoID, hbID)
			return err
		})
	}
	if err := inserir(&alvo, &hb); codigoPG(err) != "23514" {
		t.Errorf("incidente com os dois sujeitos deu %v, queria 23514", err)
	}
	// Pelo papel da aplicação, sem sujeito nenhum, a RLS recusa antes do CHECK.
	if err := inserir(nil, nil); codigoPG(err) != "42501" {
		t.Errorf("incidente sem sujeito deu %v, queria 42501 (RLS)", err)
	}
	// O superusuário atravessa a RLS: só assim o CHECK fala por si, e ele é a
	// barreira contra quem não passa pelas políticas.
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	_, err = admin.Exec(ctx, `INSERT INTO incidents (id, started_at, cause) VALUES ($1, now(), 'unknown')`, novoID())
	if codigoPG(err) != "23514" {
		t.Errorf("incidente sem sujeito, sem RLS, deu %v, queria 23514 (CHECK)", err)
	}
}

func TestOnlyOneOpenIncidentPerHeartbeat(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hb := novoHeartbeat(t, pool, novoUsuario(t, pool), "a")
	primeiro := novoIncidenteDeHeartbeat(t, pool, hb)

	abrir := func() error {
		return storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
				ID: novoID(), HeartbeatID: hb, StartedAt: agora, Cause: "heartbeat_failed",
			})
		})
	}
	if err := abrir(); codigoPG(err) != "23505" {
		t.Fatalf("segundo incidente aberto deu %v, queria 23505", err)
	}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: primeiro, ResolvedAt: agora})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := abrir(); err != nil {
		t.Errorf("incidente novo depois de fechar o anterior: %v", err)
	}
}

func TestHeartbeatConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	periodo, curto, longo := int32(3600), int32(59), int32(2678401)
	expr, fuso := "0 3 * * *", "UTC"
	casos := []struct {
		nome           string
		periodo        *int32
		cron, tz       *string
		grace          int32
		hash           []byte
		codigoEsperado string
	}{
		{"período", &periodo, nil, nil, 300, hashDe("1"), ""},
		{"cron", nil, &expr, &fuso, 300, hashDe("2"), ""},
		{"sem agendamento", nil, nil, nil, 300, hashDe("3"), "23514"},
		{"os dois agendamentos", &periodo, &expr, &fuso, 300, hashDe("4"), "23514"},
		{"cron sem fuso", nil, &expr, nil, 300, hashDe("5"), "23514"},
		{"fuso sem cron", &periodo, nil, &fuso, 300, hashDe("6"), "23514"},
		{"período curto", &curto, nil, nil, 300, hashDe("7"), "23514"},
		{"período longo", &longo, nil, nil, 300, hashDe("8"), "23514"},
		{"tolerância curta", &periodo, nil, nil, 29, hashDe("9"), "23514"},
		{"tolerância longa", &periodo, nil, nil, 604801, hashDe("10"), "23514"},
		{"hash curto", &periodo, nil, nil, 300, []byte("curto"), "23514"},
		{"hash repetido", &periodo, nil, nil, 300, hashDe("1"), "23505"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
				return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
					ID: novoID(), UserID: dono, Name: "h", PingTokenHash: c.hash, PeriodSec: c.periodo,
					CronExpr: c.cron, Timezone: c.tz, GraceSec: c.grace, Enabled: true,
					CreatedAt: agora, DeadlineAt: agora,
				})
			})
			if codigoPG(err) != c.codigoEsperado {
				t.Errorf("deu %v, queria código %q", err, c.codigoEsperado)
			}
		})
	}
}

// Apagar o dono leva heartbeats, eventos e incidentes, como no heartbeat.
// O código de saída diz o sinal, e o banco não aceita um que o contradiga.
func TestHeartbeatEventExitCodeMatchesTheKind(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "backup")
	codigo := func(v int16) *int16 { return &v }
	casos := []struct {
		nome, tipo     string
		exitCode       *int16
		codigoEsperado string
	}{
		{"sucesso sem código", "success", nil, ""},
		{"falha sem código", "fail", nil, ""},
		{"início sem código", "start", nil, ""},
		{"sucesso com zero", "success", codigo(0), ""},
		{"falha com 1", "fail", codigo(1), ""},
		{"falha com 255", "fail", codigo(255), ""},
		{"falha com zero", "fail", codigo(0), "23514"},
		{"sucesso com 1", "success", codigo(1), "23514"},
		{"início com zero", "start", codigo(0), "23514"},
		{"falha com 256", "fail", codigo(256), "23514"},
		{"falha negativa", "fail", codigo(-1), "23514"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
				return q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
					ID: novoID(), HeartbeatID: hb, At: agora, Kind: c.tipo, ExitCode: c.exitCode,
				})
			})
			if codigoPG(err) != c.codigoEsperado {
				t.Errorf("deu %v, queria código %q", err, c.codigoEsperado)
			}
		})
	}
}

func TestDeletingTheOwnerCascades(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono, vizinho := novoUsuario(t, pool), novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	novoEvento(t, pool, hb)
	novoIncidenteDeHeartbeat(t, pool, hb)
	novoEvento(t, pool, novoHeartbeat(t, pool, vizinho, "b"))

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, dono); err != nil {
		t.Fatal(err)
	}
	for tabela, want := range map[string]int{"heartbeats": 1, "heartbeat_events": 1, "incidents": 0} {
		if n := contarComoSondador(t, pool, `SELECT count(*) FROM `+tabela); n != want {
			t.Errorf("%s ficou com %d linhas, queria %d (as do vizinho)", tabela, n, want)
		}
	}
}

// Apagar o heartbeat leva eventos e incidentes.
func TestDeletingAHeartbeatCascades(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	novoEvento(t, pool, hb)
	novoIncidenteDeHeartbeat(t, pool, hb)

	err := emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM heartbeats WHERE id = $1`, hb)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tabela := range []string{"heartbeat_events", "incidents"} {
		if n := contarComoSondador(t, pool, `SELECT count(*) FROM `+tabela); n != 0 {
			t.Errorf("sobraram %d linhas de %s", n, tabela)
		}
	}
}

func TestHeartbeatMigrationGoesDownWithHeartbeatIncidents(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	novoEvento(t, pool, hb)
	novoIncidenteDeHeartbeat(t, pool, hb)
	novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))

	if err := storage.MigrateDownTo(ctx, dsn, 14); err != nil {
		t.Fatalf("down da 00016: %v", err)
	}
	if n := contarComoSondador(t, pool, `SELECT count(*) FROM incidents`); n != 1 {
		t.Errorf("%d incidentes depois do down, queria o do alvo", n)
	}
	var nula string
	if err := pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'incidents' AND column_name = 'target_id'`).Scan(&nula); err != nil {
		t.Fatal(err)
	}
	if nula != "NO" {
		t.Errorf("target_id ficou is_nullable=%s depois do down", nula)
	}
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Errorf("subir de novo depois do down: %v", err)
	}
}
