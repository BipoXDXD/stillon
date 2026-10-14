package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Spec §5.5, em toda rota com {id} de recurso: o intruso recebe 404 com o
// corpo idêntico (fora o trace_id) ao de um id que não existe, sem nenhum
// dado do dono, e os recursos do dono ficam byte a byte como estavam.
//
// Um teste por recurso, e não um só: todas as chamadas saem do mesmo IP, e o
// limite por IP da API (burst 30) cortaria um teste único no meio.

type rotaAlheia struct {
	metodo, molde, id string // molde tem um %s no lugar do id
	corpo             any
}

type cenarioIDOR struct {
	dono, intruso string
	rotas         []rotaAlheia
	// leituras são os GETs do dono que não podem mudar, e as listas que o
	// intruso lê sem achar nada do dono.
	leituras, listasDoIntruso []string
	dadosDoDono               []string
}

func conferirIDOR(t *testing.T, a *api, c cenarioIDOR) {
	t.Helper()
	antes := map[string]string{}
	for _, caminho := range c.leituras {
		_, antes[caminho] = lerCru(t, a, http.MethodGet, caminho, c.dono, nil)
	}

	for _, r := range c.rotas {
		alheio := strings.Replace(r.molde, "%s", r.id, 1)
		inexistente := strings.Replace(r.molde, "%s", uuid.NewString(), 1)
		stAlheio, crAlheio := lerCru(t, a, r.metodo, alheio, c.intruso, r.corpo)
		stInexistente, crInexistente := lerCru(t, a, r.metodo, inexistente, c.intruso, r.corpo)
		if stAlheio != http.StatusNotFound || stInexistente != http.StatusNotFound {
			t.Errorf("%s %s: alheio deu %d, inexistente %d; queria 404 nos dois", r.metodo, r.molde, stAlheio, stInexistente)
			continue
		}
		if semTrace(t, crAlheio) != semTrace(t, crInexistente) {
			t.Errorf("%s %s: o 404 do alheio difere do inexistente:\n%s\n%s", r.metodo, r.molde, crAlheio, crInexistente)
		}
		conferirSemDadosDoDono(t, r.metodo+" "+r.molde, crAlheio, c.dadosDoDono)
	}

	for _, caminho := range c.listasDoIntruso {
		status, lista := lerCru(t, a, http.MethodGet, caminho, c.intruso, nil)
		if status != http.StatusOK {
			t.Errorf("GET %s do intruso deu %d, queria 200", caminho, status)
		}
		conferirSemDadosDoDono(t, "GET "+caminho+" do intruso", lista, c.dadosDoDono)
	}

	for _, caminho := range c.leituras {
		if _, depois := lerCru(t, a, http.MethodGet, caminho, c.dono, nil); depois != antes[caminho] {
			t.Errorf("GET %s do dono mudou depois do intruso:\nantes  %s\ndepois %s", caminho, antes[caminho], depois)
		}
	}
}

func conferirSemDadosDoDono(t *testing.T, onde, corpo string, dados []string) {
	t.Helper()
	for _, dado := range dados {
		if strings.Contains(corpo, dado) {
			t.Errorf("%s: a resposta trouxe %q, do dono", onde, dado)
		}
	}
}

func TestAnotherUsersTargetIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	alvo, _ := incidentesDe(t, a, dono, 0)
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodGet, molde: "/api/v1/targets/%s", id: alvo},
			{metodo: http.MethodPatch, molde: "/api/v1/targets/%s", id: alvo, corpo: map[string]any{"name": "roubado"}},
			{metodo: http.MethodGet, molde: "/api/v1/targets/%s/checks", id: alvo},
			{metodo: http.MethodDelete, molde: "/api/v1/targets/%s", id: alvo},
		},
		leituras:        []string{"/api/v1/targets/" + alvo, "/api/v1/targets"},
		listasDoIntruso: []string{"/api/v1/targets", "/api/v1/targets/summary"},
		dadosDoDono:     []string{alvo},
	})
}

func TestAnotherUsersChannelIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	canal := criarCanalDiscord(t, a, dono)
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodPatch, molde: "/api/v1/channels/%s", id: canal, corpo: map[string]any{"enabled": false}},
			{metodo: http.MethodPost, molde: "/api/v1/channels/%s/verify/resend", id: canal},
			{metodo: http.MethodDelete, molde: "/api/v1/channels/%s", id: canal},
		},
		leituras:        []string{"/api/v1/channels"},
		listasDoIntruso: []string{"/api/v1/channels"},
		dadosDoDono:     []string{canal, "6789"},
	})
}

func TestAnotherUsersIncidentIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	alvo, incidentes := incidentesDe(t, a, dono, 1)
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodPost, molde: "/api/v1/incidents/%s/ack", id: incidentes[0]},
		},
		leituras:        []string{"/api/v1/incidents"},
		listasDoIntruso: []string{"/api/v1/incidents", "/api/v1/incidents?target_id=" + alvo},
		dadosDoDono:     []string{alvo, incidentes[0]},
	})
}

func TestAnotherUsersStatusPageIsIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	alvo, _ := incidentesDe(t, a, dono, 0)
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/status-pages", dono, map[string]any{
		"slug": "pagina-do-dono", "title": "Titulo-do-dono", "published": true,
		"items": []map[string]string{{"target_id": alvo, "display_name": "Vitrine-do-dono"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("criar status page deu %d: %s", status, corpo)
	}
	var pagina struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(corpo), &pagina); err != nil {
		t.Fatal(err)
	}
	conferirIDOR(t, a, cenarioIDOR{
		dono: dono, intruso: tokenDe(t, a, outroUsuario(t, a)),
		rotas: []rotaAlheia{
			{metodo: http.MethodPatch, molde: "/api/v1/status-pages/%s", id: pagina.ID, corpo: map[string]any{"title": "roubado"}},
		},
		leituras:    []string{"/api/v1/status-pages"},
		dadosDoDono: []string{alvo, pagina.ID, "pagina-do-dono", "Titulo-do-dono", "Vitrine-do-dono"},
	})
}

// Pôr o alvo de outro usuário na própria status page não passa, e a recusa
// não diz se o alvo existe: o alvo alheio e um id inexistente dão o mesmo
// erro. Sem isso, a página pública vazaria o estado do alvo alheio.
func TestStatusPageRefusesAnotherUsersTargetLikeAMissingOne(t *testing.T) {
	a := newAPI(t)
	alvoDoDono, _ := incidentesDe(t, a, tokenDe(t, a, a.userID), 0)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	criar := func(alvo string) (int, string) {
		return lerCru(t, a, http.MethodPost, "/api/v1/status-pages", intruso, map[string]any{
			"slug": "pagina-do-intruso", "title": "T", "published": true,
			"items": []map[string]string{{"target_id": alvo, "display_name": "x"}},
		})
	}
	stAlheio, crAlheio := criar(alvoDoDono)
	stInexistente, crInexistente := criar(uuid.NewString())
	if stAlheio != http.StatusBadRequest || stInexistente != http.StatusBadRequest {
		t.Fatalf("alvo alheio deu %d, inexistente %d; queria 400 nos dois", stAlheio, stInexistente)
	}
	if semTrace(t, crAlheio) != semTrace(t, crInexistente) {
		t.Errorf("a recusa do alvo alheio difere da do inexistente:\n%s\n%s", crAlheio, crInexistente)
	}
	if status, _ := lerCru(t, a, http.MethodGet, "/p/pagina-do-intruso", "", nil); status != http.StatusNotFound {
		t.Errorf("a página do intruso existe depois da recusa (%d)", status)
	}
}

// lerCru faz a chamada e devolve status e corpo inteiro.
func lerCru(t *testing.T, a *api, metodo, caminho, token string, corpo any) (int, string) {
	t.Helper()
	resp := chamar(t, a, metodo, caminho, token, corpo)
	defer resp.Body.Close()
	lido, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(lido)
}

// semTrace tira o trace_id, o único campo que muda entre dois envelopes de
// erro iguais.
func semTrace(t *testing.T, corpo string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(corpo), &m); err != nil {
		t.Fatalf("corpo fora do envelope: %v: %s", err, corpo)
	}
	delete(m, "trace_id")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
