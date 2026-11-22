package notify

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Caracterização: o lembrete segue os pares (incidente, canal) que já
// receberam a abertura, e a abertura já saiu só nos canais do projeto do
// recurso. Um canal do dono em outro projeto não ganha lembrete.
func TestLembreteDeIncidenteDeANaoVaiParaCanalDeB(t *testing.T) {
	b := novaBancadaLembretes(t) // alvo e incidente no Projeto padrão (A) do dono
	canalA := b.canal(t, KindDiscord, nil)
	projetoB := testdb.NovoProjeto(t, b.dono, "B")
	b.canalSemAbertura(t, KindDiscord, func(p *db.InsertChannelParams) { p.ProjectID = projetoB })

	b.avancarETicar(t, 30*time.Minute, 1)

	rows, err := testdb.Admin(t).Query(context.Background(),
		`SELECT channel_id FROM notifications WHERE event = 'reminder' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	canais, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	if quer := []uuid.UUID{canalA}; !slices.Equal(canais, quer) {
		t.Errorf("lembretes nos canais %v; queria só %v (o canal do projeto A)", canais, quer)
	}
}
