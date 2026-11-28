package httpapi_test

import (
	"slices"
	"strings"
	"testing"
)

// prefixoDaGemea é o começo de toda rota gêmea: a mesma rota de recurso, com
// o projeto lido do caminho no lugar do Projeto padrão.
const prefixoDaGemea = "/api/v1/projects/{projectID}/"

// recursosDeProjeto são os recursos que vivem num projeto. Toda rota antiga
// deles tem a gêmea por projeto, e toda gêmea tem a rota antiga.
var recursosDeProjeto = []string{"targets", "incidents", "heartbeats", "channels", "status-pages"}

// recursosSoDoProjeto nasceram com o projeto e não têm rota antiga: a equipe
// é de um projeto, e o Projeto padrão não tem por que ser exceção.
var recursosSoDoProjeto = []string{"members"}

// TestEveryLegacyResourceRouteHasAProjectTwin lê as rotas registradas nos
// fontes e exige o par: METODO /api/v1/<recurso>/resto e METODO
// /api/v1/projects/{projectID}/<recurso>/resto. Uma rota nova registrada só de
// um lado (a antiga sem a gêmea, ou o contrário) falha aqui. Uma rota que só
// exista por projeto (membros, convites) terá de entrar aqui de propósito.
func TestEveryLegacyResourceRouteHasAProjectTwin(t *testing.T) {
	var legadas, gemeas []string
	for _, rota := range rotasRegistradas(t) {
		metodo, caminho, _ := strings.Cut(rota, " ")
		if resto, ok := strings.CutPrefix(caminho, prefixoDaGemea); ok {
			if recurso, _, _ := strings.Cut(resto, "/"); !slices.Contains(recursosSoDoProjeto, recurso) {
				gemeas = append(gemeas, metodo+" "+resto)
			}
			continue
		}
		resto, ok := strings.CutPrefix(caminho, "/api/v1/")
		if !ok {
			continue
		}
		recurso, _, _ := strings.Cut(resto, "/")
		if slices.Contains(recursosDeProjeto, recurso) {
			legadas = append(legadas, metodo+" "+resto)
		}
	}
	if len(legadas) == 0 || len(gemeas) == 0 {
		t.Fatalf("achei %d rotas antigas e %d gêmeas; a enumeração deixou de enxergá-las", len(legadas), len(gemeas))
	}
	for _, rota := range legadas {
		if !slices.Contains(gemeas, rota) {
			metodo, resto, _ := strings.Cut(rota, " ")
			t.Errorf("%s /api/v1/%s não tem a gêmea %s %s%s", metodo, resto, metodo, prefixoDaGemea, resto)
		}
	}
	for _, rota := range gemeas {
		if !slices.Contains(legadas, rota) {
			metodo, resto, _ := strings.Cut(rota, " ")
			t.Errorf("%s %s%s não tem a rota antiga %s /api/v1/%s", metodo, prefixoDaGemea, resto, metodo, resto)
		}
	}
}
