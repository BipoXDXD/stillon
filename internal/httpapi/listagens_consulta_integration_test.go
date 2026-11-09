package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// O outro lado da allowlist, pelo Routes de verdade: cada listagem segue
// aceitando todos os seus parâmetros juntos, e o repetido não é recusado
// (como no resumo; só a prévia do cron recusa).
func TestListingsAcceptTheirOwnParameters(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	id, _ := criarHB(t, a, dono, corpoDePeriodo)
	pagina := "limit=5&cursor=" + uuid.NewString()
	for _, rota := range []struct{ caminho, filtros string }{
		{"/api/v1/targets", ""},
		{"/api/v1/incidents", "status=open&target_id=" + uuid.NewString() + "&"},
		{"/api/v1/heartbeats", ""},
		{"/api/v1/heartbeats/" + id + "/events", ""},
		{"/api/v1/heartbeats/" + id + "/incidents", "status=resolved&"},
	} {
		for _, consulta := range []string{rota.filtros + pagina, "limit=5&limit=6"} {
			status, corpo := lerCru(t, a, http.MethodGet, rota.caminho+"?"+consulta, dono, nil)
			if status != http.StatusOK {
				t.Errorf("%s?%s deu %d: %s", rota.caminho, consulta, status, corpo)
			}
		}
	}
}
