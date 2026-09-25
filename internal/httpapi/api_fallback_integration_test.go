package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// B5 (Plano 10): rota ou método inexistente sob /api/ responde no envelope
// de erro, com trace_id, como todo erro da API (spec §9.1). Antes, o ServeMux
// respondia "404 page not found" e "Method Not Allowed" em texto puro, e o
// painel via "Resposta inesperada do servidor".
func TestUnknownAPIRouteAndMethodAnswerWithTheEnvelope(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	alvo := "/api/v1/targets/" + uuid.NewString()

	cases := []struct {
		nome            string
		metodo, rota    string
		status          int
		codigo, permite string
	}{
		{"rota inexistente", http.MethodGet, "/api/v1/nao-existe", http.StatusNotFound, "not_found", ""},
		{"versão inexistente", http.MethodGet, "/api/v2/targets", http.StatusNotFound, "not_found", ""},
		{"rota inexistente com mutação", http.MethodPost, "/api/v1/nao-existe", http.StatusNotFound, "not_found", ""},
		{"método não registrado", http.MethodPut, alvo, http.StatusMethodNotAllowed, "method_not_allowed", "PATCH"},
		{"método não registrado numa coleção", http.MethodDelete, "/api/v1/targets", http.StatusMethodNotAllowed, "method_not_allowed", "POST"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			resp := chamar(t, a, c.metodo, c.rota, tok, nil)
			if resp.StatusCode != c.status {
				resp.Body.Close()
				t.Fatalf("%s %s deu %d, queria %d", c.metodo, c.rota, resp.StatusCode, c.status)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, queria JSON", ct)
			}
			// lerCorpo usa json.Unmarshal, que recusa o texto do ServeMux
			// grudado depois do envelope.
			var env envelope
			lerCorpo(t, resp, &env)
			if env.Error.Code != c.codigo || env.TraceID == "" {
				t.Errorf("envelope = %+v, queria code %s com trace_id", env, c.codigo)
			}
			// O Allow do ServeMux fica: é o que diz ao cliente o que vale.
			if c.permite != "" && !strings.Contains(resp.Header.Get("Allow"), c.permite) {
				t.Errorf("Allow = %q, queria conter %s", resp.Header.Get("Allow"), c.permite)
			}
		})
	}
}
