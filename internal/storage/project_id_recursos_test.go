package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O INSERT de cada recurso como o binário anterior à 00040 o faz: sem
// project_id. Durante o release os dois binários gravam juntos, e o gatilho
// da 00040 é o que põe a linha no Projeto padrão. Não atualize junto com as
// queries: a imagem anterior não muda.
const (
	antigaInsertTarget = `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
                     expected_status, headers_enc, degraded_threshold_ms, enabled,
                     next_run_at, created_at, tls_warn, content_mode, content_value,
                     content_json_pointer)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`

	antigaInsertHeartbeat = `INSERT INTO heartbeats (id, user_id, name, ping_token_hash, period_sec, cron_expr, timezone,
                        grace_sec, enabled, created_at, deadline_at, description, ping_resumes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

	antigaInsertChannel = `INSERT INTO notif_channels (id, user_id, kind, config_enc, verified_at,
                            verify_token_hash, verify_expires_at, enabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8)`

	antigaInsertStatusPage = `INSERT INTO status_pages (id, user_id, slug, title, published, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`
)

// insercaoAntiga é o INSERT do binário anterior para uma tabela de recurso,
// com argumentos válidos para uma linha nova do dono.
type insercaoAntiga struct {
	tabela string
	sql    string
	args   func(id, dono uuid.UUID) []any
}

var insercoesAntigas = []insercaoAntiga{
	{"targets", antigaInsertTarget, func(id, dono uuid.UUID) []any {
		return []any{id, dono, "alvo", "https://exemplo.com", "GET", int32(60), int32(5000), int32(200),
			[]byte(nil), (*int32)(nil), true, agora, agora, false, (*string)(nil), (*string)(nil), (*string)(nil)}
	}},
	{"heartbeats", antigaInsertHeartbeat, func(id, dono uuid.UUID) []any {
		return []any{id, dono, "hb", hashDe(id.String()), int32(3600), (*string)(nil), (*string)(nil),
			int32(300), true, agora, agora.Add(time.Hour), "", false}
	}},
	{"notif_channels", antigaInsertChannel, func(id, dono uuid.UUID) []any {
		return []any{id, dono, "discord", []byte{1}, agora, []byte(nil), (*time.Time)(nil), agora}
	}},
	{"status_pages", antigaInsertStatusPage, func(id, dono uuid.UUID) []any {
		return []any{id, dono, strings.ReplaceAll(id.String(), "-", ""), "Status", false, agora}
	}},
}

// inserirComSQLAntigo grava uma linha com o INSERT antigo no escopo do dono
// (o InUserTx).
func inserirComSQLAntigo(t *testing.T, pool *pgxpool.Pool, ins insercaoAntiga, dono uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, ins.sql, ins.args(id, dono)...)
		return err
	})
	if err != nil {
		t.Fatalf("%s: INSERT antigo: %v", ins.tabela, err)
	}
	return id
}

// projectIDDe relê o project_id por fora da RLS.
func projectIDDe(t *testing.T, tabela string, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var got *uuid.UUID
	// tabela é sempre um literal do próprio teste.
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id FROM `+tabela+` WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("%s: reler project_id: %v", tabela, err)
	}
	return got
}

// outroProjeto cria, por fora da RLS, um segundo projeto (não padrão) do titular.
func outroProjeto(t *testing.T, titular uuid.UUID) uuid.UUID {
	t.Helper()
	id := novoID()
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO projects (id, name, titular_id, is_default, created_at) VALUES ($1, 'Outro', $2, false, $3)`,
		id, titular, agora); err != nil {
		t.Fatalf("criar outro projeto: %v", err)
	}
	return id
}

// O binário anterior grava sem project_id durante o release; a linha tem de
// cair no Projeto padrão do dono, ou some quando a política por projeto ligar.
func TestOlderBinaryInsertsLandInDefaultProject(t *testing.T) {
	pool := testdb.New(t)
	for _, ins := range insercoesAntigas {
		t.Run(ins.tabela, func(t *testing.T) {
			dono := novoUsuario(t, pool)
			projeto := testdb.ProjetoPadrao(t, dono)
			id := inserirComSQLAntigo(t, pool, ins, dono)
			if got := projectIDDe(t, ins.tabela, id); got == nil || *got != projeto {
				t.Errorf("%s: project_id = %v, queria o padrão %s", ins.tabela, got, projeto)
			}
		})
	}
}

// Sem Projeto padrão (usuário anterior ao backfill), o INSERT antigo passa e
// deixa project_id nulo: o binário anterior nunca quebra.
func TestInsertWithoutDefaultProjectKeepsNullProjectID(t *testing.T) {
	pool := testdb.New(t)
	for _, ins := range insercoesAntigas {
		t.Run(ins.tabela, func(t *testing.T) {
			dono := novoUsuario(t, pool)
			id := inserirComSQLAntigo(t, pool, ins, dono)
			if got := projectIDDe(t, ins.tabela, id); got != nil {
				t.Errorf("%s: project_id = %s, queria NULL", ins.tabela, *got)
			}
		})
	}
}

// No escopo de projeto o gatilho não preenche: código novo que esquece o
// project_id tem de falhar alto, não cair no Projeto padrão. Desde a política
// restritiva por projeto (rls_restrita_por_projeto), "falhar alto" é o 42501
// dela: a linha fica com project_id NULL, que não é o projeto do escopo. O
// escopo é o do próprio Projeto padrão, então um gatilho que preenchesse
// faria o INSERT passar.
func TestFillTriggerSkipsProjectScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	for _, ins := range insercoesAntigas {
		t.Run(ins.tabela, func(t *testing.T) {
			dono := novoUsuario(t, pool)
			projeto := testdb.ProjetoPadrao(t, dono)
			err := emEscopoDeProjeto(t, pool, dono, projeto, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, ins.sql, ins.args(novoID(), dono)...)
				return err
			})
			var pgErr *pgconn.PgError
			politica := ins.tabela + "_so_projeto_do_escopo"
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" || !strings.Contains(pgErr.Message, politica) {
				t.Errorf("%s: INSERT sem project_id no escopo de projeto: %v; queria 42501 da política %s", ins.tabela, err, politica)
			}
		})
	}
}

// Uma vez gravado, o project_id não muda em escopo nenhum: os de serviço
// (ping, verify) têm UPDATE de linha inteira. NULL → valor é o backfill.
func TestProjectIDIsImmutableOnceSet(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, dono)
	outro := outroProjeto(t, dono)
	alvo := novoAlvo(t, pool, dono, agora)
	hb := novoHeartbeat(t, pool, dono, "hb-projeto")
	canal := novoCanal(t, pool, dono, hashDe("verifica-projeto"), time.Now().Add(time.Hour))
	pagina := novaPagina(t, pool, dono, "pagina-projeto", false)

	casos := []struct {
		nome, guc, valor, tabela string
		id                       uuid.UUID
	}{
		{"dono em targets", "app.user_id", dono.String(), "targets", alvo},
		{"dono em heartbeats", "app.user_id", dono.String(), "heartbeats", hb},
		{"dono em notif_channels", "app.user_id", dono.String(), "notif_channels", canal},
		{"dono em status_pages", "app.user_id", dono.String(), "status_pages", pagina},
		{"ping em heartbeats", "app.ping_token_hash", hashHex("hb-projeto"), "heartbeats", hb},
		{"verificação em notif_channels", "app.verify_token_hash", hashHex("verifica-projeto"), "notif_channels", canal},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			comEscopo(t, pool, c.guc, c.valor, func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, `UPDATE `+c.tabela+` SET project_id = $1 WHERE id = $2`, outro, c.id)
				if codigoPG(err) != "42501" {
					t.Errorf("trocar o projeto deu %d linha(s) e erro %v; queria erro 42501", tag.RowsAffected(), err)
				}
			}, false)
		})
	}

	t.Run("NULL para o padrão passa", func(t *testing.T) {
		for _, ins := range insercoesAntigas {
			semProjeto := novoUsuario(t, pool)
			id := inserirComSQLAntigo(t, pool, ins, semProjeto)
			padrao := testdb.ProjetoPadrao(t, semProjeto)
			comEscopo(t, pool, "app.user_id", semProjeto.String(), func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, `UPDATE `+ins.tabela+` SET project_id = $1 WHERE id = $2`, padrao, id)
				if err != nil || tag.RowsAffected() != 1 {
					t.Errorf("%s: backfill deu %d linha(s) e erro %v; queria 1 linha", ins.tabela, tag.RowsAffected(), err)
				}
			}, true)
			if got := projectIDDe(t, ins.tabela, id); got == nil || *got != padrao {
				t.Errorf("%s: project_id = %v depois do backfill, queria %s", ins.tabela, got, padrao)
			}
		}
	})
}

// Apagar o usuário segue levando tudo: o Projeto padrão (titular_id) e os
// recursos, pelos dois caminhos de cascata (user_id e project_id).
func TestDeletingUserTakesDefaultProjectAlong(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	testdb.ProjetoPadrao(t, dono)
	novoAlvo(t, pool, dono, agora)

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, dono); err != nil {
		t.Fatalf("apagar o usuário: %v", err)
	}
	for _, c := range []struct{ tabela, coluna string }{
		{"projects", "titular_id"},
		{"targets", "user_id"},
	} {
		var n int
		// tabela e coluna são literais do próprio teste.
		if err := testdb.Admin(t).QueryRow(ctx,
			`SELECT count(*) FROM `+c.tabela+` WHERE `+c.coluna+` = $1`, dono).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d linha(s) do usuário apagado, queria 0", c.tabela, n)
		}
	}
}

func TestGetTargetReturnsProjectID(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	projeto := testdb.ProjetoPadrao(t, dono)
	alvo := novoAlvo(t, pool, dono, agora)

	var row db.Target
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		row, err = q.GetTarget(ctx, db.GetTargetParams{UserID: dono, ID: alvo})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ProjectID == nil || *row.ProjectID != projeto {
		t.Errorf("GetTarget: ProjectID = %v, queria %s", row.ProjectID, projeto)
	}
}
