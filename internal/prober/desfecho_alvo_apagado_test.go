package prober

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// O 42501 ou o 23503 chega à gravação depois que o dono apagou o alvo, sem a
// trava de conferirSonda ter visto (a corrida que errAlvoSumido não cobre).
// registrarDesfecho relê targets, confirma que o alvo sumiu, esquece a série
// de latência e não põe nada no log como erro. Um código fora dos dois segue
// sendo erro, mesmo com o alvo apagado.
func TestDesfechoComAlvoApagadoNoMeioDoCiclo(t *testing.T) {
	casos := []struct {
		codigo    string
		esquecido bool
	}{
		{"42501", true},
		{"23503", true},
		{"23505", false},
	}
	for _, c := range casos {
		t.Run(c.codigo, func(t *testing.T) {
			b := novaBancada(t, nil, MaxInFlight)
			alvo := b.alvo(t, "https://exemplo.com", nil)
			b.m.observe(alvo, Result{Status: StatusUp, StatusCode: 200})

			ctx := context.Background()
			err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
				_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: alvo})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			log := capturarLog(t)

			b.s.registrarDesfecho(ctx, alvo, &pgconn.PgError{Code: c.codigo}, incident.ActionNone)

			vistos := b.m.alvosVistos()
			if esquecido := !slices.Contains(vistos, alvo); esquecido != c.esquecido {
				t.Errorf("série esquecida = %v; queria %v (alvos vistos: %v)", esquecido, c.esquecido, vistos)
			}
			erroNoLog := strings.Contains(log.String(), "level=ERROR")
			if erroNoLog == c.esquecido {
				t.Errorf("erro no log = %v; queria %v. Log:\n%s", erroNoLog, !c.esquecido, log)
			}
		})
	}
}
