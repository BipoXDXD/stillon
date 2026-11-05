package httpapi_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type alvoComTags struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func criarAlvoComTags(t *testing.T, a *api, token, nome string, tags ...string) alvoComTags {
	t.Helper()
	status, cru := lerCru(t, a, http.MethodPost, "/api/v1/targets", token, map[string]any{
		"name": nome, "url": "https://exemplo.com/" + nome, "tags": tags,
	})
	if status != http.StatusCreated {
		t.Fatalf("criar alvo: %d %s", status, cru)
	}
	var alvo alvoComTags
	if err := json.Unmarshal([]byte(cru), &alvo); err != nil {
		t.Fatal(err)
	}
	return alvo
}

func nomesDoResumo(t *testing.T, a *api, token, consulta string) []string {
	t.Helper()
	status, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary"+consulta, token, nil)
	if status != http.StatusOK {
		t.Fatalf("resumo %s: %d %s", consulta, status, cru)
	}
	var corpo struct {
		Items []alvoComTags `json:"items"`
	}
	if err := json.Unmarshal([]byte(cru), &corpo); err != nil {
		t.Fatal(err)
	}
	nomes := make([]string, 0, len(corpo.Items))
	for _, it := range corpo.Items {
		nomes = append(nomes, it.Name)
	}
	slices.Sort(nomes)
	return nomes
}

func TestTagsSaemNormalizadasEmTodaLeitura(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	alvo := criarAlvoComTags(t, a, token, "api", "Prod", " prod ", "Backend")
	queria := []string{"backend", "prod"}
	if !slices.Equal(alvo.Tags, queria) {
		t.Errorf("POST devolveu %v, queria %v", alvo.Tags, queria)
	}

	_, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+alvo.ID, token, nil)
	var lido alvoComTags
	if err := json.Unmarshal([]byte(cru), &lido); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lido.Tags, queria) {
		t.Errorf("GET devolveu %v", lido.Tags)
	}

	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets", token, nil)
	if !strings.Contains(cru, `"tags":["backend","prod"]`) {
		t.Errorf("a lista não trouxe as tags: %s", cru)
	}
	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets/summary", token, nil)
	if !strings.Contains(cru, `"tags":["backend","prod"]`) {
		t.Errorf("o resumo não trouxe as tags: %s", cru)
	}
}

// Sem tags é [], nunca null: o painel valida o campo como lista.
func TestAlvoSemTagsSaiComListaVazia(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	status, cru := lerCru(t, a, http.MethodPost, "/api/v1/targets", token, map[string]any{
		"name": "api", "url": "https://exemplo.com/",
	})
	if status != http.StatusCreated || !strings.Contains(cru, `"tags":[]`) {
		t.Fatalf("POST sem tags: %d %s", status, cru)
	}
	_, cru = lerCru(t, a, http.MethodGet, "/api/v1/targets/summary", token, nil)
	if !strings.Contains(cru, `"tags":[]`) {
		t.Errorf("resumo sem tags: %s", cru)
	}
}

// PATCH distingue ausente de null: ausente não mexe; null e [] limpam.
func TestPatchDeTags(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	alvo := criarAlvoComTags(t, a, token, "api", "prod")
	caminho := "/api/v1/targets/" + alvo.ID
	passos := []struct {
		nome   string
		corpo  string
		queria string
	}{
		{"ausente não mexe", `{"name":"novo"}`, `"tags":["prod"]`},
		{"troca", `{"tags":["b","a"]}`, `"tags":["a","b"]`},
		{"lista vazia limpa", `{"tags":[]}`, `"tags":[]`},
		{"volta", `{"tags":["x"]}`, `"tags":["x"]`},
		{"null limpa", `{"tags":null}`, `"tags":[]`},
	}
	for _, p := range passos {
		status, cru := enviarCru(t, a, http.MethodPatch, caminho, token, p.corpo)
		if status != http.StatusOK || !strings.Contains(cru, p.queria) {
			t.Errorf("%s: %d %s", p.nome, status, cru)
		}
		_, lido := lerCru(t, a, http.MethodGet, caminho, token, nil)
		if !strings.Contains(lido, p.queria) {
			t.Errorf("%s: não persistiu: %s", p.nome, lido)
		}
	}
}

// Tag inválida é 400, nunca 500, e não muda nada.
func TestTagsInvalidasNaEscrita(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	alvo := criarAlvoComTags(t, a, token, "api", "prod")
	cases := []struct{ nome, tags, motivo string }{
		{"NUL", `["a\u0000b"]`, "invalid"},
		{"espaço", `["a b"]`, "invalid"},
		{"injeção", `["'; DROP TABLE x;--"]`, "invalid"},
		{"longa", `["` + strings.Repeat("a", 25) + `"]`, "too_long"},
		{"nove", `["a","b","c","d","e","f","g","h","i"]`, "out_of_range"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			status, cru := enviarCru(t, a, http.MethodPatch, "/api/v1/targets/"+alvo.ID, token, `{"tags":`+c.tags+`}`)
			if status != http.StatusBadRequest || !strings.Contains(cru, `"tags"`) || !strings.Contains(cru, c.motivo) {
				t.Errorf("%d %s", status, cru)
			}
			_, lido := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+alvo.ID, token, nil)
			if !strings.Contains(lido, `"tags":["prod"]`) {
				t.Errorf("a recusa mudou as tags: %s", lido)
			}
		})
	}
}

func TestFiltroDoResumoPorTagEEstado(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	criarAlvoComTags(t, a, token, "a", "prod", "api")
	criarAlvoComTags(t, a, token, "b", "prod")
	pausado := criarAlvoComTags(t, a, token, "c", "dev")
	if status, cru := enviarCru(t, a, http.MethodPatch, "/api/v1/targets/"+pausado.ID, token, `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("pausar: %d %s", status, cru)
	}

	cases := []struct {
		consulta string
		queria   []string
	}{
		{"", []string{"a", "b", "c"}},
		{"?tag=prod", []string{"a", "b"}},
		{"?tag=PROD", []string{"a", "b"}},
		{"?tag=prod&tag=api", []string{"a"}},
		{"?tag=prod&tag=dev", []string{}},
		{"?tag=inexistente", []string{}},
		{"?state=paused", []string{"c"}},
		{"?state=unknown", []string{"a", "b"}},
		{"?state=paused&state=unknown", []string{"a", "b", "c"}},
		{"?state=unknown&tag=api", []string{"a"}},
		{"?state=down", []string{}},
	}
	for _, c := range cases {
		if got := nomesDoResumo(t, a, token, c.consulta); !slices.Equal(got, c.queria) {
			t.Errorf("%q: %v; queria %v", c.consulta, got, c.queria)
		}
	}
}

func TestFiltroDoResumoRecusaPorAllowlist(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	cases := []struct{ nome, consulta, campo, motivo string }{
		{"parâmetro desconhecido", "?sort=name", "query", "invalid"},
		// url.Query() descarta o par que não sabe ler, e estes dois devolveriam
		// a lista inteira como se tivessem filtrado.
		{"escape inválido", "?tag=%zz", "query", "invalid"},
		{"ponto e vírgula", "?tag=a;tag=b", "query", "invalid"},
		{"par inválido depois de um válido", "?state=up&tag=%zz", "query", "invalid"},
		{"estado fora da lista", "?state=caido", "state", "invalid"},
		{"estado com injeção", "?state=up%27%20OR%201%3D1", "state", "invalid"},
		{"tag com NUL", "?tag=a%00b", "tag", "invalid"},
		{"tag vazia", "?tag=", "tag", "invalid"},
		{"tag longa", "?tag=" + strings.Repeat("a", 25), "tag", "too_long"},
		{"tags demais", "?tag=a&tag=b&tag=c&tag=d&tag=e&tag=f&tag=g&tag=h&tag=i", "tag", "out_of_range"},
		{"estados demais", "?state=up&state=up&state=up&state=up&state=up&state=up", "state", "out_of_range"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			status, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary"+c.consulta, token, nil)
			if status != http.StatusBadRequest || !strings.Contains(cru, `"`+c.campo+`"`) || !strings.Contains(cru, c.motivo) {
				t.Errorf("%d %s", status, cru)
			}
		})
	}
}

// O nome do parâmetro desconhecido é entrada de quem chama: não volta no
// corpo, ou uma chave de 8 KiB viraria um eco de 8 KiB.
func TestParametroDesconhecidoNaoVoltaNaResposta(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	chave := "CANARIO" + strings.Repeat("x", 2000)
	status, cru := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary?"+chave+"=1", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("%d %s", status, cru)
	}
	if strings.Contains(cru, "CANARIO") {
		t.Errorf("a chave voltou no corpo: %.200s", cru)
	}
}

// Allowlist de saída (Secure APIs 4.6.2): as chaves de um alvo são exatamente
// estas, no POST, no GET e na lista. Um campo novo no DTO tem de passar por aqui.
func TestAlvoTemExatamenteAsChavesDoContrato(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, outroUsuario(t, a))
	queria := []string{
		"created_at", "degraded_threshold_ms", "enabled", "expected_status", "header_names", "id",
		"interval_sec", "method", "name", "tags", "timeout_ms", "url",
	}
	chaves := func(onde, cru string) {
		t.Helper()
		var item map[string]json.RawMessage
		if err := json.Unmarshal([]byte(cru), &item); err != nil {
			t.Fatalf("%s: %v", onde, err)
		}
		if got := slices.Sorted(maps.Keys(item)); !slices.Equal(got, queria) {
			t.Errorf("%s: chaves %v; queria %v", onde, got, queria)
		}
	}
	status, criado := lerCru(t, a, http.MethodPost, "/api/v1/targets", token, map[string]any{
		"name": "api", "url": "https://exemplo.com/", "tags": []string{"prod"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, criado)
	}
	chaves("POST", criado)
	var alvo alvoComTags
	if err := json.Unmarshal([]byte(criado), &alvo); err != nil {
		t.Fatal(err)
	}
	_, lido := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+alvo.ID, token, nil)
	chaves("GET", lido)
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/targets", token, nil)
	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(lista), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("%d itens: %s", len(envelope.Items), lista)
	}
	chaves("lista", string(envelope.Items[0]))
}

// As tags de A não existem para B: nem nas listas, nem como filtro. O filtro
// pela tag de A tem de ser idêntico ao de uma tag que ninguém usa, ou a
// resposta diria que a tag existe em outra conta.
func TestUsuarioBNaoVeTagsDeA(t *testing.T) {
	a := newAPI(t)
	tokenA := tokenDe(t, a, outroUsuario(t, a))
	tokenB := tokenDe(t, a, outroUsuario(t, a))
	alvoA := criarAlvoComTags(t, a, tokenA, "cofre-de-a", "segredo-de-a")
	criarAlvoComTags(t, a, tokenB, "alvo-de-b", "tag-de-b")

	for _, caminho := range []string{"/api/v1/targets", "/api/v1/targets/summary"} {
		_, cru := lerCru(t, a, http.MethodGet, caminho, tokenB, nil)
		conferirSemDadosDoDono(t, "GET "+caminho, cru, []string{"segredo-de-a", "cofre-de-a", alvoA.ID})
	}
	_, comTagAlheia := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary?tag=segredo-de-a", tokenB, nil)
	_, comTagNenhuma := lerCru(t, a, http.MethodGet, "/api/v1/targets/summary?tag=ninguem-usa", tokenB, nil)
	if comTagAlheia != comTagNenhuma || !strings.Contains(comTagAlheia, `"items":[]`) {
		t.Errorf("o filtro pela tag de A difere de uma tag inexistente:\n%s\n%s", comTagAlheia, comTagNenhuma)
	}
	if got := nomesDoResumo(t, a, tokenB, "?tag=tag-de-b"); !slices.Equal(got, []string{"alvo-de-b"}) {
		t.Errorf("B não achou a própria tag: %v", got)
	}
}

// PATCH de tags em alvo alheio é 404, igual a um id inexistente, e as tags do
// dono ficam como estavam.
func TestPatchDeTagsEmAlvoAlheioE404(t *testing.T) {
	a := newAPI(t)
	tokenA := tokenDe(t, a, outroUsuario(t, a))
	tokenB := tokenDe(t, a, outroUsuario(t, a))
	alvoA := criarAlvoComTags(t, a, tokenA, "cofre-de-a", "segredo-de-a")
	conferirIDOR(t, a, cenarioIDOR{
		dono: tokenA, intruso: tokenB,
		rotas: []rotaAlheia{{
			metodo: http.MethodPatch, molde: "/api/v1/targets/%s", id: alvoA.ID,
			corpo: map[string]any{"tags": []string{"invadida"}},
		}},
		leituras:        []string{"/api/v1/targets/" + alvoA.ID},
		listasDoIntruso: []string{"/api/v1/targets", "/api/v1/targets/summary"},
		dadosDoDono:     []string{"segredo-de-a", "cofre-de-a"},
	})
}
