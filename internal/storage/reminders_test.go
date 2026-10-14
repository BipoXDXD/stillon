package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

func enfileirarLembrete(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID, seq int32) (int64, error) {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		n, err = q.EnqueueReminder(ctx, db.EnqueueReminderParams{
			ID: novoID(), IncidentID: incidente, ChannelID: canal, Seq: seq, Now: agora,
		})
		return err
	})
	return n, err
}

// A migration 00012: a abertura continua uma só por incidente e canal, e os
// lembretes do mesmo par coexistem, um por seq; o mesmo seq colide.
func TestReminderUniquenessBySeq(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	canal := novoCanal(t, pool, dono, nil, time.Time{})
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))

	novaNotificacao(t, pool, incidente, canal)
	novaNotificacao(t, pool, incidente, canal)
	for _, c := range []struct {
		seq  int32
		quer int64
	}{{1, 1}, {2, 1}, {1, 0}} {
		n, err := enfileirarLembrete(t, pool, incidente, canal, c.seq)
		if err != nil || n != c.quer {
			t.Errorf("lembrete seq %d inseriu %d linhas (%v); queria %d", c.seq, n, err, c.quer)
		}
	}

	if n := contarComoSondador(t, pool, `SELECT count(*) FROM notifications WHERE event = 'opened'`); n != 1 {
		t.Errorf("%d aberturas, queria 1", n)
	}
	if n := contarComoSondador(t, pool, `SELECT count(*) FROM notifications WHERE event = 'reminder'`); n != 2 {
		t.Errorf("%d lembretes, queria 2", n)
	}
}

// seq 0 é de abertura e fechamento; um lembrete começa em 1. O CHECK é a
// garantia que não depende do job calcular certo.
func TestReminderSeqStartsAtOne(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	canal := novoCanal(t, pool, dono, nil, time.Time{})
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))

	if _, err := enfileirarLembrete(t, pool, incidente, canal, 0); codigoPG(err) != "23514" {
		t.Errorf("lembrete com seq 0 deu %v, queria 23514 (CHECK)", err)
	}
}

// O lembrete segue a RLS da outbox: o dono o enxerga, outro usuário não.
func TestReminderFollowsTheChannelRLS(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canal := novoCanal(t, pool, a, nil, time.Time{})
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, a, agora))
	if _, err := enfileirarLembrete(t, pool, incidente, canal, 1); err != nil {
		t.Fatal(err)
	}

	if n := contarComEscopo(t, pool, a, "notifications"); n != 1 {
		t.Errorf("A viu %d notificações, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "notifications"); n != 0 {
		t.Errorf("B viu %d notificações de A", n)
	}
}

// O down da 00012 roda com o papel da aplicação, que não ignora a RLS: ele
// precisa apagar os lembretes de todos os canais para o CHECK e o UNIQUE
// antigos voltarem. A abertura fica.
func TestReminderMigrationGoesDownWithRemindersInTheOutbox(t *testing.T) {
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
	canal := novoCanal(t, pool, dono, nil, time.Time{})
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))
	novaNotificacao(t, pool, incidente, canal)
	for seq := int32(1); seq <= 2; seq++ {
		if _, err := enfileirarLembrete(t, pool, incidente, canal, seq); err != nil {
			t.Fatal(err)
		}
	}

	if err := storage.MigrateDownTo(ctx, dsn, 11); err != nil {
		t.Fatalf("down da 00012: %v", err)
	}
	if n := contarComoSondador(t, pool, `SELECT count(*) FROM notifications`); n != 1 {
		t.Errorf("%d notificações depois do down, queria 1 (a abertura)", n)
	}
}
