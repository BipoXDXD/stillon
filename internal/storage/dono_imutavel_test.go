package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O dono de alvo, heartbeat e canal não muda por UPDATE (migration 00031).
// Os escopos de serviço (sondador, ping, verificação) só filtram a linha que
// enxergam; o WITH CHECK de ping e verify só exige o mesmo hash de token, e o
// do sondador nada. Sem o gatilho, qualquer um deles entrega a linha a outro
// usuário. Sabotagem: sem a 00031, as quatro trocas afetam 1 linha.
func TestUpdateNaoTrocaODono(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono, outro := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	hb := novoHeartbeat(t, pool, dono, "hb-dono")
	canal := novoCanal(t, pool, dono, hashDe("verifica-dono"), time.Now().Add(time.Hour))

	casos := []struct {
		nome, guc, valor, sql string
		id                    uuid.UUID
	}{
		{"sondador em targets", "app.scope", "prober",
			`UPDATE targets SET user_id = $1 WHERE id = $2`, alvo},
		{"sondador em heartbeats", "app.scope", "prober",
			`UPDATE heartbeats SET user_id = $1 WHERE id = $2`, hb},
		{"ping em heartbeats", "app.ping_token_hash", hashHex("hb-dono"),
			`UPDATE heartbeats SET user_id = $1 WHERE id = $2`, hb},
		{"verificação em notif_channels", "app.verify_token_hash", hashHex("verifica-dono"),
			`UPDATE notif_channels SET user_id = $1 WHERE id = $2`, canal},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			comEscopo(t, pool, c.guc, c.valor, func(tx pgx.Tx) {
				tag, err := tx.Exec(ctx, c.sql, outro, c.id)
				if codigoPG(err) != "42501" {
					t.Errorf("trocar o dono deu %d linha(s) e erro %v; queria erro 42501", tag.RowsAffected(), err)
				}
			}, false)
		})
	}
}

// As escritas que o código faz nesses escopos seguem passando com o gatilho,
// inclusive quando o UPDATE repete o mesmo user_id.
func TestUpdateSemTrocarODonoSeguePassando(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	hb := novoHeartbeat(t, pool, dono, "hb-dono")
	canal := novoCanal(t, pool, dono, hashDe("verifica-dono"), time.Now().Add(time.Hour))

	casos := []struct {
		nome, guc, valor, sql string
		args                  []any
	}{
		{"sondador grava next_run_at", "app.scope", "prober",
			`UPDATE targets SET next_run_at = now() WHERE id = $1`, []any{alvo}},
		{"ping grava deadline_at e last_ping_at", "app.ping_token_hash", hashHex("hb-dono"),
			`UPDATE heartbeats SET deadline_at = now(), last_ping_at = now() WHERE id = $1`, []any{hb}},
		{"verificação grava verified_at", "app.verify_token_hash", hashHex("verifica-dono"),
			`UPDATE notif_channels SET verified_at = now() WHERE id = $1`, []any{canal}},
		{"sondador repete o mesmo dono", "app.scope", "prober",
			`UPDATE targets SET user_id = $1 WHERE id = $2`, []any{dono, alvo}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			comEscopo(t, pool, c.guc, c.valor, func(tx pgx.Tx) {
				tag, err := tx.Exec(context.Background(), c.sql, c.args...)
				if err != nil || tag.RowsAffected() != 1 {
					t.Errorf("escrita legítima deu %d linha(s) e erro %v; queria 1 linha", tag.RowsAffected(), err)
				}
			}, true)
		})
	}
}
