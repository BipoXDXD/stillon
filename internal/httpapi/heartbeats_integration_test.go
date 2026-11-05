package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

var corpoDePeriodo = map[string]any{
	"name": "backup noturno", "schedule": map[string]any{"kind": "period", "period_sec": 3600}, "grace_sec": 300,
}

func decodificar(t *testing.T, corpo string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(corpo), &m); err != nil {
		t.Fatalf("corpo inválido: %v: %s", err, corpo)
	}
	return m
}

func chavesDe(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// criarHB devolve o id e o token, recortado da ping_url.
func criarHB(t *testing.T, a *api, token string, corpo map[string]any) (string, string) {
	t.Helper()
	status, resp := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", token, corpo)
	if status != http.StatusCreated {
		t.Fatalf("criar heartbeat deu %d: %s", status, resp)
	}
	m := decodificar(t, resp)
	url, _ := m["ping_url"].(string)
	_, ping, ok := strings.Cut(url, "/ping/")
	if !ok {
		t.Fatalf("ping_url %q", url)
	}
	return m["id"].(string), ping
}

// avaliar roda o avaliador com o relógio da API.
func avaliar(t *testing.T, a *api) {
	t.Helper()
	if _, err := prober.NewHeartbeatEvaluator(a.pool, a.clk).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCreateHeartbeatReturnsThePingURLOnce(t *testing.T) {
	a := newAPIWith(t, nil)
	dono := tokenDe(t, a, a.userID)
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, corpoDePeriodo)
	if status != http.StatusCreated {
		t.Fatalf("status %d: %s", status, corpo)
	}
	m := decodificar(t, corpo)
	if !strings.Contains(m["ping_url"].(string), "/ping/") {
		t.Errorf("ping_url: %v", m["ping_url"])
	}
	_, token, _ := strings.Cut(m["ping_url"].(string), "/ping/")
	id := m["id"].(string)
	for _, caminho := range []string{"/api/v1/heartbeats/" + id, "/api/v1/heartbeats"} {
		_, leitura := lerCru(t, a, http.MethodGet, caminho, dono, nil)
		if strings.Contains(leitura, token) || strings.Contains(leitura, "ping_url") {
			t.Errorf("GET %s devolveu o token: %s", caminho, leitura)
		}
	}
}

// O token sai uma vez e não fica no banco: só o SHA-256 dele.
func TestCreateStoresOnlyTheTokenHash(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	_, token := criarHB(t, a, tokenDe(t, a, a.userID), corpoDePeriodo)

	var hash []byte
	var linha string
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT ping_token_hash, h::text FROM heartbeats h`).Scan(&hash, &linha); err != nil {
		t.Fatal(err)
	}
	if string(hash) != string(crypto.HashToken(token)) {
		t.Error("a coluna não é o SHA-256 do token")
	}
	if strings.Contains(linha, token) {
		t.Errorf("o token está na linha: %s", linha)
	}
}

func TestHeartbeatResponseHasExactKeys(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	base := []string{"created_at", "deadline_at", "enabled", "grace_sec", "id", "last_ping_at", "last_start_at",
		"last_success_at", "name", "open_incident_id", "schedule", "state"}

	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	m := decodificar(t, corpo)
	if got := chavesDe(m); !slices.Equal(got, base) {
		t.Errorf("chaves %v, queria %v", got, base)
	}
	if got := chavesDe(m["schedule"].(map[string]any)); !slices.Equal(got, []string{"kind", "period_sec"}) {
		t.Errorf("schedule de período: %v", got)
	}
	_, corpo = lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/token/rotate", dono, nil)
	comURL := append(slices.Clone(base), "ping_url")
	sort.Strings(comURL)
	if got := chavesDe(decodificar(t, corpo)); !slices.Equal(got, comURL) {
		t.Errorf("rotação: %v", got)
	}

	pingar(t, a, "/ping/"+tokenDaURL(t, corpo), nil)
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/events", dono, nil)
	ev := decodificar(t, corpo)["items"].([]any)
	if len(ev) != 1 || !slices.Equal(chavesDe(ev[0].(map[string]any)), []string{"at", "id", "kind", "runtime_ms"}) {
		t.Errorf("evento: %s", corpo)
	}
}

func tokenDaURL(t *testing.T, corpo string) string {
	t.Helper()
	url, _ := decodificar(t, corpo)["ping_url"].(string)
	_, token, ok := strings.Cut(url, "/ping/")
	if !ok {
		t.Fatalf("sem ping_url: %s", corpo)
	}
	return token
}

func TestCronHeartbeatRoundTrip(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, map[string]any{
		"name": "relatório", "schedule": map[string]any{"kind": "cron", "expr": "0 3 * * *", "timezone": "America/Sao_Paulo"},
	})
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	m := decodificar(t, corpo)
	s := m["schedule"].(map[string]any)
	if s["kind"] != "cron" || s["expr"] != "0 3 * * *" || s["timezone"] != "America/Sao_Paulo" || len(s) != 3 {
		t.Errorf("schedule: %v", s)
	}
	if m["grace_sec"] != float64(300) || m["state"] != "waiting" {
		t.Errorf("padrões: grace %v, state %v", m["grace_sec"], m["state"])
	}
}

// Campo que o cliente não define é 400, e o banco não muda.
func TestHeartbeatEntryIsStrict(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	_, antes := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)

	for _, extra := range []string{"id", "user_id", "state", "deadline_at", "ping_token_hash", "failing_since"} {
		novo := map[string]any{"name": "x", "schedule": map[string]any{"kind": "period", "period_sec": 60}, extra: "invadido"}
		if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, novo); status != http.StatusBadRequest {
			t.Errorf("POST com %s deu %d: %s", extra, status, corpo)
		}
		if status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{extra: "invadido"}); status != http.StatusBadRequest {
			t.Errorf("PATCH com %s deu %d: %s", extra, status, corpo)
		}
	}
	if _, depois := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); depois != antes {
		t.Errorf("o heartbeat mudou:\n%s\n%s", antes, depois)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", dono, nil)
	if n := len(decodificar(t, lista)["items"].([]any)); n != 1 {
		t.Errorf("%d heartbeats, queria 1", n)
	}
}

func TestHeartbeatValidationBoundaries(t *testing.T) {
	periodo := func(s int) map[string]any { return map[string]any{"kind": "period", "period_sec": s} }
	casos := []struct {
		nome  string
		corpo map[string]any
		ok    bool
		campo string
	}{
		{"nome vazio", map[string]any{"name": "  ", "schedule": periodo(60)}, false, "name"},
		{"nome no teto", map[string]any{"name": strings.Repeat("a", 100), "schedule": periodo(60)}, true, ""},
		{"nome acima do teto", map[string]any{"name": strings.Repeat("a", 101), "schedule": periodo(60)}, false, "name"},
		{"nome com NUL", map[string]any{"name": "a\u0000b", "schedule": periodo(60)}, false, "name"},
		{"sem agendamento", map[string]any{"name": "a"}, false, "schedule"},
		{"tipo desconhecido", map[string]any{"name": "a", "schedule": map[string]any{"kind": "x"}}, false, "schedule.kind"},
		{"período sem valor", map[string]any{"name": "a", "schedule": map[string]any{"kind": "period"}}, false, "period_sec"},
		{"período abaixo", map[string]any{"name": "a", "schedule": periodo(59)}, false, "period_sec"},
		{"período no piso", map[string]any{"name": "a", "schedule": periodo(60)}, true, ""},
		{"período no teto", map[string]any{"name": "a", "schedule": periodo(2678400)}, true, ""},
		{"período acima", map[string]any{"name": "a", "schedule": periodo(2678401)}, false, "period_sec"},
		{"período com expr", map[string]any{"name": "a", "schedule": map[string]any{"kind": "period", "period_sec": 60, "expr": "* * * * *"}}, false, "schedule"},
		{"cron com período", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "period_sec": 60, "expr": "* * * * *", "timezone": "UTC"}}, false, "schedule"},
		{"cron sem fuso", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "expr": "* * * * *"}}, false, "timezone"},
		{"cron inválido", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "expr": "nunca", "timezone": "UTC"}}, false, "expr"},
		{"tolerância abaixo", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 29}, false, "grace_sec"},
		{"tolerância no piso", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 30}, true, ""},
		{"tolerância no teto", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 604800}, true, ""},
		{"tolerância acima", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 604801}, false, "grace_sec"},
	}
	a := newAPI(t)
	for i, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			// Um usuário por caso: o limite por usuário cortaria a tabela.
			dono := tokenDe(t, a, outroUsuario(t, a))
			status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, c.corpo)
			if c.ok {
				if status != http.StatusCreated {
					t.Fatalf("caso %d: %d %s", i, status, corpo)
				}
				return
			}
			if status != http.StatusBadRequest || !strings.Contains(corpo, `"`+c.campo+`"`) {
				t.Fatalf("caso %d: %d %s, queria 400 em %s", i, status, corpo, c.campo)
			}
		})
	}
}

// Campo ausente não muda nada; o que veio muda.
func TestPatchAppliesOnlyWhatCame(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	_, antes := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	prazo := decodificar(t, antes)["deadline_at"]

	a.clk.Advance(time.Minute)
	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"name": "novo nome"})
	m := decodificar(t, corpo)
	if status != http.StatusOK || m["name"] != "novo nome" || m["grace_sec"] != float64(300) || m["deadline_at"] != prazo {
		t.Fatalf("PATCH só do nome: %d %s", status, corpo)
	}
	if s := m["schedule"].(map[string]any); s["period_sec"] != float64(3600) {
		t.Errorf("o agendamento mudou: %v", s)
	}

	_, corpo = lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"grace_sec": 600})
	prazoNovo, err := time.Parse(time.RFC3339, decodificar(t, corpo)["deadline_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if want := a.clk.Now().Add(time.Hour + 10*time.Minute); !prazoNovo.Equal(want) {
		t.Errorf("tolerância nova: prazo %v, queria %v", prazoNovo, want)
	}
	status, corpo = lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{
		"schedule": map[string]any{"kind": "cron", "expr": "0 3 * * *", "timezone": "UTC"},
	})
	if s := decodificar(t, corpo)["schedule"].(map[string]any); status != http.StatusOK || s["kind"] != "cron" {
		t.Errorf("trocar para cron: %d %s", status, corpo)
	}
}

// D13: retomar zera a falha e recalcula o prazo, e o avaliador fecha o aberto.
func TestResumingClearsTheFailureAndClosesTheIncident(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	m := decodificar(t, corpo)
	if m["state"] != "failing" || m["open_incident_id"] == nil {
		t.Fatalf("depois da falha: %s", corpo)
	}

	lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": false})
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if decodificar(t, corpo)["state"] != "paused" {
		t.Fatalf("pausado: %s", corpo)
	}
	a.clk.Advance(3 * time.Hour)
	dono = tokenDe(t, a, a.userID) // o relógio avançou além da validade do token
	_, corpo = lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": true})
	m = decodificar(t, corpo)
	if m["state"] != "waiting" && m["state"] != "up" {
		t.Fatalf("retomado: %s", corpo)
	}
	avaliar(t, a)
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if decodificar(t, corpo)["open_incident_id"] != nil {
		t.Errorf("o incidente seguiu aberto: %s", corpo)
	}
}

func TestStateFollowsTheClockAndTheSignals(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	estado := func() any {
		_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
		return decodificar(t, corpo)["state"]
	}
	if e := estado(); e != "waiting" {
		t.Errorf("novo: %v", e)
	}
	pingar(t, a, "/ping/"+ping, nil)
	if e := estado(); e != "up" {
		t.Errorf("depois do ping: %v", e)
	}
	a.clk.Advance(time.Hour + 5*time.Minute)
	dono = tokenDe(t, a, a.userID) // o relógio avançou além da validade do token
	if e := estado(); e != "late" {
		t.Errorf("no prazo: %v", e)
	}
}

func TestRotateRevokesTheOldToken(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, antigo := criarHB(t, a, dono, corpoDePeriodo)
	if status, _, corpo := pingar(t, a, "/ping/"+antigo, nil); status != http.StatusOK {
		t.Fatalf("ping antes de rotacionar: %d %s", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/token/rotate", dono, nil)
	if status != http.StatusOK {
		t.Fatalf("rotacionar: %d %s", status, corpo)
	}
	novo := tokenDaURL(t, corpo)
	if novo == antigo {
		t.Fatal("o token não mudou")
	}
	if status, _, corpo := pingar(t, a, "/ping/"+antigo+"/start", nil); status != http.StatusNotFound {
		t.Errorf("token antigo deu %d %s", status, corpo)
	}
	if status, _, corpo := pingar(t, a, "/ping/"+novo+"/start", nil); status != http.StatusOK {
		t.Errorf("token novo deu %d %s", status, corpo)
	}
}

func TestDeletingAHeartbeatRevokesItsToken(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	if status, corpo := lerCru(t, a, http.MethodDelete, "/api/v1/heartbeats/"+id, dono, nil); status != http.StatusNoContent {
		t.Fatalf("apagar: %d %s", status, corpo)
	}
	if status, _, _ := pingar(t, a, "/ping/"+ping, nil); status != http.StatusNotFound {
		t.Errorf("ping depois de apagar: %d", status)
	}
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); status != http.StatusNotFound {
		t.Errorf("GET depois de apagar: %d", status)
	}
}

func TestAnotherUsersHeartbeatIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents", dono, nil)
	incidente := decodificar(t, corpo)["items"].([]any)[0].(map[string]any)["id"].(string)

	molde := "/api/v1/heartbeats/%s"
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodGet, molde: molde, id: id},
			{metodo: http.MethodPatch, molde: molde, id: id, corpo: map[string]any{"name": "roubado"}},
			{metodo: http.MethodGet, molde: molde + "/events", id: id},
			{metodo: http.MethodGet, molde: molde + "/incidents", id: id},
			{metodo: http.MethodPost, molde: molde + "/token/rotate", id: id},
			{metodo: http.MethodPost, molde: molde + "/incidents/" + incidente + "/ack", id: id},
			{metodo: http.MethodDelete, molde: molde, id: id},
		},
		leituras: []string{"/api/v1/heartbeats/" + id, "/api/v1/heartbeats", "/api/v1/heartbeats/" + id + "/incidents",
			"/api/v1/heartbeats/" + id + "/events"},
		listasDoIntruso: []string{"/api/v1/heartbeats"},
		dadosDoDono:     []string{id, "backup noturno"},
	})
	if status, _, corpo := pingar(t, a, "/ping/"+ping, nil); status != http.StatusOK {
		t.Errorf("o token do dono parou: %d %s", status, corpo)
	}
}

// O ack exige que o incidente seja daquele heartbeat: id de incidente de outro
// heartbeat do mesmo dono não passa por este caminho.
func TestAckIsScopedToTheHeartbeatAndIdempotent(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	outro, _ := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents?status=open", dono, nil)
	item := decodificar(t, corpo)["items"].([]any)[0].(map[string]any)
	incidente := item["id"].(string)
	if item["cause"] != "heartbeat_failed" || item["heartbeat_name"] != "backup noturno" {
		t.Errorf("incidente: %v", item)
	}
	if got := chavesDe(item); !slices.Equal(got, []string{"acknowledged_at", "cause", "heartbeat_id", "heartbeat_name", "id", "resolved_at", "started_at"}) {
		t.Errorf("chaves: %v", got)
	}

	if status, _ := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+outro+"/incidents/"+incidente+"/ack", dono, nil); status != http.StatusNotFound {
		t.Errorf("ack pelo heartbeat errado deu %d", status)
	}
	if status, _ := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/incidents/nao-e-uuid/ack", dono, nil); status != http.StatusNotFound {
		t.Errorf("ack malformado deu %d", status)
	}
	_, primeiro := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/incidents/"+incidente+"/ack", dono, nil)
	a.clk.Advance(time.Hour)
	dono = tokenDe(t, a, a.userID) // o relógio avançou além da validade do token
	_, segundo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/incidents/"+incidente+"/ack", dono, nil)
	p, s := decodificar(t, primeiro)["acknowledged_at"], decodificar(t, segundo)["acknowledged_at"]
	if p == nil || p != s {
		t.Errorf("ack não preservou o primeiro: %v, %v", p, s)
	}
	_, resolvidos := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents?status=resolved", dono, nil)
	if n := len(decodificar(t, resolvidos)["items"].([]any)); n != 0 {
		t.Errorf("%d resolvidos, queria 0", n)
	}
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents?status=x", dono, nil); status != http.StatusBadRequest {
		t.Errorf("status inválido deu %d", status)
	}
}

// D3: os incidentes de heartbeat não entram em /incidents, cujo contrato exige
// target_id em todo item: o painel de hoje recusaria a lista inteira.
func TestIncidentsListStaysOnlyWithTargets(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	_, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/incidents", dono, nil)
	if status != http.StatusOK {
		t.Fatalf("%d %s", status, corpo)
	}
	if n := len(decodificar(t, corpo)["items"].([]any)); n != 0 {
		t.Errorf("/incidents devolveu %d itens: %s", n, corpo)
	}
}

func TestHeartbeatListPagesByCursor(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	ids := map[string]bool{}
	for range 3 {
		id, _ := criarHB(t, a, dono, corpoDePeriodo)
		ids[id] = true
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=2", dono, nil)
	m := decodificar(t, corpo)
	if len(m["items"].([]any)) != 2 || m["next_cursor"] == nil {
		t.Fatalf("primeira página: %s", corpo)
	}
	_, corpo = lerCru(t, a, http.MethodGet, fmt.Sprintf("/api/v1/heartbeats?limit=2&cursor=%s", m["next_cursor"]), dono, nil)
	if n := len(decodificar(t, corpo)["items"].([]any)); n != 1 {
		t.Errorf("segunda página: %s", corpo)
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=x"} {
		if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?"+q, dono, nil); status != http.StatusBadRequest {
			t.Errorf("%s deu %d", q, status)
		}
	}
}

func encherHeartbeats(t *testing.T, a *api, n int64) {
	t.Helper()
	ctx := context.Background()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		for range n {
			_, hash := heartbeat.NewPingToken()
			if err := q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
				ID: uuid.Must(uuid.NewV7()), UserID: a.userID, Name: "h", PingTokenHash: hash, PeriodSec: &periodo,
				GraceSec: 300, Enabled: true, CreatedAt: a.clk.Now(), DeadlineAt: a.clk.Now().Add(time.Hour),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatLimitPerUser(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	encherHeartbeats(t, a, heartbeat.MaxPerUser-1)
	if status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, corpoDePeriodo); status != http.StatusCreated {
		t.Fatalf("último dentro do teto: %d %s", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, corpoDePeriodo)
	if status != http.StatusConflict || !strings.Contains(corpo, "heartbeat_limit_reached") {
		t.Errorf("acima do teto: %d %s", status, corpo)
	}
}

// Criar e rotacionar devolvem o token, e nenhum dos dois o escreve no log.
func TestHeartbeatTokenNeverReachesTheLog(t *testing.T) {
	log := capturarLog(t)
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	_, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/token/rotate", dono, nil)
	if strings.Contains(log.String(), ping) || strings.Contains(log.String(), tokenDaURL(t, corpo)) {
		t.Fatalf("token no log: %s", log.String())
	}
}
