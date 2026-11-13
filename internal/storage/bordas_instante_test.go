package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As bordas de instante exato: cada predicado de tempo é testado um
// microssegundo antes, no instante e um microssegundo depois do relógio
// injetado (timestamptz guarda microssegundos). Um `<=` trocado por `<`, ou
// `>` por `>=`, só aparece no ponto exato.

// Vencido inclui o instante: o alvo com next_run_at == now é sondado neste
// tique, e não no próximo.
func TestClaimDueTargetsClaimsTheTargetDueAtTheExactInstant(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	antes := novoAlvo(t, pool, dono, agora.Add(-time.Microsecond))
	exato := novoAlvo(t, pool, dono, agora)
	depois := novoAlvo(t, pool, dono, agora.Add(time.Microsecond))

	var contados int64
	reivindicados := map[uuid.UUID]bool{}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		if contados, err = q.CountDueTargets(ctx, agora); err != nil {
			return err
		}
		alvos, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		for _, a := range alvos {
			reivindicados[a.ID] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if contados != 2 {
		t.Errorf("CountDueTargets contou %d alvos vencidos, queria 2 (now-1µs e now)", contados)
	}
	for _, c := range []struct {
		nome string
		id   uuid.UUID
		quer bool
	}{
		{"next_run_at = now-1µs", antes, true},
		{"next_run_at = now", exato, true},
		{"next_run_at = now+1µs", depois, false},
	} {
		if reivindicados[c.id] != c.quer {
			t.Errorf("%s: reivindicado = %v, queria %v", c.nome, reivindicados[c.id], c.quer)
		}
	}
}

// sessaoQueVenceEm grava a linha viva de uma família nova do dono, vencendo em
// expira.
func sessaoQueVenceEm(t *testing.T, conn *pgx.Conn, dono uuid.UUID, expira time.Time) (familia uuid.UUID) {
	t.Helper()
	sessao := novoID()
	familia = novoID()
	if _, err := conn.Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		sessao, dono, familia, hashDe(sessao.String()), expira); err != nil {
		t.Fatalf("criar sessão: %v", err)
	}
	return familia
}

// Viva é expires_at > now: a sessão que vence no instante já está morta, em
// todas as queries que leem ou revogam a linha viva.
func TestSessionsExpiringAtTheExactInstantAreDead(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	conn := conexaoSemRLS(t)
	q := db.New(conn)

	// Cada query devolve se enxergou a família como viva.
	consultas := []struct {
		nome    string
		enxerga func(t *testing.T, dono, familia uuid.UUID) bool
	}{
		{"FamilyIsLive", func(t *testing.T, dono, familia uuid.UUID) bool {
			ok, err := q.FamilyIsLive(ctx, db.FamilyIsLiveParams{UserID: dono, FamilyID: familia, Now: agora})
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}},
		{"ListLiveFamilies", func(t *testing.T, dono, _ uuid.UUID) bool {
			l, err := q.ListLiveFamilies(ctx, db.ListLiveFamiliesParams{UserID: dono, Now: agora})
			if err != nil {
				t.Fatal(err)
			}
			return len(l) == 1
		}},
		{"RevokeAllFamilies", func(t *testing.T, dono, _ uuid.UUID) bool {
			n, err := q.RevokeAllFamilies(ctx, db.RevokeAllFamiliesParams{UserID: dono, Now: agora})
			if err != nil {
				t.Fatal(err)
			}
			return n == 1
		}},
		{"RevokeOtherFamilies", func(t *testing.T, dono, _ uuid.UUID) bool {
			n, err := q.RevokeOtherFamilies(ctx, db.RevokeOtherFamiliesParams{UserID: dono, KeepFamilyID: novoID(), Now: agora})
			if err != nil {
				t.Fatal(err)
			}
			return n == 1
		}},
		{"RevokeFamilyOfUser", func(t *testing.T, dono, familia uuid.UUID) bool {
			n, err := q.RevokeFamilyOfUser(ctx, db.RevokeFamilyOfUserParams{UserID: dono, FamilyID: familia, Now: agora})
			if err != nil {
				t.Fatal(err)
			}
			return n == 1
		}},
	}
	bordas := []struct {
		nome   string
		expira time.Time
		viva   bool
	}{
		{"expires_at = now-1µs", agora.Add(-time.Microsecond), false},
		{"expires_at = now", agora, false},
		{"expires_at = now+1µs", agora.Add(time.Microsecond), true},
	}

	for _, c := range consultas {
		t.Run(c.nome, func(t *testing.T) {
			for _, b := range bordas {
				dono := novoUsuario(t, pool)
				familia := sessaoQueVenceEm(t, conn, dono, b.expira)
				if got := c.enxerga(t, dono, familia); got != b.viva {
					t.Errorf("%s: enxergou viva = %v, queria %v", b.nome, got, b.viva)
				}
			}
		})
	}
}

// Fechar exige prazo no futuro (deadline_at > now): no instante do prazo o
// heartbeat já está atrasado, e o incidente fica aberto mesmo com o sucesso.
func TestClaimHeartbeatsToCloseKeepsOpenAtTheExactDeadline(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)

	// Um heartbeat de período com incidente aberto em agora e sucesso no
	// mesmo instante: só o prazo decide.
	comPrazo := func(nome string, prazo time.Time) uuid.UUID {
		t.Helper()
		hb := novoHeartbeat(t, pool, dono, nome)
		incidente := novoIncidenteDeHeartbeat(t, pool, hb)
		if _, err := conn.Exec(ctx,
			`UPDATE heartbeats SET deadline_at = $1, last_success_at = $2 WHERE id = $3`,
			prazo, agora, hb); err != nil {
			t.Fatalf("ajustar prazo: %v", err)
		}
		return incidente
	}
	antes := comPrazo("antes", agora.Add(-time.Microsecond))
	exato := comPrazo("exato", agora)
	depois := comPrazo("depois", agora.Add(time.Microsecond))

	fechaveis := map[uuid.UUID]bool{}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		l, err := q.ClaimHeartbeatsToClose(ctx, db.ClaimHeartbeatsToCloseParams{Now: agora, MaxRows: 10})
		for _, h := range l {
			fechaveis[h.IncidentID] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		nome      string
		incidente uuid.UUID
		quer      bool
	}{
		{"deadline_at = now-1µs", antes, false},
		{"deadline_at = now", exato, false},
		{"deadline_at = now+1µs", depois, true},
	} {
		if fechaveis[c.incidente] != c.quer {
			t.Errorf("%s: reivindicado para fechar = %v, queria %v", c.nome, fechaveis[c.incidente], c.quer)
		}
	}
}
