package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Depois de um rollback, a outbox pode ter aviso de um tipo de canal ou de um
// evento que este binário não sabe entregar. Reivindicá-lo
// gastaria uma tentativa por volta até fechar como failed sem nunca ter
// saído; ele fica pending, intacto, até a versão nova voltar. Os CHECKs são
// afrouxados como superusuário para simular "schema novo, binário velho".

func TestTipoDesconhecidoFicaPendenteSemGastarTentativa(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE notif_channels DROP CONSTRAINT notif_channels_kind_check`); err != nil {
		t.Fatal(err)
	}
	dono := novoUsuario(t, pool)
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))
	desconhecida := notificacaoVencidaEm1159(t, pool, incidente, canalDeTipo(t, pool, dono, "futuro"), "opened")
	discord := novaNotificacao(t, pool, incidente, novoCanal(t, pool, dono, nil, time.Time{}))

	conferirSoAConhecidaReivindicada(t, pool, discord, desconhecida)
}

func TestEventoDesconhecidoFicaPendenteSemGastarTentativa(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE notifications DROP CONSTRAINT notifications_event_check`); err != nil {
		t.Fatal(err)
	}
	dono := novoUsuario(t, pool)
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))
	canal := novoCanal(t, pool, dono, nil, time.Time{})
	desconhecida := notificacaoVencidaEm1159(t, pool, incidente, canal, "futuro")
	conhecida := novaNotificacao(t, pool, incidente, canal)

	conferirSoAConhecidaReivindicada(t, pool, conhecida, desconhecida)
}

// conferirSoAConhecidaReivindicada reivindica com um lugar só. A desconhecida
// vence antes (11:59), então sem o filtro ela ocuparia a vaga.
func conferirSoAConhecidaReivindicada(t *testing.T, pool *pgxpool.Pool, conhecida, desconhecida uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var lote []db.ClaimDueNotificationsRow
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: agora, LeaseUntil: agora.Add(time.Minute), MaxRows: 1,
			Kinds: kindsConhecidos, Events: eventosConhecidos,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lote) != 1 || lote[0].ID != conhecida {
		t.Fatalf("reivindicou %d avisos (%v); queria só o conhecido %v", len(lote), lote, conhecida)
	}

	var status string
	var tentativas int
	var proxima time.Time
	noEscopoDoSondador(t, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status, attempts, next_attempt_at FROM notifications WHERE id = $1`, desconhecida,
		).Scan(&status, &tentativas, &proxima)
	})
	if status != "pending" || tentativas != 0 || !proxima.Equal(time.Date(2026, 9, 22, 11, 59, 0, 0, time.UTC)) {
		t.Errorf("aviso desconhecido ficou status=%s attempts=%d next_attempt_at=%s; queria pending, 0, 2026-09-22 11:59:00Z",
			status, tentativas, proxima.UTC())
	}
}

// canalDeTipo cria um canal verificado do dono com o kind dado, que pode ser
// um que o CHECK atual recusaria.
func canalDeTipo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	p := db.InsertChannelParams{ID: novoID(), UserID: dono, Kind: kind, ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora}
	if err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error { return q.InsertChannel(ctx, p) }); err != nil {
		t.Fatalf("criar canal %s: %v", kind, err)
	}
	return p.ID
}

// notificacaoVencidaEm1159 grava o aviso como a versão futura o gravaria, com
// next_attempt_at literal antes de agora.
func notificacaoVencidaEm1159(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID, evento string) uuid.UUID {
	t.Helper()
	id := novoID()
	noEscopoDoSondador(t, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO notifications (id, incident_id, channel_id, event, next_attempt_at, created_at)
			 VALUES ($1, $2, $3, $4, '2026-09-22 11:59:00+00', '2026-09-22 11:59:00+00')`,
			id, incidente, canal, evento)
		return err
	})
	return id
}
