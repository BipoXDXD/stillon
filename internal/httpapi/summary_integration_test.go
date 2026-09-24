package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// GET /targets/summary responde o resumo, e não o 404 de /targets/{id} com
// id "summary"; o item tem exatamente as chaves do contrato, os nulos saem
// null e o spark sai [] (nunca null), e nada de URL nem header.
func TestSummaryRouteReturnsTheContractShape(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	criado := chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido)
	if criado.StatusCode != http.StatusCreated {
		t.Fatalf("POST deu %d: %s", criado.StatusCode, lerCorpo(t, criado, nil))
	}
	criado.Body.Close()

	resp := chamar(t, a, http.MethodGet, "/api/v1/targets/summary", tok, nil)
	var corpo struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	cru := lerCorpo(t, resp, &corpo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /targets/summary deu %d: %s", resp.StatusCode, cru)
	}
	if len(corpo.Items) != 1 {
		t.Fatalf("%d itens, queria 1: %s", len(corpo.Items), cru)
	}
	item := corpo.Items[0]
	chaves := []string{"id", "name", "enabled", "state", "last_checked_at", "last_latency_ms",
		"uptime_24h", "open_incident_id", "spark"}
	if len(item) != len(chaves) {
		t.Errorf("item com %d chaves, queria %d: %s", len(item), len(chaves), cru)
	}
	for _, k := range chaves {
		if _, ok := item[k]; !ok {
			t.Errorf("chave %q faltando", k)
		}
	}
	for _, k := range []string{"last_checked_at", "last_latency_ms", "uptime_24h", "open_incident_id"} {
		if string(item[k]) != "null" {
			t.Errorf("%s = %s, queria null num alvo sem check", k, item[k])
		}
	}
	if string(item["spark"]) != "[]" || string(item["state"]) != `"unknown"` {
		t.Errorf("spark = %s, state = %s; queria [] e \"unknown\"", item["spark"], item["state"])
	}
	if strings.Contains(string(cru), "segredo-do-cliente") || strings.Contains(string(cru), "exemplo.com") {
		t.Errorf("o resumo vazou URL ou header: %s", cru)
	}
}
