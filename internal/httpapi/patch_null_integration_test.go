package httpapi_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// recursoDePatch descreve um recurso com PATCH. Depois de um PATCH recusado,
// a leitura tem de devolver o mesmo texto de antes. A página nasce publicada
// para que um null lido como false apareça na comparação, como nos enabled.
type recursoDePatch struct {
	nome         string
	rota         string
	leitura      func(id string) string
	corpo        func(t *testing.T, a *api, dono string) map[string]any
	obrigatorios []string
	// mudancaJunto é uma mudança válida que vai com o null do campo: sem ela,
	// um null ignorado pareceria um PATCH vazio.
	mudancaJunto func(campo string) map[string]any
}

func mudarTags(string) map[string]any { return map[string]any{"tags": []string{"nova"}} }

var (
	alvoDePatch = recursoDePatch{
		nome: "alvo", rota: "/api/v1/targets",
		leitura: func(id string) string { return "/api/v1/targets/" + id },
		corpo: func(*testing.T, *api, string) map[string]any {
			return map[string]any{"name": "antes", "url": "https://exemplo.com"}
		},
		obrigatorios: []string{"name", "url", "method", "interval_sec", "timeout_ms", "expected_status", "enabled"},
		mudancaJunto: mudarTags,
	}
	heartbeatDePatch = recursoDePatch{
		nome: "heartbeat", rota: "/api/v1/heartbeats",
		leitura:      func(id string) string { return "/api/v1/heartbeats/" + id },
		corpo:        func(*testing.T, *api, string) map[string]any { return corpoDePeriodo },
		obrigatorios: []string{"name", "schedule", "grace_sec", "enabled"},
		mudancaJunto: mudarTags,
	}
	paginaDePatch = recursoDePatch{
		nome: "pagina", rota: "/api/v1/status-pages",
		leitura: func(string) string { return "/api/v1/status-pages" },
		corpo: func(t *testing.T, a *api, dono string) map[string]any {
			t.Helper()
			status, alvo := lerCru(t, a, http.MethodPost, "/api/v1/targets", dono, alvoValido)
			if status != http.StatusCreated {
				t.Fatalf("criar alvo deu %d: %s", status, alvo)
			}
			return map[string]any{
				"slug": "empresa", "title": "Antes", "published": true,
				"items": []map[string]any{{"target_id": decodificar(t, alvo)["id"], "display_name": "API"}},
			}
		},
		obrigatorios: []string{"slug", "title", "published", "items"},
		mudancaJunto: func(campo string) map[string]any {
			if campo == "title" {
				return map[string]any{"slug": "outra"}
			}
			return map[string]any{"title": "Depois"}
		},
	}
	recursosDePatch = []recursoDePatch{alvoDePatch, heartbeatDePatch, paginaDePatch}
)

// criarParaPatch cria o recurso e devolve a rota do PATCH e uma função que
// confere se a leitura continua igual à de agora.
func criarParaPatch(t *testing.T, a *api, dono string, r recursoDePatch) (string, func()) {
	t.Helper()
	status, criado := lerCru(t, a, http.MethodPost, r.rota, dono, r.corpo(t, a, dono))
	if status != http.StatusCreated {
		t.Fatalf("criar deu %d: %s", status, criado)
	}
	id := decodificar(t, criado)["id"].(string)
	_, antes := lerCru(t, a, http.MethodGet, r.leitura(id), dono, nil)
	continuaIgual := func() {
		t.Helper()
		if _, depois := lerCru(t, a, http.MethodGet, r.leitura(id), dono, nil); depois != antes {
			t.Errorf("o recurso mudou:\n%s\n%s", antes, depois)
		}
	}
	return r.rota + "/" + id, continuaIgual
}

func conferirRecusa(t *testing.T, status int, resp string, querCampos []apierr.FieldError) {
	t.Helper()
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
}

// Null em campo obrigatório não é "campo ausente": é pedir um recurso sem o
// campo. O 400 diz qual, e nada é gravado, nem o que veio junto.
func TestPatchWithNullInRequiredFieldIsRefusedAndChangesNothing(t *testing.T) {
	for _, r := range recursosDePatch {
		for _, campo := range r.obrigatorios {
			t.Run(r.nome+"/"+campo, func(t *testing.T) {
				a := newAPI(t)
				dono := tokenDe(t, a, a.userID)
				caminho, continuaIgual := criarParaPatch(t, a, dono, r)

				corpo := r.mudancaJunto(campo)
				corpo[campo] = nil
				status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, corpo)

				conferirRecusa(t, status, resp, []apierr.FieldError{{Field: campo, Reason: "required"}})
				continuaIgual()
			})
		}
	}
}

// Corrigir um campo por vez não custa uma ida ao servidor por campo.
func TestPatchWithSeveralNullsReportsThemAll(t *testing.T) {
	for _, r := range recursosDePatch {
		t.Run(r.nome, func(t *testing.T) {
			a := newAPI(t)
			dono := tokenDe(t, a, a.userID)
			caminho, continuaIgual := criarParaPatch(t, a, dono, r)

			corpo := map[string]any{}
			quer := make([]apierr.FieldError, 0, len(r.obrigatorios))
			for _, campo := range r.obrigatorios {
				corpo[campo] = nil
				quer = append(quer, apierr.FieldError{Field: campo, Reason: "required"})
			}
			status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, corpo)

			conferirRecusa(t, status, resp, quer)
			continuaIgual()
		})
	}
}

// A recusa vem antes de buscar o recurso: é a mesma para o alheio e para o
// inexistente, e não conta qual dos dois é.
func TestPatchWithNullDoesNotRevealWhetherTheResourceExists(t *testing.T) {
	for _, r := range recursosDePatch {
		t.Run(r.nome, func(t *testing.T) {
			a := newAPI(t)
			dono := tokenDe(t, a, a.userID)
			caminho, continuaIgual := criarParaPatch(t, a, dono, r)
			intruso := tokenDe(t, a, outroUsuario(t, a))
			campo := r.obrigatorios[0]
			quer := []apierr.FieldError{{Field: campo, Reason: "required"}}

			status, resp := lerCru(t, a, http.MethodPatch, caminho, intruso, map[string]any{campo: nil})
			conferirRecusa(t, status, resp, quer)
			inexistente := r.rota + "/00000000-0000-7000-8000-000000000000"
			status, resp = lerCru(t, a, http.MethodPatch, inexistente, intruso, map[string]any{campo: nil})
			conferirRecusa(t, status, resp, quer)

			continuaIgual()
		})
	}
}

// optional decodifica o valor à parte, fora do decoder estrito do corpo. Em
// schedule e items, que têm campos próprios, um nome errado continua sendo 400.
// O mesmo corpo sem o nome errado dá 200: a recusa é por causa dele.
func TestPatchKeepsRejectingUnknownFieldsInsideOptionalObjects(t *testing.T) {
	casos := []struct {
		recurso recursoDePatch
		corpo   func(t *testing.T, a *api, dono string, interno map[string]any) map[string]any
	}{
		{heartbeatDePatch, func(_ *testing.T, _ *api, _ string, interno map[string]any) map[string]any {
			interno["kind"], interno["period_sec"] = "period", 60
			return map[string]any{"schedule": interno}
		}},
		{paginaDePatch, func(t *testing.T, a *api, dono string, interno map[string]any) map[string]any {
			t.Helper()
			interno["target_id"], interno["display_name"] = paginasDe(t, a, dono)[0].Items[0].TargetID, "API"
			return map[string]any{"items": []map[string]any{interno}}
		}},
	}
	for _, c := range casos {
		t.Run(c.recurso.nome, func(t *testing.T) {
			a := newAPI(t)
			dono := tokenDe(t, a, a.userID)
			caminho, continuaIgual := criarParaPatch(t, a, dono, c.recurso)

			corpo := c.corpo(t, a, dono, map[string]any{"extra": 1})
			status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, corpo)

			if status != http.StatusBadRequest || codigoDoEnvelope(t, resp) != string(apierr.CodeValidation) {
				t.Errorf("%d %s, queria 400 validation_failed", status, resp)
			}
			continuaIgual()

			corpo = c.corpo(t, a, dono, map[string]any{})
			if status, resp := lerCru(t, a, http.MethodPatch, caminho, dono, corpo); status != http.StatusOK {
				t.Errorf("sem o campo desconhecido deu %d %s, queria 200", status, resp)
			}
		})
	}
}
