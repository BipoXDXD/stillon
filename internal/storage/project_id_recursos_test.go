package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O INSERT de cada recurso como o binário anterior à 00040 o faz: sem
// project_id, no escopo de usuário. Até a troca das políticas por pertença ao
// projeto, o gatilho da 00040 punha a linha no Projeto padrão; depois dela, o
// escopo de usuário não grava recurso (o binário em sobreposição no release
// já grava por InProjectTx). Não atualize junto com as queries: a imagem
// anterior não muda.
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

// inserirComSQLAntigo grava uma linha com o INSERT antigo no escopo de usuário
// do dono, como o binário anterior (o InUserTx); com o pool do superusuário, a
// linha entra como dado legado, por fora da RLS.
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

// O INSERT do binário anterior (escopo só do usuário, sem project_id) não
// grava mais: sem app.project_id nenhuma política de recurso concede. Com o
// Projeto padrão o gatilho preenche project_id, sem ele a linha ficaria NULL;
// nos dois casos o WITH CHECK de <t>_do_projeto recusa, e nada fica no banco.
func TestOlderBinaryInsertsAreRefusedInUserScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	for _, ins := range insercoesAntigas {
		for _, comPadrao := range []bool{true, false} {
			nome := ins.tabela + "/sem Projeto padrão"
			if comPadrao {
				nome = ins.tabela + "/com Projeto padrão"
			}
			t.Run(nome, func(t *testing.T) {
				dono := novoUsuario(t, pool)
				if comPadrao {
					testdb.ProjetoPadrao(t, dono)
				}
				id := novoID()
				err := emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, ins.sql, ins.args(id, dono)...)
					return err
				})
				if codigoPG(err) != "42501" {
					t.Errorf("INSERT antigo no escopo de usuário: %v; queria 42501", err)
				}
				// tabela é sempre um literal do próprio teste.
				if n := contar(t, testdb.Admin(t), `SELECT count(*) FROM `+ins.tabela+` WHERE id = $1`, id); n != 0 {
					t.Errorf("o INSERT recusado deixou %d linha(s)", n)
				}
			})
		}
	}
}

// No escopo de projeto o gatilho não preenche: código novo que esquece o
// project_id tem de falhar alto, não cair no Projeto padrão. "Falhar alto" é o
// 42501 da RLS: a linha fica com project_id NULL, que não é o projeto do
// escopo, e tanto <t>_do_projeto quanto a restritiva <t>_so_projeto_do_escopo
// a recusam (qual das duas o Postgres nomeia não importa aqui). O escopo é o
// do próprio Projeto padrão, então um gatilho que preenchesse faria o INSERT
// passar.
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
			if codigoPG(err) != "42501" {
				t.Errorf("%s: INSERT sem project_id no escopo de projeto: %v; queria 42501", ins.tabela, err)
			}
		})
	}
}

// O project_id não muda em escopo nenhum: os de serviço (ping, verify,
// sondador) têm UPDATE de linha inteira. Nem NULL → valor passa: o único caso
// era o backfill, que a migration da troca das políticas roda pela última vez
// antes de deixar o guard estrito.
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
		{"dono em targets", projetoDoDono, dono.String(), "targets", alvo},
		{"dono em heartbeats", projetoDoDono, dono.String(), "heartbeats", hb},
		{"dono em notif_channels", projetoDoDono, dono.String(), "notif_channels", canal},
		{"dono em status_pages", projetoDoDono, dono.String(), "status_pages", pagina},
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

	// A linha sem projeto é dado legado, gravada aqui pelo superusuário (que
	// ignora a RLS, mas não o gatilho). Nem o sondador, que tem UPDATE de
	// alvo em todo projeto, nem o superusuário a põem num projeto.
	t.Run("NULL para valor não passa", func(t *testing.T) {
		admin := testdb.Admin(t)
		semProjeto := novoUsuario(t, pool)
		legados := map[string]uuid.UUID{}
		for _, ins := range insercoesAntigas {
			legados[ins.tabela] = inserirComSQLAntigo(t, admin, ins, semProjeto)
		}
		padrao := testdb.ProjetoPadrao(t, semProjeto)

		// O escopo de InProberTx, ligado por SQL cru para o UPDATE sem query gerada.
		comEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) {
			tag, err := tx.Exec(ctx, `UPDATE targets SET project_id = $1 WHERE id = $2`, padrao, legados["targets"])
			if codigoPG(err) != "42501" {
				t.Errorf("o sondador pôs projeto no alvo sem projeto (%d linha(s), erro %v); queria 42501",
					tag.RowsAffected(), err)
			}
		}, false)
		for _, ins := range insercoesAntigas {
			// tabela é sempre um literal do próprio teste.
			tag, err := admin.Exec(ctx, `UPDATE `+ins.tabela+` SET project_id = $1 WHERE id = $2`, padrao, legados[ins.tabela])
			if codigoPG(err) != "42501" {
				t.Errorf("%s: o superusuário pôs projeto na linha sem projeto (%d linha(s), erro %v); queria 42501",
					ins.tabela, tag.RowsAffected(), err)
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
	err := emProjetoTx(t, pool, dono, func(q *db.Queries) error {
		var err error
		row, err = q.GetTarget(ctx, db.GetTargetParams{ProjectID: testdb.ProjetoDoAlvo(t, alvo), ID: alvo})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ProjectID == nil || *row.ProjectID != projeto {
		t.Errorf("GetTarget: ProjectID = %v, queria %s", row.ProjectID, projeto)
	}
}
