package httpapi_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// prefixoDoHeartbeat é o começo de toda rota que age sobre um heartbeat pelo id.
const prefixoDoHeartbeat = "/api/v1/heartbeats/{id}"

// Spec §5.5: heartbeat de outro usuário é 404, nunca 403, em toda rota que o
// recebe pelo id. A tabela é conferida contra as rotas registradas nos fontes:
// rota nova em /heartbeats/{id} sem linha aqui derruba o teste, em vez de
// ficar sem a conferência de dono.
func TestAnotherUsersHeartbeatIs404OnEveryRoute(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	idDoIntruso := outroUsuario(t, a)
	intruso := tokenDe(t, a, idDoIntruso)

	const (
		nomeDeA      = "CANARY-hb-nome-5d21"
		descricaoDeA = "CANARY-hb-descricao-5d21"
		tagDeA       = "canary-hb-tag-5d21"
	)
	id, ping := criarHB(t, a, dono, map[string]any{
		"name": nomeDeA, "description": descricaoDeA, "tags": []string{tagDeA},
		"schedule": map[string]any{"kind": "period", "period_sec": 31536000}, "grace_sec": 600,
	})
	// Um /fail avaliado abre o incidente que a rota de ack precisa, e deixa
	// eventos no log para a limpeza e o ping-now terem o que mudar.
	pingarOK(t, a, ping, "/start", "/fail")
	avaliar(t, a)
	_, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id, dono, nil)
	incidente, ok := decodificar(t, corpo)["open_incident_id"].(string)
	if !ok {
		t.Fatalf("a preparação precisa de um incidente aberto: %s", corpo)
	}

	corpos := map[string]any{
		"GET " + prefixoDoHeartbeat:                                   nil,
		"PATCH " + prefixoDoHeartbeat:                                 map[string]any{"name": "roubado", "tags": []string{"invadida"}, "enabled": false},
		"POST " + prefixoDoHeartbeat + "/token/rotate":                nil,
		"POST " + prefixoDoHeartbeat + "/clone":                       nil,
		"POST " + prefixoDoHeartbeat + "/ping-now":                    nil,
		"GET " + prefixoDoHeartbeat + "/events":                       nil,
		"POST " + prefixoDoHeartbeat + "/events/clear":                nil,
		"GET " + prefixoDoHeartbeat + "/incidents":                    nil,
		"POST " + prefixoDoHeartbeat + "/incidents/{incident_id}/ack": nil,
		// O DELETE fica por último na volta: se passasse, as outras linhas
		// ainda testariam o heartbeat vivo.
		"DELETE " + prefixoDoHeartbeat: nil,
	}
	rotas := rotasDoHeartbeat(t)
	conferirTabelaCobreAsRotas(t, rotas, corpos)

	leituras := []string{
		"/api/v1/heartbeats/" + id,
		"/api/v1/heartbeats/" + id + "/events",
		"/api/v1/heartbeats/" + id + "/incidents",
	}
	antes := map[string]string{}
	for _, caminho := range leituras {
		status, corpo := lerCru(t, a, http.MethodGet, caminho, dono, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s do dono deu %d: %s", caminho, status, corpo)
		}
		antes[caminho] = corpo
	}
	linhaAntes := linhaDoHeartbeat(t, a, uuid.MustParse(id))

	proibidos := []string{id, incidente, nomeDeA, descricaoDeA, tagDeA, ping}
	for _, rota := range ordemComDeleteNoFim(rotas) {
		metodo, molde, _ := strings.Cut(rota, " ")
		caminho := strings.NewReplacer("{id}", id, "{incident_id}", incidente).Replace(molde)
		status, cru := lerCru(t, a, metodo, caminho, intruso, corpos[rota])
		var env envelopeComCampos
		if err := json.Unmarshal([]byte(cru), &env); err != nil {
			// Errorf, não Fatalf: uma rota que vaza não pode esconder as seguintes.
			t.Errorf("%s pelo intruso deu %d com corpo que não é JSON: %v\n%s", rota, status, err, cru)
			conferirSemDadosDoDono(t, rota, cru, proibidos)
			continue
		}
		if status != http.StatusNotFound || env.Error.Code != "not_found" {
			t.Errorf("%s pelo intruso deu %d/%s, queria 404/not_found", rota, status, env.Error.Code)
		}
		conferirSemDadosDoDono(t, rota, cru, proibidos)
	}

	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats?limit=100", intruso, nil)
	conferirSemDadosDoDono(t, "GET /api/v1/heartbeats do intruso", lista, proibidos)
	if n := contarHeartbeatsDe(t, a, idDoIntruso); n != 0 {
		t.Errorf("o intruso ficou com %d heartbeats, queria 0", n)
	}
	for _, caminho := range leituras {
		if _, depois := lerCru(t, a, http.MethodGet, caminho, dono, nil); depois != antes[caminho] {
			t.Errorf("GET %s do dono mudou depois do intruso:\nantes  %s\ndepois %s", caminho, antes[caminho], depois)
		}
	}
	// A linha inteira, com o hash do token, que a API só mostra na criação e
	// na rotação.
	if linhaDepois := linhaDoHeartbeat(t, a, uuid.MustParse(id)); !reflect.DeepEqual(linhaDepois, linhaAntes) {
		t.Errorf("a linha do heartbeat do dono mudou:\nantes  %+v\ndepois %+v", linhaAntes, linhaDepois)
	}
}

// rotasDoHeartbeat devolve, das rotas registradas, as que recebem o id de um
// heartbeat.
func rotasDoHeartbeat(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, rota := range rotasRegistradas(t) {
		_, caminho, _ := strings.Cut(rota, " ")
		if caminho == prefixoDoHeartbeat || strings.HasPrefix(caminho, prefixoDoHeartbeat+"/") {
			out = append(out, rota)
		}
	}
	return out
}

// conferirTabelaCobreAsRotas exige que a tabela e as rotas registradas sejam o
// mesmo conjunto: rota sem linha escapa da conferência, e linha sem rota é
// tabela velha.
func conferirTabelaCobreAsRotas(t *testing.T, rotas []string, tabela map[string]any) {
	t.Helper()
	for _, rota := range rotas {
		if _, ok := tabela[rota]; !ok {
			t.Errorf("a rota %q não está na tabela deste teste; acrescente a linha dela", rota)
		}
	}
	for rota := range tabela {
		if !slices.Contains(rotas, rota) {
			t.Errorf("a tabela tem %q, que não está registrada nos fontes", rota)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func ordemComDeleteNoFim(rotas []string) []string {
	out := slices.Clone(rotas)
	slices.SortStableFunc(out, func(x, y string) int {
		return boolParaInt(strings.HasPrefix(x, "DELETE ")) - boolParaInt(strings.HasPrefix(y, "DELETE "))
	})
	return out
}

func boolParaInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
