package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Segunda camada do fan-out por projeto (spec §8): o gatilho
// notifications_canal_do_projeto descarta, no banco, a notificação de um
// incidente gravada num canal fora do projeto do recurso. O sondador lê todos
// os canais (notif_channels_prober_select) e a RESTRICTIVE da 00042 vale true
// sem app.project_id: sem a guarda, um enfileiramento errado no código passaria.
// Descarta sem abortar: o enfileiramento roda na transação do check.

// alvoEmProjeto grava, por fora da RLS, um alvo do dono no projeto (nil grava
// NULL; o gatilho da 00040 só o preenche se o dono tiver Projeto padrão).
func alvoEmProjeto(t *testing.T, dono uuid.UUID, projeto *uuid.UUID) uuid.UUID {
	t.Helper()
	id := novoID()
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		VALUES ($1, $2, $3, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $4, $4)`,
		id, dono, projeto, agora); err != nil {
		t.Fatalf("criar alvo: %v", err)
	}
	return id
}

// canalEmProjeto grava, por fora da RLS, um canal Discord habilitado e
// verificado do dono no projeto (nil grava NULL, como em alvoEmProjeto).
func canalEmProjeto(t *testing.T, dono uuid.UUID, projeto *uuid.UUID) uuid.UUID {
	t.Helper()
	id := novoID()
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, enabled, created_at)
		VALUES ($1, $2, $3, 'discord', '\x01', $4, true, $4)`,
		id, dono, projeto, agora); err != nil {
		t.Fatalf("criar canal: %v", err)
	}
	return id
}

// enfileirarComoSondador grava a abertura do incidente no canal pelo caminho de
// produção (EnqueueNotification sob InProberTx). A guarda descarta sem erro.
func enfileirarComoSondador(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: novoID(), IncidentID: incidente, ChannelID: canal, Event: "opened", Now: agora,
		})
	})
	if err != nil {
		t.Fatalf("enfileirar notificação: %v (a guarda descarta, não aborta)", err)
	}
}

// contarNotificacoes conta todas as notificações, de todos os canais, por fora
// da RLS.
func contarNotificacoes(t *testing.T) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(), `SELECT count(*) FROM notifications`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNotificacaoEmCanalDeOutroProjetoEDescartada(t *testing.T) {
	t.Run("abertura de alvo", func(t *testing.T) {
		pool := testdb.New(t)
		u := novoUsuario(t, pool)
		a, b := testdb.ProjetoPadrao(t, u), testdb.NovoProjeto(t, u, "B")
		incidente := novoIncidente(t, pool, alvoEmProjeto(t, u, &a))
		cB := canalEmProjeto(t, u, &b)

		enfileirarComoSondador(t, pool, incidente, cB)

		if n := contarNotificacoes(t); n != 0 {
			t.Errorf("notifications = %d, queria 0 (canal de B, incidente de A)", n)
		}
	})
	t.Run("lembrete de alvo", func(t *testing.T) {
		pool := testdb.New(t)
		u := novoUsuario(t, pool)
		a, b := testdb.ProjetoPadrao(t, u), testdb.NovoProjeto(t, u, "B")
		incidente := novoIncidente(t, pool, alvoEmProjeto(t, u, &a))
		cB := canalEmProjeto(t, u, &b)
		ctx := context.Background()

		var linhas int64
		err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			var err error
			linhas, err = q.EnqueueReminder(ctx, db.EnqueueReminderParams{
				ID: novoID(), IncidentID: incidente, ChannelID: cB, Seq: 1, Now: agora,
			})
			return err
		})
		if err != nil {
			t.Fatalf("enfileirar lembrete: %v (a guarda descarta, não aborta)", err)
		}

		if n := contarNotificacoes(t); n != 0 || linhas != 0 {
			t.Errorf("notifications = %d (linhas afetadas %d), queria 0 (canal de B, incidente de A)", n, linhas)
		}
	})
	t.Run("abertura de heartbeat", func(t *testing.T) {
		pool := testdb.New(t)
		u := novoUsuario(t, pool)
		b := testdb.NovoProjeto(t, u, "B")
		incidente := novoIncidenteDeHeartbeat(t, pool, novoHeartbeat(t, pool, u, "hb")) // no Projeto padrão
		cB := canalEmProjeto(t, u, &b)

		enfileirarComoSondador(t, pool, incidente, cB)

		if n := contarNotificacoes(t); n != 0 {
			t.Errorf("notifications = %d, queria 0 (canal de B, heartbeat do Projeto padrão)", n)
		}
	})
}

func TestNotificacaoNoProjetoCertoPassa(t *testing.T) {
	t.Run("canal do titular", func(t *testing.T) {
		pool := testdb.New(t)
		u := novoUsuario(t, pool)
		a := testdb.ProjetoPadrao(t, u)
		incidente := novoIncidente(t, pool, alvoEmProjeto(t, u, &a))

		enfileirarComoSondador(t, pool, incidente, canalEmProjeto(t, u, &a))

		if n := contarNotificacoes(t); n != 1 {
			t.Errorf("notifications = %d, queria 1 (canal e incidente de A)", n)
		}
	})
	t.Run("canal de um membro", func(t *testing.T) {
		pool := testdb.New(t)
		u, m := novoUsuario(t, pool), novoUsuario(t, pool)
		a := testdb.ProjetoPadrao(t, u)
		testdb.Membro(t, a, m, "manager")
		incidente := novoIncidente(t, pool, alvoEmProjeto(t, u, &a))

		enfileirarComoSondador(t, pool, incidente, canalEmProjeto(t, m, &a))

		if n := contarNotificacoes(t); n != 1 {
			t.Errorf("notifications = %d, queria 1 (o projeto decide, não quem criou o canal)", n)
		}
	})
}

// Fallback legado (sai no PR 7, com o NOT NULL), o mesmo de
// ListDeliverableChannelsForIncident: recurso ou canal sem project_id casa
// por user_id.
func TestNotificacaoLegadaSemProjetoPassaPeloCriador(t *testing.T) {
	casos := []struct {
		nome          string
		alvoEmProjeto bool
		canalEmProj   bool
		mesmoCriador  bool
		quer          int
	}{
		{"alvo e canal sem projeto, mesmo criador", false, false, true, 1},
		{"alvo e canal sem projeto, criador diferente", false, false, false, 0},
		{"alvo sem projeto e canal em projeto, mesmo criador", false, true, true, 1},
		{"alvo sem projeto e canal em projeto, criador diferente", false, true, false, 0},
		{"alvo em projeto e canal sem projeto, mesmo criador", true, false, true, 1},
		{"alvo em projeto e canal sem projeto, criador diferente", true, false, false, 0},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			pool := testdb.New(t)
			// Sem Projeto padrão: o gatilho da 00040 deixa o project_id NULL.
			l, outro := novoUsuario(t, pool), novoUsuario(t, pool)
			donoDoCanal := l
			if !c.mesmoCriador {
				donoDoCanal = outro
			}
			var projetoDoAlvo, projetoDoCanal *uuid.UUID
			if c.alvoEmProjeto {
				p := testdb.NovoProjeto(t, l, "do alvo")
				projetoDoAlvo = &p
			}
			if c.canalEmProj {
				p := testdb.NovoProjeto(t, donoDoCanal, "do canal")
				projetoDoCanal = &p
			}
			incidente := novoIncidente(t, pool, alvoEmProjeto(t, l, projetoDoAlvo))

			enfileirarComoSondador(t, pool, incidente, canalEmProjeto(t, donoDoCanal, projetoDoCanal))

			if n := contarNotificacoes(t); n != c.quer {
				t.Errorf("notifications = %d, queria %d", n, c.quer)
			}
		})
	}
}

// O Down da guarda leva o gatilho e a função, e o Up sobe de novo.
func TestGuardaNotificacaoMigrationDownReverte(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, "guarda_notificacao_projeto")
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	estado := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM pg_trigger
			         WHERE tgrelid = 'notifications'::regclass AND tgname = 'notifications_canal_do_projeto')::text
			       || ' / ' ||
			       (SELECT count(*) FROM pg_proc WHERE proname = 'guardar_notificacao_no_projeto')::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s, queria := estado(), "1 / 1"; s != queria {
		t.Fatalf("depois do up: %q; queria %q", s, queria)
	}
	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if s, queria := estado(), "0 / 0"; s != queria {
		t.Errorf("depois do down: %q; queria %q", s, queria)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
	if s, queria := estado(), "1 / 1"; s != queria {
		t.Errorf("depois de subir de novo: %q; queria %q", s, queria)
	}
}
