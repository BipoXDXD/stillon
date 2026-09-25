package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/notify"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Pausar e retomar canal pela API (Plano 9, §3.3): PATCH /channels/{id} com
// {"enabled": bool}.

func criarCanalDiscord(t *testing.T, a *api, tok string) string {
	t.Helper()
	var c canalJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/channels", tok, map[string]string{"kind": "discord", "url": webhookDeTeste})
	cru := lerCorpo(t, resp, &c)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /channels deu %d: %s", resp.StatusCode, cru)
	}
	return c.ID
}

func pausar(t *testing.T, a *api, tok, id string, corpo any) (int, map[string]any, []byte) {
	t.Helper()
	resp := chamar(t, a, http.MethodPatch, "/api/v1/channels/"+id, tok, corpo)
	var m map[string]any
	cru := lerCorpo(t, resp, nil)
	_ = json.Unmarshal(cru, &m)
	return resp.StatusCode, m, cru
}

// itemDaLista devolve o item cru de GET /channels com o id dado.
func itemDaLista(t *testing.T, a *api, tok, id string) map[string]any {
	t.Helper()
	var l struct {
		Items []map[string]any `json:"items"`
	}
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/channels", tok, nil), &l)
	for _, it := range l.Items {
		if it["id"] == id {
			return it
		}
	}
	t.Fatalf("o canal %s não está na lista", id)
	return nil
}

func chaves(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// PATCH pausa e retoma; a resposta tem o formato de um item de GET /channels,
// e o GET reflete. Mudar para o valor que já está é 200 sem efeito.
func TestPatchChannelPausesAndResumes(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	id := criarCanalDiscord(t, a, tok)

	st, corpo, cru := pausar(t, a, tok, id, map[string]bool{"enabled": false})
	if st != http.StatusOK || corpo["enabled"] != false {
		t.Fatalf("pausar deu %d: %s", st, cru)
	}
	item := itemDaLista(t, a, tok, id)
	if item["enabled"] != false {
		t.Errorf("GET /channels não reflete a pausa: %v", item)
	}
	if !slices.Equal(chaves(corpo), chaves(item)) {
		t.Errorf("a resposta do PATCH tem %v; o item de GET /channels tem %v", chaves(corpo), chaves(item))
	}
	for _, k := range []string{"id", "kind", "hint", "status", "created_at"} {
		if corpo[k] != item[k] {
			t.Errorf("%s: PATCH %v, GET %v", k, corpo[k], item[k])
		}
	}

	if st, corpo, _ := pausar(t, a, tok, id, map[string]bool{"enabled": false}); st != http.StatusOK || corpo["enabled"] != false {
		t.Errorf("pausar o pausado deu %d %v, queria 200 sem efeito", st, corpo["enabled"])
	}
	if st, corpo, _ := pausar(t, a, tok, id, map[string]bool{"enabled": true}); st != http.StatusOK || corpo["enabled"] != true {
		t.Errorf("retomar deu %d %v", st, corpo["enabled"])
	}
	if item := itemDaLista(t, a, tok, id); item["enabled"] != true {
		t.Errorf("GET /channels não reflete a retomada: %v", item)
	}
}

// Canal alheio, inexistente ou com id malformado é 404, e o canal alheio não
// muda. A perna positiva é o dono pausando o mesmo canal.
func TestPatchAnotherUsersChannelIs404(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	id := criarCanalDiscord(t, a, tok)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	for nome, caso := range map[string]struct{ tok, id string }{
		"canal alheio":      {intruso, id},
		"canal inexistente": {tok, uuid.NewString()},
		"id malformado":     {tok, "nao-e-uuid"},
	} {
		st, _, cru := pausar(t, a, caso.tok, caso.id, map[string]bool{"enabled": false})
		if st != http.StatusNotFound {
			t.Errorf("%s: %d %s, queria 404", nome, st, cru)
		}
	}
	if item := itemDaLista(t, a, tok, id); item["enabled"] != true {
		t.Fatalf("o intruso pausou o canal do dono: %v", item)
	}
	if st, _, _ := pausar(t, a, tok, id, map[string]bool{"enabled": false}); st != http.StatusOK {
		t.Errorf("o dono pausando o próprio canal deu %d", st)
	}
}

// Corpo sem enabled, com enabled que não é booleano, ou com outro campo: 400
// validation_failed. Os fields vêm do vocabulário fechado (o nome do campo
// desconhecido nunca ecoa). Nenhuma recusa muda o canal; a perna positiva é o
// corpo certo no fim.
func TestPatchChannelRejectsBadBodies(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	id := criarCanalDiscord(t, a, tok)

	type campo struct{ Field, Reason string }
	casos := []struct {
		nome   string
		corpo  any
		campos []campo
	}{
		{"corpo vazio", map[string]any{}, []campo{{"enabled", "required"}}},
		{"enabled texto", map[string]any{"enabled": "false"}, []campo{{"enabled", "invalid"}}},
		{"enabled número", map[string]any{"enabled": 0}, []campo{{"enabled", "invalid"}}},
		{"enabled null", map[string]any{"enabled": nil}, []campo{{"enabled", "invalid"}}},
		{"campo a mais", map[string]any{"enabled": false, "url": "https://evil.example"}, nil},
		{"não é objeto", []bool{false}, nil},
	}
	for _, c := range casos {
		resp := chamar(t, a, http.MethodPatch, "/api/v1/channels/"+id, tok, c.corpo)
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Fields []struct {
					Field  string `json:"field"`
					Reason string `json:"reason"`
				} `json:"fields"`
			} `json:"error"`
		}
		cru := lerCorpo(t, resp, &env)
		if resp.StatusCode != http.StatusBadRequest || env.Error.Code != "validation_failed" {
			t.Errorf("%s: %d %s, queria 400 validation_failed", c.nome, resp.StatusCode, cru)
			continue
		}
		var got []campo
		for _, f := range env.Error.Fields {
			got = append(got, campo{f.Field, f.Reason})
		}
		if !slices.Equal(got, c.campos) {
			t.Errorf("%s: fields = %v, queria %v", c.nome, got, c.campos)
		}
	}
	if item := itemDaLista(t, a, tok, id); item["enabled"] != true {
		t.Fatalf("um corpo recusado mudou o canal: %v", item)
	}
	if st, _, _ := pausar(t, a, tok, id, map[string]bool{"enabled": false}); st != http.StatusOK {
		t.Errorf("o corpo certo deu %d", st)
	}
}

// A rota fica atrás de protegida, e não do balde de envio (5 de uma vez): ela
// não manda mensagem para fora. Seis PATCHes seguidos passam.
func TestPatchChannelIsNotASendRoute(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	id := criarCanalDiscord(t, a, tok) // gasta 1 do balde de envio
	for i := range 6 {
		if st, _, cru := pausar(t, a, tok, id, map[string]bool{"enabled": i%2 == 0}); st != http.StatusOK {
			t.Fatalf("PATCH %d deu %d: %s", i+1, st, cru)
		}
	}
}

// entregadorFalso é o Discord do despacho: conta os envios.
type entregadorFalso struct {
	mu     sync.Mutex
	envios int
}

func (*entregadorFalso) Kind() notify.Kind { return notify.KindDiscord }

func (e *entregadorFalso) Send(context.Context, crypto.Secret, notify.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.envios++
	return nil
}

func (e *entregadorFalso) quantos() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.envios
}

// O caminho pela rota até o despacho (spec §6, linha 3.3): pausado pelo
// PATCH, o aviso pendente é cancelado no envio; retomado pelo PATCH, o
// seguinte sai. O que foi cancelado não volta.
func TestPausedChannelDropsThroughTheRoute(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	canal := uuid.MustParse(criarCanalDiscord(t, a, tok))

	agora := a.clk.Now()
	alvo, incidente := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: a.userID, Name: "API", URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	}); err != nil {
		t.Fatal(err)
	}
	enfileirar := func(evento string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
			return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
				ID: id, IncidentID: incidente, ChannelID: canal, Event: evento, Now: a.clk.Now(),
			})
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: incidente, TargetID: alvo, StartedAt: agora.Add(-time.Minute), Cause: "timeout"})
	}); err != nil {
		t.Fatal(err)
	}
	status := func(id uuid.UUID) string {
		t.Helper()
		// Sob o escopo do dono: a RLS de notifications delega ao canal.
		var s string
		tx, err := a.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT status FROM notifications WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	box, err := crypto.NewBox(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	entregador := &entregadorFalso{}
	despacho := notify.NewDispatcher(a.pool, box, a.clk, prometheus.NewRegistry(), entregador)
	despachar := func() {
		t.Helper()
		if n, err := despacho.Dispatch(ctx); err != nil || n != 1 {
			t.Fatalf("Dispatch = %d, %v; queria 1", n, err)
		}
	}

	abertura := enfileirar(incident.EventOpened)
	if st, _, cru := pausar(t, a, tok, canal.String(), map[string]bool{"enabled": false}); st != http.StatusOK {
		t.Fatalf("pausar deu %d: %s", st, cru)
	}
	despachar()
	if entregador.quantos() != 0 || status(abertura) != "cancelled" {
		t.Fatalf("pausado pela rota: %d envios, status %s; queria 0 e cancelled", entregador.quantos(), status(abertura))
	}

	if st, _, cru := pausar(t, a, tok, canal.String(), map[string]bool{"enabled": true}); st != http.StatusOK {
		t.Fatalf("retomar deu %d: %s", st, cru)
	}
	fechamento := enfileirar(incident.EventResolved)
	despachar()
	if entregador.quantos() != 1 || status(fechamento) != "sent" {
		t.Errorf("retomado pela rota: %d envios, status %s; queria 1 e sent", entregador.quantos(), status(fechamento))
	}
	if status(abertura) != "cancelled" {
		t.Error("retomar reenviou o aviso cancelado durante a pausa")
	}
}
