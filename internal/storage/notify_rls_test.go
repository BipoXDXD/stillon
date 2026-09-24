package storage_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// target_health e incidents não têm user_id: a RLS delega ao alvo, como em
// checks. Quem não enxerga o alvo não enxerga nem cria nada dele.
func TestIncidentsAndHealthFollowTheTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	novoIncidente(t, pool, alvoA)
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.EnsureTargetHealth(ctx, alvoA) }); err != nil {
		t.Fatal(err)
	}

	for _, tabela := range []string{"incidents", "target_health"} {
		if n := contarComEscopo(t, pool, b, tabela); n != 0 {
			t.Errorf("B viu %d linhas de %s do alvo de A", n, tabela)
		}
		if n := contarComEscopo(t, pool, a, tabela); n != 1 {
			t.Errorf("A viu %d linhas de %s, queria 1", n, tabela)
		}
	}

	err := storage.InUserTx(ctx, pool, a, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: novoID(), TargetID: alvoB, StartedAt: agora, Cause: "dns"})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("A abrindo incidente no alvo de B deu %v, queria 42501 (RLS)", err)
	}
}

// O detector não abre dois, e o índice parcial é a garantia que não depende
// do código estar certo.
func TestOnlyOneOpenIncidentPerTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alvo := novoAlvo(t, pool, novoUsuario(t, pool), agora)
	primeiro := novoIncidente(t, pool, alvo)

	abrir := func() error {
		return storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.OpenIncident(ctx, db.OpenIncidentParams{ID: novoID(), TargetID: alvo, StartedAt: agora, Cause: "dns"})
		})
	}
	if err := abrir(); codigoPG(err) != "23505" {
		t.Fatalf("segundo incidente aberto no mesmo alvo deu %v, queria 23505", err)
	}

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: primeiro, ResolvedAt: agora.Add(time.Minute)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := abrir(); err != nil {
		t.Errorf("depois de resolver o primeiro, abrir outro deu %v", err)
	}
}

// O dono vê só os seus canais. O sondador lê os de todos para o fan-out e
// não escreve nenhum.
func TestChannelsAreIsolatedAndProberOnlyReads(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	canalA := novoCanal(t, pool, a, nil, time.Time{})
	novoCanal(t, pool, b, nil, time.Time{})

	if n := contarComEscopo(t, pool, a, "notif_channels"); n != 1 {
		t.Errorf("sob o escopo de A, SELECT sem WHERE viu %d canais, queria 1", n)
	}

	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		ids, err := q.ListDeliverableChannels(ctx, a)
		if err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] != canalA {
			t.Errorf("fan-out de A = %v, queria [%s]", ids, canalA)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: novoID(), UserID: a, Kind: "discord", ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if codigoPG(err) != "42501" {
		t.Errorf("o sondador criando canal deu %v, queria 42501", err)
	}
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		n, err := q.DeleteChannel(ctx, db.DeleteChannelParams{UserID: a, ID: canalA})
		if n != 0 {
			t.Errorf("o sondador apagou %d canais", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hashDe(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// O escopo de verificação só existe com o token: enxerga a linha daquele
// hash e mais nada, nem a linha de outro token pedida pelo nome.
func TestVerifyScopeSeesOnlyTheTokenRow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	hA, hB, hVencido := hashDe("token-a"), hashDe("token-b"), hashDe("token-vencido")
	canalA := novoCanal(t, pool, a, hA, agora.Add(24*time.Hour))
	novoCanal(t, pool, b, hB, agora.Add(24*time.Hour))
	novoCanal(t, pool, b, hVencido, agora.Add(-time.Minute))
	alvoA := novoAlvo(t, pool, a, agora)
	// D3: um e-mail já verificado mantém o hash para sempre, então canalA
	// segue alcançável no escopo verify muito depois de confirmado. Se a
	// política de notifications delegasse só "o canal existe", esta
	// notificação vazaria para quem tem o token.
	novaNotificacao(t, pool, novoIncidente(t, pool, alvoA), canalA)

	err := storage.InVerifyTx(ctx, pool, hA, func(q *db.Queries) error {
		row, err := q.FindChannelByVerification(ctx, hA)
		if err != nil || row.ID != canalA {
			t.Errorf("buscar pelo próprio token deu %v, %v", row.ID, err)
		}
		// A linha de B existe e o WHERE casa com ela: só a política a esconde.
		if _, err := q.FindChannelByVerification(ctx, hB); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("com o token de A, buscar o de B deu %v, queria ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.verify_token_hash', encode($1::bytea, 'hex'), true)`, hA); err != nil {
		t.Fatal(err)
	}
	for tabela, quer := range map[string]int{"notif_channels": 1, "targets": 0, "notifications": 0} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tabela).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != quer {
			t.Errorf("no escopo de verificação, %s mostrou %d linhas, queria %d", tabela, n, quer)
		}
	}
}

// Confirmar é idempotente, e token vencido não confirma nada.
func TestConfirmChannelIsIdempotentAndRespectsExpiry(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	h, hVencido := hashDe("token"), hashDe("vencido")
	novoCanal(t, pool, dono, h, agora.Add(24*time.Hour))
	novoCanal(t, pool, dono, hVencido, agora.Add(-time.Minute))

	confirmar := func(hash []byte, em time.Time) int64 {
		t.Helper()
		var n int64
		err := storage.InVerifyTx(ctx, pool, hash, func(q *db.Queries) error {
			var err error
			n, err = q.ConfirmChannel(ctx, db.ConfirmChannelParams{TokenHash: hash, Now: em})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := confirmar(h, agora); n != 1 {
		t.Fatalf("primeira confirmação afetou %d linhas", n)
	}
	// Depois do vencimento, o canal JÁ verificado continua confirmando.
	if n := confirmar(h, agora.Add(48*time.Hour)); n != 1 {
		t.Errorf("segunda confirmação afetou %d linhas, queria 1 (idempotente)", n)
	}
	if n := confirmar(hVencido, agora); n != 0 {
		t.Errorf("token vencido confirmou %d linhas", n)
	}

	var verificadoEm time.Time
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		cs, err := q.ListChannels(ctx, dono)
		for _, c := range cs {
			if string(c.VerifyTokenHash) == string(h) && c.VerifiedAt != nil {
				verificadoEm = *c.VerifiedAt
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verificadoEm.Equal(agora) {
		t.Errorf("verified_at = %v, queria o instante da PRIMEIRA confirmação %v", verificadoEm, agora)
	}
}

func TestInVerifyTxRefusesAMalformedHash(t *testing.T) {
	pool := testdb.New(t)
	err := storage.InVerifyTx(context.Background(), pool, nil, func(*db.Queries) error { return nil })
	if !errors.Is(err, storage.ErrNoToken) {
		t.Errorf("hash vazio deu %v, queria ErrNoToken", err)
	}
}

// A outbox delega ao canal. O sondador reivindica, e a reivindicação é um
// lease: a mesma notificação não volta antes de ele vencer.
func TestNotificationClaimIsALease(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	n := novaNotificacao(t, pool, novoIncidente(t, pool, alvo), novoCanal(t, pool, a, nil, time.Time{}))

	if v := contarComEscopo(t, pool, b, "notifications"); v != 0 {
		t.Errorf("B viu %d notificações de A", v)
	}

	reivindicar := func(em time.Time) []db.ClaimDueNotificationsRow {
		t.Helper()
		var rows []db.ClaimDueNotificationsRow
		err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			var err error
			rows, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
				Now: em, LeaseUntil: em.Add(2 * time.Minute), MaxRows: 8,
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}

	rows := reivindicar(agora)
	if len(rows) != 1 {
		t.Fatalf("reivindicou %d, queria 1", len(rows))
	}
	r := rows[0]
	if r.ID != n || r.Attempts != 1 || r.UserID != a || r.TargetName != "alvo" || r.Cause != "timeout" || r.Kind != "discord" {
		t.Errorf("linha reivindicada = %+v", r)
	}
	if again := reivindicar(agora.Add(time.Minute)); len(again) != 0 {
		t.Errorf("antes do lease vencer, reivindicou de novo %d", len(again))
	}
	if again := reivindicar(agora.Add(3 * time.Minute)); len(again) != 1 || again[0].Attempts != 2 {
		t.Errorf("depois do lease, reivindicou %+v; queria a mesma, na tentativa 2", again)
	}
}

// Um segundo dispatcher pega OUTRAS notificações em vez de esperar pelas que
// o primeiro travou. A trava é segurada de propósito numa transação aberta:
// sem sobreposição de verdade, o teste passaria mesmo sem SKIP LOCKED.
func TestNotificationClaimSkipsLockedRows(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	inc := novoIncidente(t, pool, novoAlvo(t, pool, a, agora))
	travada := novaNotificacao(t, pool, inc, novoCanal(t, pool, a, nil, time.Time{}))
	livre := novaNotificacao(t, pool, inc, novoCanal(t, pool, a, nil, time.Time{}))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM notifications WHERE id = $1 FOR UPDATE`, travada); err != nil {
		t.Fatal(err)
	}

	prazo, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var rows []db.ClaimDueNotificationsRow
	err = storage.InProberTx(prazo, pool, func(q *db.Queries) error {
		var err error
		rows, err = q.ClaimDueNotifications(prazo, db.ClaimDueNotificationsParams{
			Now: agora, LeaseUntil: agora.Add(2 * time.Minute), MaxRows: 8,
		})
		return err
	})
	if err != nil {
		t.Fatalf("reivindicação esperou a trava em vez de pular: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != livre {
		t.Errorf("reivindicou %+v, queria só a livre %s", rows, livre)
	}
}

// Dívida do Plano 2: dois boots concorrentes criando a mesma partição. Com
// a advisory lock, a segunda chamada ESPERA a primeira. O teste segura a
// trava numa sessão, confirma no pg_stat_activity que a chamada está
// parada nela, e só então solta.
func TestPartitionCreationWaitsForTheAdvisoryLock(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	trava, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer trava.Release()
	const chave = `hashtextextended('vigilume.ensure_checks_partition', 0)`
	if _, err := trava.Exec(ctx, `SELECT pg_advisory_lock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}

	fim := make(chan error, 1)
	go func() {
		fim <- storage.EnsureChecksPartitions(ctx, pool, time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC))
	}()

	esperando := false
	for range 50 {
		var n int
		err := trava.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND wait_event = 'advisory'
			  AND query LIKE '%ensure_checks_partition%'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			esperando = true
			break
		}
		select {
		case err := <-fim:
			t.Fatalf("EnsureChecksPartitions terminou (%v) com a trava segurada: a função não usa a advisory lock", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !esperando {
		t.Fatal("a chamada nunca apareceu esperando a advisory lock")
	}

	if _, err := trava.Exec(ctx, `SELECT pg_advisory_unlock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fim:
		if err != nil {
			t.Fatalf("depois de soltar a trava: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a chamada não terminou depois de a trava ser solta")
	}
}
