package prober_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O par de fanout_projeto_test.go para o heartbeat, que tem bancada própria
// neste pacote externo: o aviso do incidente vai só aos canais do projeto do
// heartbeat.

type avisoHB struct {
	canal  uuid.UUID
	evento string
}

func (a avisoHB) String() string { return a.evento + "@" + a.canal.String() }

// notificacoesHB lê todas as notificações por fora da RLS, em ordem de id.
func notificacoesHB(t *testing.T) []avisoHB {
	t.Helper()
	rows, err := testdb.Admin(t).Query(context.Background(), `SELECT channel_id, event FROM notifications ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []avisoHB
	for rows.Next() {
		var a avisoHB
		if err := rows.Scan(&a.canal, &a.evento); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// canalNoProjetoHB grava, por fora da RLS, um canal habilitado e verificado
// do criador no projeto.
func canalNoProjetoHB(t *testing.T, projeto, criador uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := testdb.Admin(t).Exec(context.Background(), `
		INSERT INTO notif_channels (id, user_id, project_id, kind, config_enc, verified_at, enabled, created_at)
		VALUES ($1, $2, $3, 'discord', '\x01', now(), true, now())`, id, criador, projeto); err != nil {
		t.Fatalf("criar canal no projeto %s: %v", projeto, err)
	}
	return id
}

func TestHeartbeatAtrasadoDeAAvisaSoCanaisDeA(t *testing.T) {
	b := novaBancadaHB(t)
	_, token := b.heartbeat(t, "backup") // no Projeto padrão (A) do dono
	canalA := b.canal(t)
	canalNoProjetoHB(t, testdb.NovoProjeto(t, b.dono, "B"), b.dono)

	b.atrasar(t)
	if ns, quer := notificacoesHB(t), []avisoHB{{canalA, incident.EventOpened}}; !slices.Equal(ns, quer) {
		t.Fatalf("abertura: notificações = %v; queria %v (só o canal do projeto A)", ns, quer)
	}

	b.recuperar(t, token)
	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria o fechamento", n)
	}
	quer := []avisoHB{{canalA, incident.EventOpened}, {canalA, incident.EventResolved}}
	if ns := notificacoesHB(t); !slices.Equal(ns, quer) {
		t.Errorf("fechamento: notificações = %v; queria %v (só o canal do projeto A)", ns, quer)
	}
}
