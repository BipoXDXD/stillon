package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// recursosDeGestao são os recursos cujas escritas pedem mais que member:
// canais e status page (manager) e membros (owner). O {userID} das rotas de
// membros é o titular, e não o próprio member: sair do projeto é dele.
var recursosDeGestao = []string{"channels", "status-pages", "members"}

// O member escreve em alvos, heartbeats e incidentes
// (TestMemberReadsTitularResourcesOnProjectRoutes), mas toda gêmea de escrita
// de canais, status page e membros, e o renomear, lhe dão 403 forbidden. A
// lista vem de rotasGemeas: uma rota de gestão nova entra sozinha. O viewer em
// toda gêmea de escrita é TestViewerGetsForbiddenOnEveryMutatingProjectRoute.
func TestMemberGetsForbiddenOnManagementRoutes(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	r := prepararDono(t, a, dono)
	idDoMember := outroUsuario(t, a)
	testdb.Membro(t, uuid.MustParse(r.projeto), idDoMember, "member")
	member := tokenDe(t, a, idDoMember)
	_, escritas := rotasGemeas(t)
	conferirTabelaCobreAsRotas(t, escritas, corpoDaGemea)

	var testadas []string
	for _, rota := range ordemComDeleteNoFim(escritas) {
		_, molde, _ := strings.Cut(rota, " ")
		recurso, _, _ := strings.Cut(strings.TrimPrefix(molde, prefixoDaGemea), "/")
		if !slices.Contains(recursosDeGestao, recurso) {
			continue
		}
		testadas = append(testadas, rota)
		metodo, _, _ := strings.Cut(rota, " ")
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, metodo, r.caminho(t, rota, r.projeto), member, corpoDaGemea[rota])
		if codigo, _ := erroDe(t, corpo); status != http.StatusForbidden || codigo != "forbidden" {
			t.Errorf("%s do member deu %d, queria 403 forbidden: %s", rota, status, corpo)
		}
	}
	// Canais (4), status page (2) e membros (2) hoje.
	if len(testadas) < 8 {
		t.Errorf("só %d rotas de gestão testadas (%v): a enumeração deixou rotas de fora", len(testadas), testadas)
	}

	esperarVagaNoLimite(a)
	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/projects/"+r.projeto, member, map[string]string{"name": "Do member"})
	if codigo, _ := erroDe(t, corpo); status != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("renomear pelo member deu %d, queria 403 forbidden: %s", status, corpo)
	}
}
