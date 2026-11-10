package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
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
	base := []string{"created_at", "deadline_at", "description", "enabled", "grace_sec", "id", "last_ping_at", "last_runtime_ms", "last_start_at",
		"last_success_at", "name", "open_incident_id", "ping_resumes", "schedule", "state", "tags"}

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
}

// O evento sai com o mesmo conjunto de chaves com e sem rid: run_id ausente é
// null, e não some.
func TestHeartbeatEventHasExactKeys(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"?rid="+ridValido, nil)
	pingar(t, a, "/ping/"+ping, nil)

	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/events", dono, nil)
	ev := decodificar(t, corpo)["items"].([]any)
	if len(ev) != 2 {
		t.Fatalf("eventos: %s", corpo)
	}
	quer := []string{"at", "exit_code", "id", "kind", "run_id", "runtime_ms", "source"}
	for i, e := range ev {
		if got := chavesDe(e.(map[string]any)); !slices.Equal(got, quer) {
			t.Errorf("evento %d: chaves %v, queria %v", i, got, quer)
		}
	}
}

// O rid volta como o servidor o guardou, em minúsculas; sem rid, run_id é null.
// A origem do sinal pela URL de ping é "ping".
func TestEventListShowsRunIDAndSource(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/start?rid="+strings.ToUpper(ridValido), nil)
	pingar(t, a, "/ping/"+ping, nil)

	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/events", dono, nil)
	ev := decodificar(t, corpo)["items"].([]any)
	if len(ev) != 2 {
		t.Fatalf("eventos: %s", corpo)
	}
	semRID, comRID := ev[0].(map[string]any), ev[1].(map[string]any)
	if comRID["run_id"] != "0195f3c2-8a4b-7c1d-9e2f-3a4b5c6d7e8f" || comRID["source"] != "ping" {
		t.Errorf("com rid: %v", comRID)
	}
	if v, ok := semRID["run_id"]; !ok || v != nil || semRID["source"] != "ping" {
		t.Errorf("sem rid: %v", semRID)
	}
}

func pingResumesDe(t *testing.T, corpo string) any {
	t.Helper()
	return decodificar(t, corpo)["ping_resumes"]
}

// ping_resumes nasce false, vem no cadastro e muda no PATCH sem mexer no prazo.
// null e tipo errado são 400, e nada muda.
func TestPingResumesRoundTrip(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	semCampo, _ := criarHB(t, a, dono, corpoDePeriodo)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+semCampo, dono, nil)
	if v := pingResumesDe(t, corpo); v != false {
		t.Errorf("sem o campo: %v", v)
	}

	comCampo := maps.Clone(corpoDePeriodo)
	comCampo["ping_resumes"] = true
	id, _ := criarHB(t, a, dono, comCampo)
	_, antes := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if v := pingResumesDe(t, antes); v != true {
		t.Fatalf("com true: %v", v)
	}
	prazo := decodificar(t, antes)["deadline_at"]

	a.clk.Advance(time.Minute)
	caminho := "/api/v1/heartbeats/" + id
	// Tipo errado é recusado pelo decoder, sem nomear o campo, como nos demais.
	for _, c := range []struct {
		nome  string
		valor any
		marca string
	}{{"null", nil, `"field":"ping_resumes"`}, {"texto", "sim", `"validation_failed"`}} {
		status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, map[string]any{"ping_resumes": c.valor})
		if status != http.StatusBadRequest || !strings.Contains(resp, c.marca) {
			t.Errorf("PATCH %s: %d %s, queria 400 com %s", c.nome, status, resp, c.marca)
		}
		if _, depois := lerCru(t, a, http.MethodGet, caminho, dono, nil); depois != antes {
			t.Errorf("PATCH %s mudou o heartbeat:\n%s\n%s", c.nome, antes, depois)
		}
	}

	status, corpo := lerCru(t, a, http.MethodPatch, caminho, dono, map[string]any{"ping_resumes": false})
	m := decodificar(t, corpo)
	if status != http.StatusOK || m["ping_resumes"] != false || m["deadline_at"] != prazo {
		t.Errorf("PATCH false: %d %s, queria false com prazo %v", status, corpo, prazo)
	}
	_, corpo = lerCru(t, a, http.MethodGet, caminho, dono, nil)
	if v := pingResumesDe(t, corpo); v != false {
		t.Errorf("leitura depois do PATCH: %v", v)
	}
}

// Período de 1 h e tolerância de 10 min: o prazo é t0+1h10min, e a tolerância
// é [t0+1h, t0+1h10min).
func TestStateShowsGraceInsideTheWindow(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, map[string]any{
		"name": "backup", "schedule": map[string]any{"kind": "period", "period_sec": 3600}, "grace_sec": 600,
	})
	pingar(t, a, "/ping/"+ping, nil)
	estado := func() any {
		t.Helper()
		dono = tokenDe(t, a, a.userID) // o relógio avança além da validade do token
		_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
		return decodificar(t, corpo)["state"]
	}
	a.clk.Advance(time.Hour + time.Minute)
	if e := estado(); e != "grace" {
		t.Errorf("em t0+1h1min: %v, queria grace", e)
	}
	a.clk.Advance(9 * time.Minute)
	if e := estado(); e != "late" {
		t.Errorf("em t0+1h10min: %v, queria late", e)
	}
}

// A lista e o detalhe trazem a última duração, e o início fica visível enquanto
// a execução corre.
func TestListAndDetailShowTheLastRuntime(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, token := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+token+"/start", nil)
	a.clk.Advance(90 * time.Second)

	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	h := decodificar(t, corpo)
	if h["last_start_at"] == nil || h["last_runtime_ms"] != nil {
		t.Errorf("em execução: %s", corpo)
	}
	pingar(t, a, "/ping/"+token, nil)

	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", dono, nil)
	h = decodificar(t, corpo)["items"].([]any)[0].(map[string]any)
	if h["last_start_at"] != nil || h["last_runtime_ms"] != float64(90000) {
		t.Errorf("depois do sucesso: %s", corpo)
	}
}

func tagsDe(t *testing.T, corpo string) []any {
	t.Helper()
	tags, ok := decodificar(t, corpo)["tags"].([]any)
	if !ok {
		t.Fatalf("sem lista de tags: %s", corpo)
	}
	return tags
}

// Criar, ler, listar e editar. A forma canônica é a mesma dos alvos.
func TestHeartbeatTagsRoundTrip(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["tags"] = []string{"Prod", " prod ", "dados"}
	id, _ := criarHB(t, a, dono, corpo)

	_, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if got := tagsDe(t, lido); !slices.Equal(got, []any{"dados", "prod"}) {
		t.Errorf("detalhe: %v", got)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", dono, nil)
	item := decodificar(t, lista)["items"].([]any)[0].(map[string]any)
	if got := item["tags"].([]any); !slices.Equal(got, []any{"dados", "prod"}) {
		t.Errorf("lista: %v", got)
	}
}

// Sem tags é lista vazia, nunca null: o schema do painel recusaria o null.
func TestHeartbeatWithoutTagsHasAnEmptyList(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	for _, caminho := range []string{"/api/v1/heartbeats/" + id, "/api/v1/heartbeats?limit=100"} {
		_, corpo := lerCru(t, a, http.MethodGet, caminho, dono, nil)
		if strings.Contains(corpo, `"tags":null`) || !strings.Contains(corpo, `"tags":[]`) {
			t.Errorf("%s: %s", caminho, corpo)
		}
	}
}

// Ausente não mexe; lista troca; [] e null limpam.
func TestPatchOfHeartbeatTags(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["tags"] = []string{"prod"}
	id, _ := criarHB(t, a, dono, corpo)
	vizinho, _ := criarHB(t, a, dono, corpo)
	t.Cleanup(func() {
		_, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+vizinho, dono, nil)
		if got := tagsDe(t, lido); !slices.Equal(got, []any{"prod"}) {
			t.Errorf("as tags do vizinho mudaram: %v", got)
		}
	})

	casos := []struct {
		nome  string
		corpo map[string]any
		tags  []any
	}{
		{"ausente não mexe", map[string]any{"name": "outro"}, []any{"prod"}},
		{"lista troca", map[string]any{"tags": []string{"api", "web"}}, []any{"api", "web"}},
		{"vazia limpa", map[string]any{"tags": []string{}}, []any{}},
		{"volta", map[string]any{"tags": []string{"prod"}}, []any{"prod"}},
		{"null limpa", map[string]any{"tags": nil}, []any{}},
	}
	for _, c := range casos {
		status, resp := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, c.corpo)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", c.nome, status, resp)
		}
		if got := tagsDe(t, resp); !slices.Equal(got, c.tags) {
			t.Errorf("%s: %v, queria %v", c.nome, got, c.tags)
		}
	}
}

func TestHeartbeatTagsAreValidatedLikeTargetTags(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	casos := []struct {
		nome, motivo string
		tags         []string
	}{
		{"com espaço", "invalid", []string{"a b"}},
		{"com vírgula", "invalid", []string{"a,b"}},
		{"NUL", "invalid", []string{"a\x00b"}},
		{"injeção", "invalid", []string{"';DROP--"}},
		{"25 caracteres", "too_long", []string{strings.Repeat("a", 25)}},
		{"nove tags", "out_of_range", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			criar := maps.Clone(corpoDePeriodo)
			criar["tags"] = c.tags
			status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, criar)
			if status != http.StatusBadRequest || !strings.Contains(corpo, `"tags"`) || !strings.Contains(corpo, c.motivo) {
				t.Errorf("POST: %d %s", status, corpo)
			}
			status, corpo = lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"tags": c.tags})
			if status != http.StatusBadRequest || !strings.Contains(corpo, c.motivo) {
				t.Errorf("PATCH: %d %s", status, corpo)
			}
		})
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", dono, nil)
	if itens := decodificar(t, corpo)["items"].([]any); len(itens) != 1 || len(itens[0].(map[string]any)["tags"].([]any)) != 0 {
		t.Errorf("tag inválida deixou rastro: %s", corpo)
	}
}

// B não vê as tags de A nem as altera, e o PATCH dele é o 404 de sempre.
func TestAnotherUsersHeartbeatTagsStayOut(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["tags"] = []string{"segredo-de-a"}
	id, _ := criarHB(t, a, dono, corpo)
	outro := tokenDe(t, a, outroUsuario(t, a))

	status, resp := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, outro, map[string]any{"tags": []string{"invadida"}})
	if status != http.StatusNotFound || strings.Contains(resp, "segredo-de-a") {
		t.Errorf("PATCH de B: %d %s", status, resp)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", outro, nil)
	if strings.Contains(lista, "segredo-de-a") {
		t.Errorf("lista de B: %s", lista)
	}
	_, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if got := tagsDe(t, lido); !slices.Equal(got, []any{"segredo-de-a"}) {
		t.Errorf("as tags de A mudaram: %v", got)
	}
}

func descricaoDe(t *testing.T, corpo string) any {
	t.Helper()
	return decodificar(t, corpo)["description"]
}

// A descrição sai igual nas três leituras, com a quebra de linha, e sem ela o
// campo é "", nunca null: o painel não tem dois jeitos de dizer "sem descrição".
func TestHeartbeatDescriptionRoundTrip(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	texto := "Roda no NAS.\nSe falhar: ver /var/log/backup.log"
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = "  " + texto + "\n"
	status, criado := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats", dono, corpo)
	if status != http.StatusCreated || descricaoDe(t, criado) != texto {
		t.Fatalf("criar: %d %s", status, criado)
	}
	id := decodificar(t, criado)["id"].(string)
	if _, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); descricaoDe(t, lido) != texto {
		t.Errorf("detalhe: %s", lido)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", dono, nil)
	if got := decodificar(t, lista)["items"].([]any)[0].(map[string]any)["description"]; got != texto {
		t.Errorf("lista: %s", lista)
	}

	sem, _ := criarHB(t, a, dono, corpoDePeriodo)
	if _, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+sem, dono, nil); descricaoDe(t, lido) != "" {
		t.Errorf("sem descrição: %s", lido)
	}
}

// Ausente não mexe; texto troca; "" e null limpam. Mudar a descrição não mexe
// no prazo: é anotação, e não agendamento.
func TestPatchOfHeartbeatDescription(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = "antes"
	id, _ := criarHB(t, a, dono, corpo)
	_, inicial := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	prazo := decodificar(t, inicial)["deadline_at"]
	a.clk.Advance(time.Minute)

	passos := []struct {
		nome  string
		corpo map[string]any
		quer  string
	}{
		{"ausente não mexe", map[string]any{"name": "novo"}, "antes"},
		{"texto troca", map[string]any{"description": "depois"}, "depois"},
		{"vazio limpa", map[string]any{"description": ""}, ""},
		{"texto de novo", map[string]any{"description": "outra"}, "outra"},
		{"null limpa", map[string]any{"description": nil}, ""},
	}
	for _, p := range passos {
		status, resp := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, p.corpo)
		m := decodificar(t, resp)
		if status != http.StatusOK || m["description"] != p.quer || m["deadline_at"] != prazo {
			t.Errorf("%s: %d %s", p.nome, status, resp)
		}
	}
}

func TestPatchWithInvalidDescriptionChangesNothing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = "antes"
	id, _ := criarHB(t, a, dono, corpo)
	_, antes := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	for _, invalida := range []any{strings.Repeat("a", 10001), "a\u0000b", "'; DROP TABLE heartbeats; --\u202E", 123} {
		status, resp := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"description": invalida, "name": "invadido"})
		if status != http.StatusBadRequest {
			t.Errorf("%v: %d %s", invalida, status, resp)
		}
	}
	if _, depois := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); depois != antes {
		t.Errorf("o heartbeat mudou:\n%s\n%s", antes, depois)
	}
}

// O 400 diz a linha pelo campo, no envelope de sempre, e não ecoa o texto: a
// descrição é do dono, mas o erro passa por logs e proxies.
func TestInvalidDescriptionNamesTheLineWithoutEchoingIt(t *testing.T) {
	const texto = "CANARY-linha-1\n\nCANARY-linha-3\u202E"
	querCampos := []apierr.FieldError{{Field: "description[2]", Reason: "invalid"}}

	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = texto
	chamadas := []struct {
		nome, metodo, rota string
		corpo              map[string]any
	}{
		{"POST", http.MethodPost, "/api/v1/heartbeats", corpo},
		{"PATCH", http.MethodPatch, "/api/v1/heartbeats/" + id, map[string]any{"description": texto}},
	}
	for _, c := range chamadas {
		t.Run(c.nome, func(t *testing.T) {
			status, resp := lerCru(t, a, c.metodo, c.rota, dono, c.corpo)
			if strings.Contains(resp, "CANARY") {
				t.Errorf("a resposta ecoou a descrição: %s", resp)
			}
			var env struct {
				Error struct {
					Fields []apierr.FieldError `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(resp), &env); err != nil {
				t.Fatal(err)
			}
			if status != http.StatusBadRequest || !slices.Equal(env.Error.Fields, querCampos) {
				t.Errorf("%d %s, queria 400 com %v", status, resp, querCampos)
			}
		})
	}
}

func TestAnotherUsersHeartbeatDescriptionStaysOut(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = "segredo-de-a"
	id, _ := criarHB(t, a, dono, corpo)
	outro := tokenDe(t, a, outroUsuario(t, a))

	status, resp := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, outro, map[string]any{"description": "invadida"})
	if status != http.StatusNotFound || strings.Contains(resp, "segredo-de-a") {
		t.Errorf("PATCH de B: %d %s", status, resp)
	}
	status, resp = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, outro, nil)
	if status != http.StatusNotFound || strings.Contains(resp, "segredo-de-a") {
		t.Errorf("GET de B: %d %s", status, resp)
	}
	if _, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", outro, nil); strings.Contains(lista, "segredo-de-a") {
		t.Errorf("lista de B: %s", lista)
	}
	if _, lido := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); descricaoDe(t, lido) != "segredo-de-a" {
		t.Errorf("a descrição de A mudou: %s", lido)
	}
}

// Aviso sai para terceiros (e-mail, webhook), e a descrição pode citar máquina e
// caminho: ela fica no painel. O ping público também não a devolve.
func TestHeartbeatDescriptionStaysOutOfThePingAndTheNotices(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	criarCanalDiscord(t, a, dono)
	corpo := maps.Clone(corpoDePeriodo)
	corpo["description"] = "CANARY-descricao"
	_, ping := criarHB(t, a, dono, corpo)
	_, _, resposta := pingar(t, a, "/ping/"+ping+"/fail", nil)
	if strings.Contains(resposta, "CANARY") {
		t.Errorf("resposta do ping: %s", resposta)
	}
	avaliar(t, a)
	a.clk.Advance(time.Minute)
	pingar(t, a, "/ping/"+ping, nil)
	avaliar(t, a)
	avisos := entregar(t, a)
	if len(avisos.corpo) == 0 {
		t.Fatal("nenhum aviso entregue: o teste não provaria nada")
	}
	if texto := strings.Join(append(avisos.assunto, avisos.corpo...), "\n"); strings.Contains(texto, "CANARY") {
		t.Errorf("aviso com a descrição: %s", texto)
	}
}

func previaDoCron(t *testing.T, a *api, sessao string, consulta url.Values) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/cron-preview?"+consulta.Encode(), sessao, nil)
}

// Cinco disparos em sequência, no fuso declarado: 3h em São Paulo são 6h UTC, e
// a resposta tem só a chave do contrato.
func TestCronPreviewListsTheNextRunsInTheTimezone(t *testing.T) {
	a := newAPI(t)
	sessao := tokenDe(t, a, a.userID)
	status, corpo := previaDoCron(t, a, sessao, url.Values{"expr": {"0 3 * * *"}, "timezone": {"America/Sao_Paulo"}})
	if status != http.StatusOK {
		t.Fatalf("%d %s", status, corpo)
	}
	m := decodificar(t, corpo)
	if got := chavesDe(m); !slices.Equal(got, []string{"next_runs"}) {
		t.Errorf("chaves %v", got)
	}
	runs := m["next_runs"].([]any)
	if len(runs) != 5 {
		t.Fatalf("%d disparos: %s", len(runs), corpo)
	}
	agora := a.clk.Now()
	var anterior time.Time
	for i, r := range runs {
		quando, err := time.Parse(time.RFC3339, r.(string))
		if err != nil {
			t.Fatal(err)
		}
		if h, m := quando.UTC().Hour(), quando.UTC().Minute(); h != 6 || m != 0 {
			t.Errorf("disparo %d: %v, queria 06:00 UTC", i, quando)
		}
		if !quando.After(agora) || (i > 0 && quando.Sub(anterior) != 24*time.Hour) {
			t.Errorf("disparo %d fora de sequência: %v depois de %v (agora %v)", i, quando, anterior, agora)
		}
		anterior = quando
	}
}

func TestCronPreviewRefusesWhatTheFormWouldRefuse(t *testing.T) {
	a := newAPI(t)
	sessao := tokenDe(t, a, a.userID)
	sp := "America/Sao_Paulo"
	casos := []struct {
		nome, expr, fuso, campo string
	}{
		{"sem expressão", "", sp, "expr"},
		{"sem fuso", "0 3 * * *", "", "timezone"},
		{"seis campos", "0 0 3 * * *", sp, "expr"},
		{"lixo", "isso não é cron", sp, "expr"},
		{"passo zero", "*/0 * * * *", sp, "expr"},
		{"nunca dispara", "0 0 30 2 *", sp, "expr"},
		{"fuso dentro da expressão", "CRON_TZ=UTC 0 3 * * *", sp, "expr"},
		{"expressão longa", strings.Repeat("1", 101), sp, "expr"},
		{"NUL", "0 3 * * *\x00", sp, "expr"},
		{"fuso inexistente", "0 3 * * *", "Marte/Olympus", "timezone"},
		{"fuso da máquina", "0 3 * * *", "Local", "timezone"},
		{"fuso com caminho", "0 3 * * *", "../../etc/passwd", "timezone"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			esperarVagaNoLimite(a)
			status, corpo := previaDoCron(t, a, sessao, url.Values{"expr": {c.expr}, "timezone": {c.fuso}})
			if status != http.StatusBadRequest || !strings.Contains(corpo, `"field":"`+c.campo+`"`) {
				t.Errorf("%d %s, queria 400 em %s", status, corpo, c.campo)
			}
		})
	}
}

// Parâmetro desconhecido ou repetido é 400 no campo fixo, sem ecoar a chave.
func TestCronPreviewQueryIsStrict(t *testing.T) {
	a := newAPI(t)
	sessao := tokenDe(t, a, a.userID)
	valida := "expr=0+3+*+*+*&timezone=UTC"
	for _, consulta := range []string{valida + "&CANARIO=1", valida + "&expr=1+*+*+*+*", "expr=%zz&timezone=UTC", "expr=a;timezone=UTC"} {
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/cron-preview?"+consulta, sessao, nil)
		if status != http.StatusBadRequest || !strings.Contains(corpo, `"field":"query"`) || strings.Contains(corpo, "CANARIO") {
			t.Errorf("%s: %d %s", consulta, status, corpo)
		}
	}
}

// A prévia não cria nada, nem conta no teto.
func TestCronPreviewCreatesNothing(t *testing.T) {
	a := newAPI(t)
	sessao := tokenDe(t, a, a.userID)
	if status, corpo := previaDoCron(t, a, sessao, url.Values{"expr": {"0 3 * * *"}, "timezone": {"UTC"}}); status != http.StatusOK {
		t.Fatalf("%d %s", status, corpo)
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", sessao, nil)
	if itens := decodificar(t, corpo)["items"].([]any); len(itens) != 0 {
		t.Errorf("a prévia gravou: %s", corpo)
	}
}

// Sem sessão, 401. Isto não prova o registro da rota nem o Authn: o curinga {id}
// também responde 401, e o handler recusa sem usuário. Quem prova os dois é
// TestCronPreviewListsTheNextRunsInTheTimezone, que dá 200 com sessão válida.
func TestCronPreviewWithoutSessionIs401(t *testing.T) {
	a := newAPI(t)
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/cron-preview?expr=0+3+*+*+*&timezone=UTC", "", nil)
	if status != http.StatusUnauthorized {
		t.Errorf("%d %s", status, corpo)
	}
}

// O log mostra o código de quem o mandou, e null nos demais.
func TestEventListShowsTheExitCode(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, token := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+token+"/start", nil)
	pingar(t, a, "/ping/"+token+"/3", nil)

	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/events", dono, nil)
	ev := decodificar(t, corpo)["items"].([]any)
	if len(ev) != 2 {
		t.Fatalf("eventos: %s", corpo)
	}
	falha, inicio := ev[0].(map[string]any), ev[1].(map[string]any)
	if falha["kind"] != "fail" || falha["exit_code"] != float64(3) {
		t.Errorf("falha: %v", falha)
	}
	if v, ok := inicio["exit_code"]; !ok || v != nil {
		t.Errorf("início: %v", inicio)
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
		{"período no teto", map[string]any{"name": "a", "schedule": periodo(31536000)}, true, ""},
		{"período acima", map[string]any{"name": "a", "schedule": periodo(31536001)}, false, "period_sec"},
		{"período com expr", map[string]any{"name": "a", "schedule": map[string]any{"kind": "period", "period_sec": 60, "expr": "* * * * *"}}, false, "schedule"},
		{"cron com período", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "period_sec": 60, "expr": "* * * * *", "timezone": "UTC"}}, false, "schedule"},
		{"cron sem fuso", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "expr": "* * * * *"}}, false, "timezone"},
		{"cron inválido", map[string]any{"name": "a", "schedule": map[string]any{"kind": "cron", "expr": "nunca", "timezone": "UTC"}}, false, "expr"},
		{"tolerância abaixo", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 29}, false, "grace_sec"},
		{"tolerância no piso", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 30}, true, ""},
		{"tolerância no teto", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 31536000}, true, ""},
		{"tolerância acima", map[string]any{"name": "a", "schedule": periodo(60), "grace_sec": 31536001}, false, "grace_sec"},
		{"descrição no teto", map[string]any{"name": "a", "schedule": periodo(60), "description": strings.Repeat("é", 10000)}, true, ""},
		{"descrição acima do teto", map[string]any{"name": "a", "schedule": periodo(60), "description": strings.Repeat("é", 10001)}, false, "description"},
		{"descrição de emojis no teto", map[string]any{"name": "a", "schedule": periodo(60), "description": strings.Repeat("😀", 10000)}, true, ""},
		{"descrição com NUL", map[string]any{"name": "a", "schedule": periodo(60), "description": "a\u0000b"}, false, "description[0]"},
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
	t.Run("PATCH para o período no teto recalcula o prazo", func(t *testing.T) {
		dono := tokenDe(t, a, outroUsuario(t, a))
		id, _ := criarHB(t, a, dono, corpoDePeriodo)
		status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{
			"schedule": periodo(31536000),
		})
		if status != http.StatusOK {
			t.Fatalf("PATCH: %d %s", status, corpo)
		}
		prazo, err := time.Parse(time.RFC3339, decodificar(t, corpo)["deadline_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if want := a.clk.Now().Add(31536000*time.Second + 300*time.Second); !prazo.Equal(want) {
			t.Errorf("prazo %v, queria %v", prazo, want)
		}
	})
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

// notificadorDeTeste guarda o texto de cada aviso, como os canais o enviariam.
type notificadorDeTeste struct {
	mu      sync.Mutex
	assunto []string
	corpo   []string
}

func (n *notificadorDeTeste) Kind() notify.Kind { return notify.KindDiscord }

func (n *notificadorDeTeste) Send(_ context.Context, _ crypto.Secret, ev notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	assunto, corpo := ev.Text()
	n.assunto, n.corpo = append(n.assunto, assunto), append(n.corpo, corpo)
	return nil
}

// entregar roda o dispatcher de verdade sobre a outbox.
func entregar(t *testing.T, a *api) *notificadorDeTeste {
	t.Helper()
	box, err := crypto.NewBox(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	n := &notificadorDeTeste{}
	d := notify.NewDispatcher(a.pool, box, a.clk, prometheus.NewRegistry(), n)
	if _, err := d.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	return n
}

// abertoComAtraso cria um heartbeat, deixa vencer e abre o incidente. Devolve
// um token novo, porque o relógio passa da validade do anterior.
func abertoComAtraso(t *testing.T, a *api) (dono, id, ping string) {
	t.Helper()
	dono = tokenDe(t, a, a.userID)
	id, ping = criarHB(t, a, dono, corpoDePeriodo)
	a.clk.Advance(2 * time.Hour)
	avaliar(t, a)
	dono = tokenDe(t, a, a.userID)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if m := decodificar(t, corpo); m["open_incident_id"] == nil || m["state"] != "late" {
		t.Fatalf("preparo: %s", corpo)
	}
	return dono, id, ping
}

// Reenviar o que já está gravado não muda nada: nem adia o prazo, nem fecha o
// incidente, nem manda aviso de volta sem a tarefa ter dado sinal.
func TestResendingTheSameScheduleKeepsTheDeadlineAndTheIncident(t *testing.T) {
	a := newAPI(t)
	dono, id, _ := abertoComAtraso(t, a)
	_, antes := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	prazo := decodificar(t, antes)["deadline_at"]

	a.clk.Advance(time.Minute)
	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{
		"schedule": map[string]any{"kind": "period", "period_sec": 3600}, "grace_sec": 300,
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH: %d %s", status, corpo)
	}
	m := decodificar(t, corpo)
	if m["deadline_at"] != prazo {
		t.Errorf("reenviar os mesmos valores moveu o prazo: %v, antes %v", m["deadline_at"], prazo)
	}
	avaliar(t, a)
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if m := decodificar(t, corpo); m["open_incident_id"] == nil || m["state"] != "late" {
		t.Errorf("o incidente não ficou aberto: %s", corpo)
	}
}

// Mudança de verdade move o prazo, mas prazo novo não encerra alarme: o
// incidente fica aberto até um sucesso.
func TestChangingTheScheduleDoesNotCloseTheIncident(t *testing.T) {
	a := newAPI(t)
	dono, id, ping := abertoComAtraso(t, a)
	for _, patch := range []map[string]any{
		{"grace_sec": 600},
		{"schedule": map[string]any{"kind": "period", "period_sec": 7200}},
	} {
		status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, patch)
		if status != http.StatusOK {
			t.Fatalf("PATCH %v: %d %s", patch, status, corpo)
		}
		prazo, err := time.Parse(time.RFC3339, decodificar(t, corpo)["deadline_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !prazo.After(a.clk.Now()) {
			t.Fatalf("o prazo não foi recalculado: %v", prazo)
		}
		avaliar(t, a)
		_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
		if m := decodificar(t, corpo); m["open_incident_id"] == nil || m["state"] != "late" {
			t.Fatalf("depois de %v: %s", patch, corpo)
		}
	}
	pingar(t, a, "/ping/"+ping, nil)
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if decodificar(t, corpo)["open_incident_id"] != nil {
		t.Errorf("o sucesso não fechou o incidente: %s", corpo)
	}
}

// D13: retomar zera a falha e recalcula o prazo, e o avaliador fecha o aberto.
// O aviso diz que foi retomado, porque a tarefa não voltou: nenhum sucesso veio.
func TestResumingClearsTheFailureAndClosesTheIncident(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	criarCanalDiscord(t, a, dono)
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
	if status := decodificar(t, corpo)["state"]; status == "failing" {
		t.Fatalf("retomado: %s", corpo)
	}
	avaliar(t, a)
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	m = decodificar(t, corpo)
	if m["open_incident_id"] != nil || m["state"] != "up" {
		t.Errorf("depois de avaliar: %s", corpo)
	}
	// A abertura de um incidente já fechado não é enviada: sobra o fechamento.
	avisos := entregar(t, a)
	if len(avisos.assunto) != 1 || avisos.assunto[0] != "[StillOn] backup noturno foi retomado" {
		t.Fatalf("avisos: %q", avisos.assunto)
	}
	if !strings.Contains(avisos.corpo[0], "sem sinal de sucesso") || strings.Contains(avisos.corpo[0], "voltou") {
		t.Errorf("fechamento: %q", avisos.corpo[0])
	}
}

// O caminho normal continua dizendo que voltou.
func TestSuccessStillAnnouncesTheReturn(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	criarCanalDiscord(t, a, dono)
	_, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	a.clk.Advance(time.Minute)
	pingar(t, a, "/ping/"+ping, nil)
	avaliar(t, a)
	avisos := entregar(t, a)
	if len(avisos.assunto) != 1 || avisos.assunto[0] != "[StillOn] backup noturno voltou a funcionar" {
		t.Fatalf("avisos: %q", avisos.assunto)
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

// A tarefa nova que manda /start e morre não fica "em dia": segue aguardando, e
// o prazo a pega. Quem tira de waiting é o primeiro sucesso.
func TestStartAloneKeepsTheHeartbeatWaiting(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	if status, _, corpo := pingar(t, a, "/ping/"+ping+"/start", nil); status != http.StatusOK {
		t.Fatalf("%d %s", status, corpo)
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	m := decodificar(t, corpo)
	if m["state"] != "waiting" || m["last_start_at"] == nil || m["last_ping_at"] == nil {
		t.Errorf("depois do início: %s", corpo)
	}
	a.clk.Advance(time.Hour + 5*time.Minute)
	dono = tokenDe(t, a, a.userID) // o relógio avançou além da validade do token
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if e := decodificar(t, corpo)["state"]; e != "late" {
		t.Errorf("no prazo: %s", corpo)
	}
}

// O início descartado na retomada não vira execução terminada: segue aguardando.
func TestDiscardedStartKeepsTheHeartbeatWaiting(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/start", nil)
	lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": false})
	_, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": true})
	m := decodificar(t, corpo)
	if m["state"] != "waiting" || m["last_start_at"] != nil {
		t.Errorf("depois de retomar: %s", corpo)
	}
}

// O início de antes da pausa não vira execução de três dias.
func TestResumingDiscardsTheOpenStart(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping, nil)
	pingar(t, a, "/ping/"+ping+"/start", nil)
	lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": false})

	a.clk.Advance(72 * time.Hour)
	dono = tokenDe(t, a, a.userID)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if decodificar(t, corpo)["last_start_at"] == nil {
		t.Fatalf("o controle falhou: pausar não deveria apagar o início: %s", corpo)
	}
	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"enabled": true})
	if status != http.StatusOK || decodificar(t, corpo)["last_start_at"] != nil {
		t.Fatalf("depois de retomar: %d %s", status, corpo)
	}

	pingar(t, a, "/ping/"+ping, nil)
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	if m := decodificar(t, corpo); m["last_runtime_ms"] != nil {
		t.Errorf("a pausa virou duração: %s", corpo)
	}
}

// Só a retomada descarta: editar o nome com a execução em curso não mexe nela.
func TestEditingKeepsTheOpenStart(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/start", nil)
	_, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id, dono, map[string]any{"name": "outro"})
	if decodificar(t, corpo)["last_start_at"] == nil {
		t.Errorf("editar apagou o início: %s", corpo)
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

// O ping-now fica num teste próprio, como o idor_integration_test.go faz por
// recurso: todas as chamadas saem do mesmo IP e do mesmo intruso, e com ele a
// lista de rotas abaixo passaria do burst por IP (30) e por usuário (20).
func TestAnotherUsersHeartbeatIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents", dono, nil)
	incidente := decodificar(t, corpo)["items"].([]any)[0].(map[string]any)["id"].(string)

	intruso := outroUsuario(t, a)
	molde := "/api/v1/heartbeats/%s"
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, intruso),
		rotas: []rotaAlheia{
			{metodo: http.MethodGet, molde: molde, id: id},
			{metodo: http.MethodPatch, molde: molde, id: id, corpo: map[string]any{"name": "roubado"}},
			{metodo: http.MethodGet, molde: molde + "/events", id: id},
			{metodo: http.MethodGet, molde: molde + "/incidents", id: id},
			{metodo: http.MethodPost, molde: molde + "/token/rotate", id: id},
			{metodo: http.MethodPost, molde: molde + "/clone", id: id},
			{metodo: http.MethodPost, molde: molde + "/events/clear", id: id},
			{metodo: http.MethodPost, molde: molde + "/incidents/" + incidente + "/ack", id: id},
			{metodo: http.MethodDelete, molde: molde, id: id},
		},
		leituras: []string{"/api/v1/heartbeats/" + id, "/api/v1/heartbeats", "/api/v1/heartbeats/" + id + "/incidents",
			"/api/v1/heartbeats/" + id + "/events"},
		listasDoIntruso: []string{"/api/v1/heartbeats"},
		dadosDoDono:     []string{id, "backup noturno"},
	})
	conferirDuplicarELimparSemEfeito(t, a, uuid.MustParse(id), intruso)
	if status, _, corpo := pingar(t, a, "/ping/"+ping, nil); status != http.StatusOK {
		t.Errorf("o token do dono parou: %d %s", status, corpo)
	}
}

// O sinalizar agora alheio responde como o inexistente e não grava nada no
// heartbeat do dono: nem last_ping_at, nem evento.
func TestAnotherUsersPingNowIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, ping := criarHB(t, a, dono, corpoDePeriodo)
	ultimoPing := a.clk.Now()
	pingar(t, a, "/ping/"+ping+"/fail", nil)
	eventosAntes := len(eventosPelaAPI(t, a, dono, id))

	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodPost, molde: "/api/v1/heartbeats/%s/ping-now", id: id},
		},
		leituras:        []string{"/api/v1/heartbeats/" + id, "/api/v1/heartbeats/" + id + "/events"},
		listasDoIntruso: []string{"/api/v1/heartbeats"},
		dadosDoDono:     []string{id, "backup noturno"},
	})
	if h := linhaDoHeartbeat(t, a, uuid.MustParse(id)); h.LastPingAt == nil || !h.LastPingAt.Equal(ultimoPing) {
		t.Errorf("last_ping_at do dono = %s, queria %s", valorDe(h.LastPingAt), ultimoPing)
	}
	if n := len(eventosPelaAPI(t, a, dono, id)); n != eventosAntes {
		t.Errorf("%d eventos depois do intruso, queria %d", n, eventosAntes)
	}
	if status, _, corpo := pingar(t, a, "/ping/"+ping, nil); status != http.StatusOK {
		t.Errorf("o token do dono parou: %d %s", status, corpo)
	}
}

// conferirDuplicarELimparSemEfeito olha o banco depois do intruso, e não só a
// resposta: duplicar não criou cópia para ninguém, limpar não apagou o log do
// dono (o único evento, o fail do cenário) e nada foi auditado em nome do
// intruso sobre a tarefa do dono.
func conferirDuplicarELimparSemEfeito(t *testing.T, a *api, id, intruso uuid.UUID) {
	t.Helper()
	if n := contarHeartbeatsDe(t, a, intruso); n != 0 {
		t.Errorf("o intruso ficou com %d heartbeats, queria 0", n)
	}
	if n := contarHeartbeatsDe(t, a, a.userID); n != 1 {
		t.Errorf("o dono ficou com %d heartbeats, queria 1", n)
	}
	var tipos []string
	for _, e := range eventosDoHeartbeat(t, a, id) {
		tipos = append(tipos, e.Kind)
	}
	if !slices.Equal(tipos, []string{"fail"}) {
		t.Errorf("eventos do dono: %v, queria [fail]", tipos)
	}
	var auditados int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND user_id = $2`, id, intruso).Scan(&auditados); err != nil {
		t.Fatal(err)
	}
	if auditados != 0 {
		t.Errorf("%d linhas de auditoria do intruso sobre a tarefa do dono, queria 0", auditados)
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
	// Duplicar é a terceira rota que emite token de ping.
	status, copia := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/clone", dono, nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicar deu %d: %s", status, copia)
	}
	url, _ := decodificar(t, copia)["ping_url"].(string)
	if strings.Contains(log.String(), url) || strings.Contains(log.String(), tokenDaURL(t, copia)) {
		t.Fatalf("token da cópia no log: %s", log.String())
	}
}
