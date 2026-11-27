package storage_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

var agora = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// kindsConhecidos e eventosConhecidos são o que o binário desta versão
// reivindica da outbox (notify.kindsConhecidos e notify.eventosConhecidos).
var (
	kindsConhecidos   = []string{"discord", "email"}
	eventosConhecidos = []string{"opened", "resolved", "reminder"}
)

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

// projetoPadraoDe devolve o Projeto padrão do dono no banco de pool, criando-o
// com o vínculo de owner se faltar. Grava pelo papel da aplicação no escopo de
// usuário, como a conta faz no cadastro (projects e project_members são da
// pessoa): serve também ao banco de testdb.EmptyDatabase já migrado, que
// testdb.ProjetoPadrao (superusuário no banco compartilhado) não alcança.
func projetoPadraoDe(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var projeto uuid.UUID
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		projeto, err = storage.CriarProjetoPadraoNaTx(ctx, q, dono, agora)
		return err
	})
	if err != nil {
		t.Fatalf("Projeto padrão de %s: %v", dono, err)
	}
	return projeto
}

// noProjetoPadraoDe roda fn no escopo do Projeto padrão do dono
// (storage.InProjectPgxTx, que confere a pertença antes de ligar
// app.project_id), com o pgx.Tx cru para o SQL sem query gerada. As sementes
// passam por aqui: com as políticas por pertença ao projeto, o escopo de
// usuário não vê nem grava recurso nenhum.
func noProjetoPadraoDe(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, fn func(tx pgx.Tx, projeto uuid.UUID) error) error {
	t.Helper()
	projeto := projetoPadraoDe(t, pool, dono)
	return storage.InProjectPgxTx(context.Background(), pool, dono, projeto,
		func(tx pgx.Tx, _ tenancy.ProjectScope) error { return fn(tx, projeto) })
}

// emProjetoTx é o storage.InUserTx dos testes de recurso: roda fn com as
// queries geradas no escopo do Projeto padrão do dono.
func emProjetoTx(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, fn func(*db.Queries) error) error {
	t.Helper()
	return noProjetoPadraoDe(t, pool, dono, func(tx pgx.Tx, _ uuid.UUID) error { return fn(db.New(tx)) })
}

// projetoDoDono, no lugar do nome do GUC em emEscopo e comEscopo, liga o
// escopo do Projeto padrão do usuário cujo id vem no valor (ver
// escopoDoProjetoDe). Os testes que comparam escopos sobre a mesma tabela
// (dono, sondador, ping, página pública) trocam app.user_id por ele sem mudar
// de forma.
const projetoDoDono = "projeto padrão do dono"

// escopoDoProjetoDe liga, na transação tx já aberta, o escopo do Projeto
// padrão do dono: storage.InProjectPgxTx num savepoint de tx confere a
// pertença e liga app.user_id e app.project_id, que valem em tx depois que o
// savepoint fecha. Para a transação que o teste segura aberta e para o banco
// de testdb.EmptyDatabase (testdb.EscopoDoProjetoPadrao só alcança o
// compartilhado).
func escopoDoProjetoDe(t *testing.T, pool *pgxpool.Pool, tx pgx.Tx, dono uuid.UUID) uuid.UUID {
	t.Helper()
	projeto := projetoPadraoDe(t, pool, dono)
	if err := storage.InProjectPgxTx(context.Background(), tx, dono, projeto,
		func(pgx.Tx, tenancy.ProjectScope) error { return nil }); err != nil {
		t.Fatalf("ligar o escopo do Projeto padrão de %s: %v", dono, err)
	}
	return projeto
}

// novoAlvo grava um alvo no Projeto padrão do dono, no escopo desse projeto.
// SQL cru, e não o InsertTarget gerado, para gravar só as colunas que todo
// banco migrado até o fim tem.
func novoAlvo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, proximo time.Time) uuid.UUID {
	t.Helper()
	id := novoID()
	err := noProjetoPadraoDe(t, pool, dono, func(tx pgx.Tx, projeto uuid.UUID) error {
		_, err := tx.Exec(context.Background(), `
			INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
			                     expected_status, enabled, next_run_at, created_at)
			VALUES ($1, $2, $3, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $4, $5)`,
			id, dono, projeto, proximo, agora)
		return err
	})
	if err != nil {
		t.Fatalf("criar alvo: %v", err)
	}
	return id
}

// novoAlvoDoBinarioAnterior grava como o binário anterior aos projetos: só as
// colunas que targets tem desde a 00004, sem project_id, no escopo de
// usuário. É para o banco parado numa versão antiga (testes de migration), em
// que as políticas ainda são por dono e as colunas novas não existem.
func novoAlvoDoBinarioAnterior(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, proximo time.Time) uuid.UUID {
	t.Helper()
	id := novoID()
	err := emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `
			INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms,
			                     expected_status, enabled, next_run_at, created_at)
			VALUES ($1, $2, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $3, $4)`,
			id, dono, proximo, agora)
		return err
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
// usuário, para as tabelas da pessoa (sessões, projetos, vínculos): é a
// consulta que "esqueceu o filtro", e só a política a contém.
func contarComEscopo(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, tabela string) int {
	t.Helper()
	var n int
	err := emEscopo(t, pool, "app.user_id", userID.String(), func(tx pgx.Tx) error {
		// tabela é sempre um literal do próprio teste.
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+tabela).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// contarNoProjeto roda um SELECT count(*) SEM WHERE no escopo do Projeto
// padrão do usuário: a consulta que "esqueceu o filtro" numa tabela de
// projeto. Só as políticas do projeto a contêm: a permissiva (<t>_owner até a
// 00044, <t>_do_projeto depois) e a restritiva <t>_so_projeto_do_escopo, nas
// quatro tabelas com project_id; as de posse indireta herdam pelo EXISTS no
// pai. Uma das duas já basta para separar dois projetos, então a sabotagem que
// deixa vermelho troca as duas por true.
func contarNoProjeto(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, tabela string) int {
	t.Helper()
	var n int
	err := noProjetoPadraoDe(t, pool, userID, func(tx pgx.Tx, _ uuid.UUID) error {
		// tabela é sempre um literal do próprio teste.
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+tabela).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// novoCanal cria um canal no Projeto padrão do dono. tokenHash nil cria um
// Discord já verificado; com hash, um e-mail pendente que vence em expira.
func novoCanal(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, tokenHash []byte, expira time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := noProjetoPadraoDe(t, pool, dono, func(tx pgx.Tx, projeto uuid.UUID) error {
		p := db.InsertChannelParams{ID: id, UserID: dono, ProjectID: projeto, Kind: "discord", ConfigEnc: []byte{1}, CreatedAt: agora}
		if tokenHash == nil {
			p.VerifiedAt = &agora
		} else {
			p.Kind, p.VerifyTokenHash, p.VerifyExpiresAt = "email", tokenHash, &expira
		}
		return db.New(tx).InsertChannel(ctx, p)
	})
	if err != nil {
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
