package httpapi_test

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Ações sobre uma tarefa que já existe (SP1, PR 2): duplicar.

func clonar(t *testing.T, a *api, token, id string, corpo any) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+id+"/clone", token, corpo)
}

func contarHeartbeats(t *testing.T, a *api) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		var err error
		n, err = q.CountHeartbeats(ctx, a.userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A cópia leva a configuração (nome com sufixo, descrição, tags, agendamento,
// tolerância, enabled) e nasce sem nada do que o original viveu: token,
// eventos, last_*, falha, incidentes e retomada ficam para trás.
func TestCloneCopiesTheConfigurationAndNotTheState(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, tokenOriginal := criarHB(t, a, dono, map[string]any{
		"name": "backup noturno", "description": "roda às 3h no servidor de dados", "tags": []string{"prod", "backup"},
		"schedule": map[string]any{"kind": "period", "period_sec": 31536000}, "grace_sec": 600,
	})
	for _, sufixo := range []string{"/start", "", "/fail"} {
		if status, _, corpo := pingar(t, a, "/ping/"+tokenOriginal+sufixo, nil); status != http.StatusOK {
			t.Fatalf("ping %q no original: %d %s", sufixo, status, corpo)
		}
	}
	avaliar(t, a)
	if status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+original, dono, map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatalf("pausar o original: %d %s", status, corpo)
	}
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+original, dono, nil)
	antes := decodificar(t, corpo)
	if antes["open_incident_id"] == nil || antes["last_ping_at"] == nil || antes["enabled"] != false {
		t.Fatalf("o original precisa estar pausado, com ping e incidente aberto: %s", corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(original)); n != 3 {
		t.Fatalf("o original tem %d eventos, queria 3", n)
	}

	a.clk.Advance(time.Minute)
	agoraDaCopia := a.clk.Now()
	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicar: %d %s", status, corpo)
	}
	copia := decodificar(t, corpo)
	tokenDaCopia := tokenDaURL(t, corpo)
	id := copia["id"].(string)
	if id == original || tokenDaCopia == tokenOriginal {
		t.Fatalf("a cópia repetiu o id ou o token do original: %s", corpo)
	}

	if copia["name"] != "backup noturno (cópia)" {
		t.Errorf("nome %v", copia["name"])
	}
	if copia["description"] != "roda às 3h no servidor de dados" {
		t.Errorf("descrição %v", copia["description"])
	}
	if got := copia["tags"].([]any); !slices.Equal(got, []any{"backup", "prod"}) {
		t.Errorf("tags %v", got)
	}
	agendamento := copia["schedule"].(map[string]any)
	if agendamento["kind"] != "period" || agendamento["period_sec"] != float64(31536000) {
		t.Errorf("agendamento %v", agendamento)
	}
	if copia["grace_sec"] != float64(600) || copia["enabled"] != false || copia["state"] != "paused" {
		t.Errorf("tolerância %v, enabled %v, estado %v", copia["grace_sec"], copia["enabled"], copia["state"])
	}
	for _, campo := range []string{"last_ping_at", "last_success_at", "last_start_at", "last_runtime_ms", "open_incident_id"} {
		if v, ok := copia[campo]; !ok || v != nil {
			t.Errorf("%s = %v, queria null", campo, v)
		}
	}
	prazo, err := time.Parse(time.RFC3339Nano, copia["deadline_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if quer := agoraDaCopia.Add(31536000*time.Second + 600*time.Second); !prazo.Equal(quer) {
		t.Errorf("prazo %s, queria %s", prazo, quer)
	}

	linha := linhaDoHeartbeat(t, a, uuid.MustParse(id))
	if linha.FailingSince != nil || linha.ResumedAt != nil || linha.LastSignal != nil {
		t.Errorf("a cópia herdou estado: falha %s, retomada %s, último sinal %s",
			valorDe(linha.FailingSince), valorDe(linha.ResumedAt), valorDe(linha.LastSignal))
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 0 {
		t.Errorf("a cópia nasceu com %d eventos", n)
	}
	_, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id+"/incidents", dono, nil)
	if n := len(decodificar(t, corpo)["items"].([]any)); n != 0 {
		t.Errorf("a cópia nasceu com %d incidentes: %s", n, corpo)
	}

	// O token devolvido é da cópia: pinga ela, e o original não muda.
	if status, _, corpo := pingar(t, a, "/ping/"+tokenDaCopia+"/start", nil); status != http.StatusOK {
		t.Fatalf("ping com o token da cópia: %d %s", status, corpo)
	}
	if n := contarEventos(t, a, uuid.MustParse(id)); n != 1 {
		t.Errorf("a cópia tem %d eventos depois do ping, queria 1", n)
	}
	if n := contarEventos(t, a, uuid.MustParse(original)); n != 3 {
		t.Errorf("o ping da cópia chegou ao original: %d eventos, queria 3", n)
	}
}

// A resposta é a da criação: a tarefa e a ping_url, que só sai aqui.
func TestCloneResponseHasExactKeys(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, _ := criarHB(t, a, dono, corpoDePeriodo)
	base := []string{"created_at", "deadline_at", "description", "enabled", "grace_sec", "id", "last_ping_at", "last_runtime_ms", "last_start_at",
		"last_success_at", "name", "open_incident_id", "schedule", "state", "tags"}
	comURL := append(slices.Clone(base), "ping_url")
	sort.Strings(comURL)

	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicar: %d %s", status, corpo)
	}
	copia := decodificar(t, corpo)
	if got := chavesDe(copia); !slices.Equal(got, comURL) {
		t.Errorf("chaves %v, queria %v", got, comURL)
	}
	token := tokenDaURL(t, corpo)
	_, leitura := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+copia["id"].(string), dono, nil)
	if got := chavesDe(decodificar(t, leitura)); !slices.Equal(got, base) {
		t.Errorf("GET da cópia: chaves %v, queria %v", got, base)
	}
	if strings.Contains(leitura, token) {
		t.Errorf("GET da cópia devolveu o token: %s", leitura)
	}
}

// Com 99 tarefas a cópia é a centésima; com 100 é recusada e nada é gravado.
func TestCloneCountsTowardsTheLimit(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	original, _ := criarHB(t, a, dono, corpoDePeriodo)
	encherHeartbeats(t, a, 98)

	if status, corpo := clonar(t, a, dono, original, nil); status != http.StatusCreated {
		t.Fatalf("a centésima: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 100 {
		t.Fatalf("%d tarefas, queria 100", n)
	}
	status, corpo := clonar(t, a, dono, original, nil)
	if status != http.StatusConflict || !strings.Contains(corpo, `"heartbeat_limit_reached"`) {
		t.Errorf("acima do teto: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 100 {
		t.Errorf("%d tarefas depois da recusa, queria 100", n)
	}
}

// A linha de uma versão posterior (piso de rollback): esta não sabe regravar o
// agendamento nem calcular o prazo, então recusa sem gravar.
func TestCloneOfUnknownScheduleConflicts(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)

	status, corpo := clonar(t, a, dono, id.String(), nil)
	if status != http.StatusConflict || !strings.Contains(corpo, `"schedule_unsupported"`) {
		t.Errorf("duplicar agendamento desconhecido: %d %s", status, corpo)
	}
	if n := contarHeartbeats(t, a); n != 1 {
		t.Errorf("%d tarefas depois da recusa, queria 1", n)
	}
}

// Nenhum campo do corpo muda a cópia: nome, estado, dono e enabled vêm do
// original e de quem chamou.
func TestCloneIgnoresTheBody(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	outro := outroUsuario(t, a)
	corpo := map[string]any{
		"name": "backup noturno", "schedule": map[string]any{"kind": "period", "period_sec": 3600}, "grace_sec": 300,
		"enabled": false,
	}
	original, _ := criarHB(t, a, dono, corpo)

	status, resp := clonar(t, a, dono, original, map[string]any{
		"name": "x", "status": "up", "user_id": outro.String(), "enabled": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("duplicar com corpo: %d %s", status, resp)
	}
	copia := decodificar(t, resp)
	if copia["name"] != "backup noturno (cópia)" || copia["enabled"] != false {
		t.Errorf("o corpo mudou a cópia: nome %v, enabled %v", copia["name"], copia["enabled"])
	}
	id := copia["id"].(string)
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil); status != http.StatusOK {
		t.Errorf("a cópia não é de quem chamou: GET deu %d", status)
	}
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", tokenDe(t, a, outro), nil)
	if strings.Contains(lista, id) || strings.Contains(lista, "backup noturno") {
		t.Errorf("a cópia foi para o usuário do corpo: %s", lista)
	}
}
