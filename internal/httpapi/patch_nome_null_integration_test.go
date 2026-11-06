package httpapi_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// O nome é obrigatório: null não é "campo ausente", é pedir um recurso sem
// nome. O 400 é o mesmo do nome vazio, e nada é gravado, nem o que veio junto.
func TestPatchWithNullNameIsRefusedAndChangesNothing(t *testing.T) {
	querCampos := []apierr.FieldError{{Field: "name", Reason: "required"}}
	recursos := []struct {
		nome  string
		rota  string
		corpo map[string]any
		extra string
	}{
		{"alvo", "/api/v1/targets", map[string]any{"name": "antes", "url": "https://exemplo.com"}, "interval_sec"},
		{"heartbeat", "/api/v1/heartbeats", corpoDePeriodo, "grace_sec"},
	}
	for _, r := range recursos {
		t.Run(r.nome, func(t *testing.T) {
			a := newAPI(t)
			dono := tokenDe(t, a, a.userID)
			status, criado := lerCru(t, a, http.MethodPost, r.rota, dono, r.corpo)
			if status != http.StatusCreated {
				t.Fatalf("criar deu %d: %s", status, criado)
			}
			caminho := r.rota + "/" + decodificar(t, criado)["id"].(string)
			_, antes := lerCru(t, a, http.MethodGet, caminho, dono, nil)

			status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, map[string]any{"name": nil, r.extra: 600})

			var env struct {
				Error struct {
					Code   string              `json:"code"`
					Fields []apierr.FieldError `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(resp), &env); err != nil {
				t.Fatalf("%v: %s", err, resp)
			}
			if status != http.StatusBadRequest || env.Error.Code != string(apierr.CodeValidation) ||
				!slices.Equal(env.Error.Fields, querCampos) {
				t.Errorf("%d %s, queria 400 com %v", status, resp, querCampos)
			}
			if _, depois := lerCru(t, a, http.MethodGet, caminho, dono, nil); depois != antes {
				t.Errorf("o recurso mudou:\n%s\n%s", antes, depois)
			}
		})
	}
}
