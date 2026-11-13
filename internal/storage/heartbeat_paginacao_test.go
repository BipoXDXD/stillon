package storage_test

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O log e o histórico de incidentes de um heartbeat são paginados por cursor: o
// id é UUIDv7 e o cursor é o último id da página anterior. Os testes de
// isolamento só leem uma linha, e as rotas só pedem a primeira página, então
// nada exercitava o `<` do cursor, o LIMIT nem a ordem: um `<=` repetiria o
// último item de cada página, e um `>` devolveria a página errada.

// maisNovoPrimeiro ordena os ids do mais novo para o mais velho, que é a ordem
// das duas listagens. O UUIDv7 ordena pelos bytes; o esperado vem daqui, e não
// da ordem de criação.
func maisNovoPrimeiro(ids []uuid.UUID) []uuid.UUID {
	ordenados := slices.Clone(ids)
	slices.SortFunc(ordenados, func(a, b uuid.UUID) int { return bytes.Compare(b[:], a[:]) })
	return ordenados
}

func idsDosEventos(linhas []db.HeartbeatEvent) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(linhas))
	for _, l := range linhas {
		ids = append(ids, l.ID)
	}
	return ids
}

func idsDosIncidentes(linhas []db.ListHeartbeatIncidentsRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(linhas))
	for _, l := range linhas {
		ids = append(ids, l.ID)
	}
	return ids
}

func gravarEventos(t *testing.T, pool *pgxpool.Pool, heartbeat uuid.UUID, quantos int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := make([]uuid.UUID, 0, quantos)
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		for range quantos {
			id := novoID()
			ids = append(ids, id)
			if err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: id, HeartbeatID: heartbeat, At: agora, Kind: "success", Source: "ping",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("gravar eventos: %v", err)
	}
	return ids
}

func eventosDoDono(t *testing.T, pool *pgxpool.Pool, dono, heartbeat uuid.UUID, antes *uuid.UUID, max int32) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var linhas []db.HeartbeatEvent
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		linhas, err = q.ListHeartbeatEvents(ctx, db.ListHeartbeatEventsParams{
			UserID: dono, HeartbeatID: heartbeat, Before: antes, MaxRows: max,
		})
		return err
	})
	if err != nil {
		t.Fatalf("listar eventos: %v", err)
	}
	return idsDosEventos(linhas)
}

func TestListHeartbeatEventsPagesWithoutRepeatingOrSkipping(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hb, outro := novoHeartbeat(t, pool, dono, "a"), novoHeartbeat(t, pool, dono, "b")
	todos := maisNovoPrimeiro(gravarEventos(t, pool, hb, 5))
	gravarEventos(t, pool, outro, 3) // outro heartbeat do mesmo dono: nunca entra

	if got := eventosDoDono(t, pool, dono, hb, nil, 100); !slices.Equal(got, todos) {
		t.Fatalf("sem cursor: %v, queria os 5 do mais novo ao mais velho %v", got, todos)
	}

	// Páginas de 2: 2 + 2 + 1. O cursor é o último id da página anterior.
	var paginas [][]uuid.UUID
	var cursor *uuid.UUID
	for range 4 {
		pagina := eventosDoDono(t, pool, dono, hb, cursor, 2)
		if len(pagina) == 0 {
			break
		}
		paginas = append(paginas, pagina)
		ultimo := pagina[len(pagina)-1]
		cursor = &ultimo
	}
	quer := [][]uuid.UUID{todos[0:2], todos[2:4], todos[4:5]}
	if len(paginas) != len(quer) {
		t.Fatalf("%d páginas, queria %d: %v", len(paginas), len(quer), paginas)
	}
	for i := range quer {
		if !slices.Equal(paginas[i], quer[i]) {
			t.Errorf("página %d = %v, queria %v", i+1, paginas[i], quer[i])
		}
	}
}

// O cursor é estrito: o próprio id do cursor não volta, e o do mais velho não
// deixa nada depois dele.
func TestListHeartbeatEventsCursorIsStrict(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hb := novoHeartbeat(t, pool, dono, "a")
	todos := maisNovoPrimeiro(gravarEventos(t, pool, hb, 3))

	if got := eventosDoDono(t, pool, dono, hb, &todos[0], 100); !slices.Equal(got, todos[1:]) {
		t.Errorf("com o cursor no mais novo: %v, queria os 2 mais velhos %v", got, todos[1:])
	}
	if got := eventosDoDono(t, pool, dono, hb, &todos[2], 100); len(got) != 0 {
		t.Errorf("com o cursor no mais velho: %v, queria nenhum", got)
	}
	if got := eventosDoDono(t, pool, dono, hb, nil, 1); !slices.Equal(got, todos[:1]) {
		t.Errorf("limite 1: %v, queria só o mais novo %v", got, todos[:1])
	}
}

func abrirEResolver(t *testing.T, pool *pgxpool.Pool, heartbeat uuid.UUID, resolver bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		if err := q.OpenHeartbeatIncident(ctx, db.OpenHeartbeatIncidentParams{
			ID: id, HeartbeatID: heartbeat, StartedAt: agora, Cause: "heartbeat_late",
		}); err != nil {
			return err
		}
		if !resolver {
			return nil
		}
		n, err := q.ResolveHeartbeatIncident(ctx, db.ResolveHeartbeatIncidentParams{
			ID: id, ResolvedAt: agora.Add(time.Minute),
		})
		if err == nil && n != 1 {
			t.Errorf("resolver %s afetou %d linhas, queria 1", id, n)
		}
		return err
	})
	if err != nil {
		t.Fatalf("abrir incidente de heartbeat: %v", err)
	}
	return id
}

func incidentesDoDono(t *testing.T, pool *pgxpool.Pool, dono, heartbeat uuid.UUID, abertos *bool, antes *uuid.UUID, max int32) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var linhas []db.ListHeartbeatIncidentsRow
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		linhas, err = q.ListHeartbeatIncidents(ctx, db.ListHeartbeatIncidentsParams{
			UserID: dono, HeartbeatID: heartbeat, Open: abertos, Before: antes, MaxRows: max,
		})
		return err
	})
	if err != nil {
		t.Fatalf("listar incidentes: %v", err)
	}
	return idsDosIncidentes(linhas)
}

// O índice único parcial só admite um incidente aberto por heartbeat, então o
// histórico é: três resolvidos e, por último, um aberto.
func TestListHeartbeatIncidentsOpenFilterAndCursor(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	hb, outro := novoHeartbeat(t, pool, dono, "a"), novoHeartbeat(t, pool, dono, "b")
	resolvidos := []uuid.UUID{
		abrirEResolver(t, pool, hb, true), abrirEResolver(t, pool, hb, true), abrirEResolver(t, pool, hb, true),
	}
	aberto := abrirEResolver(t, pool, hb, false)
	abrirEResolver(t, pool, outro, false) // outro heartbeat do mesmo dono: nunca entra
	historico := maisNovoPrimeiro(append(slices.Clone(resolvidos), aberto))
	soResolvidos := maisNovoPrimeiro(resolvidos)
	sim, nao := true, false

	if got := incidentesDoDono(t, pool, dono, hb, nil, nil, 100); !slices.Equal(got, historico) {
		t.Errorf("open nulo: %v, queria os 4 do mais novo ao mais velho %v", got, historico)
	}
	if got := incidentesDoDono(t, pool, dono, hb, &sim, nil, 100); !slices.Equal(got, []uuid.UUID{aberto}) {
		t.Errorf("open true: %v, queria só o aberto %s", got, aberto)
	}
	if got := incidentesDoDono(t, pool, dono, hb, &nao, nil, 100); !slices.Equal(got, soResolvidos) {
		t.Errorf("open false: %v, queria os 3 resolvidos %v", got, soResolvidos)
	}

	// O cursor pagina dentro do filtro: 2 + 1 entre os resolvidos.
	primeira := incidentesDoDono(t, pool, dono, hb, &nao, nil, 2)
	if !slices.Equal(primeira, soResolvidos[:2]) {
		t.Fatalf("primeira página dos resolvidos: %v, queria %v", primeira, soResolvidos[:2])
	}
	if got := incidentesDoDono(t, pool, dono, hb, &nao, &primeira[1], 2); !slices.Equal(got, soResolvidos[2:]) {
		t.Errorf("segunda página dos resolvidos: %v, queria %v", got, soResolvidos[2:])
	}

	// Estrito: o id do cursor não volta, e depois do mais velho não há nada.
	if got := incidentesDoDono(t, pool, dono, hb, nil, &historico[0], 100); !slices.Equal(got, historico[1:]) {
		t.Errorf("cursor no mais novo: %v, queria os 3 mais velhos %v", got, historico[1:])
	}
	if got := incidentesDoDono(t, pool, dono, hb, nil, &historico[3], 100); len(got) != 0 {
		t.Errorf("cursor no mais velho: %v, queria nenhum", got)
	}
}
