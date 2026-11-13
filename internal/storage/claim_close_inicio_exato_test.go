package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Saudável de novo é sucesso ou retomada a partir do início do incidente,
// inclusive: o sinal no mesmo microssegundo do início conta (>= started_at), e
// o de um microssegundo antes é anterior à falha e não fecha nada. O prazo fica
// uma hora no futuro em todos os casos, para só a coluna testada decidir.
func TestClaimHeartbeatsToCloseCountsTheSignalAtTheExactIncidentStart(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	conn := conexaoSemRLS(t)

	// Os incidentes começam em agora (novoIncidenteDeHeartbeat).
	comSinal := func(coluna, nome string, instante time.Time) uuid.UUID {
		t.Helper()
		hb := novoHeartbeat(t, pool, dono, coluna+"-"+nome)
		incidente := novoIncidenteDeHeartbeat(t, pool, hb)
		if _, err := conn.Exec(ctx,
			`UPDATE heartbeats SET `+coluna+` = $1 WHERE id = $2`, instante, hb); err != nil {
			t.Fatalf("gravar %s: %v", coluna, err)
		}
		return incidente
	}

	type caso struct {
		nome      string
		incidente uuid.UUID
		quer      bool
	}
	var casos []caso
	for _, coluna := range []string{"last_success_at", "resumed_at"} {
		casos = append(casos,
			caso{coluna + " = started_at-1µs", comSinal(coluna, "antes", agora.Add(-time.Microsecond)), false},
			caso{coluna + " = started_at", comSinal(coluna, "exato", agora), true},
			caso{coluna + " = started_at+1µs", comSinal(coluna, "depois", agora.Add(time.Microsecond)), true},
		)
	}

	reivindicados := map[uuid.UUID]bool{}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		l, err := q.ClaimHeartbeatsToClose(ctx, db.ClaimHeartbeatsToCloseParams{Now: agora, MaxRows: 10})
		for _, h := range l {
			reivindicados[h.IncidentID] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range casos {
		if reivindicados[c.incidente] != c.quer {
			t.Errorf("%s: reivindicado para fechar = %v, queria %v", c.nome, reivindicados[c.incidente], c.quer)
		}
	}
	if len(reivindicados) != 4 {
		t.Errorf("reivindicou %d incidentes, queria 4", len(reivindicados))
	}

	// Reivindicar não fecha: os seis incidentes seguem abertos no banco.
	var abertos int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM incidents x JOIN heartbeats h ON h.id = x.heartbeat_id
		  WHERE h.user_id = $1 AND x.resolved_at IS NULL`, dono).Scan(&abertos); err != nil {
		t.Fatal(err)
	}
	if abertos != len(casos) {
		t.Errorf("incidentes abertos = %d, queria %d", abertos, len(casos))
	}
}
