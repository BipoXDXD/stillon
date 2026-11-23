package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

const rotaDeProjetos = "/api/v1/projects"

// criarProjetoHTTP faz o POST e devolve status e corpo.
func criarProjetoHTTP(t *testing.T, a *api, token string, corpo any) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodPost, rotaDeProjetos, token, corpo)
}

func listarProjetos(t *testing.T, a *api, token string) []projetoJSON {
	t.Helper()
	status, corpo := lerCru(t, a, http.MethodGet, rotaDeProjetos, token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s deu %d, queria 200: %s", rotaDeProjetos, status, corpo)
	}
	var lista struct {
		Items []projetoJSON `json:"items"`
	}
	if err := json.Unmarshal([]byte(corpo), &lista); err != nil {
		t.Fatal(err)
	}
	return lista.Items
}

func projetosDoTitular(t *testing.T, titular uuid.UUID) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM projects WHERE titular_id = $1`, titular).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func codigoNoCorpo(t *testing.T, corpo string) string {
	t.Helper()
	erro, _ := decodificar(t, corpo)["error"].(map[string]any)
	codigo, _ := erro["code"].(string)
	return codigo
}

// O projeto criado volta com o mesmo item da lista: owner, fora do padrão. A
// lista passa a trazer os dois, o padrão primeiro, e as rotas do projeto novo
// respondem a quem o criou.
func TestCreateProjectReturns201AndListShowsIt(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	padrao := testdb.ProjetoPadrao(t, a.userID)

	status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": "Clientes"})
	if status != http.StatusCreated {
		t.Fatalf("POST %s deu %d, queria 201: %s", rotaDeProjetos, status, corpo)
	}
	if chaves := chavesDe(decodificar(t, corpo)); !slices.Equal(chaves, []string{"created_at", "id", "is_default", "name", "role"}) {
		t.Errorf("a resposta tem as chaves %v, queria as do item de GET %s", chaves, rotaDeProjetos)
	}
	var criado projetoJSON
	if err := json.Unmarshal([]byte(corpo), &criado); err != nil {
		t.Fatal(err)
	}
	if criado.Name != "Clientes" || criado.Role != "owner" || criado.IsDefault || criado.CreatedAt.IsZero() {
		t.Errorf("projeto criado = %+v, queria Clientes, owner, fora do padrão, com created_at", criado)
	}
	if _, err := uuid.Parse(criado.ID); err != nil {
		t.Fatalf("id %q não é UUID: %v", criado.ID, err)
	}

	lista := listarProjetos(t, a, token)
	if len(lista) != 2 || lista[0].ID != padrao.String() || lista[1] != criado {
		t.Errorf("a lista trouxe %+v, queria o padrão %s e depois %+v", lista, padrao, criado)
	}

	status, corpo = lerCru(t, a, http.MethodGet, rotaDeProjetos+"/"+criado.ID+"/targets", token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET dos alvos do projeto novo deu %d, queria 200: %s", status, corpo)
	}
	if itens, ok := decodificar(t, corpo)["items"].([]any); !ok || len(itens) != 0 {
		t.Errorf("os alvos do projeto novo = %s, queria items vazio", corpo)
	}
}

// O projeto criado por um não existe para outro: não aparece na lista dele, e
// as rotas do projeto dão o mesmo 404 de um projeto que não existe.
func TestCreatedProjectIsInvisibleToAnotherUser(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	status, corpo := criarProjetoHTTP(t, a, dono, map[string]string{"name": "Segredo-do-dono"})
	if status != http.StatusCreated {
		t.Fatalf("POST %s deu %d, queria 201: %s", rotaDeProjetos, status, corpo)
	}
	novo, _ := decodificar(t, corpo)["id"].(string)

	intruso := tokenDe(t, a, outroUsuario(t, a))
	for _, p := range listarProjetos(t, a, intruso) {
		if p.ID == novo || p.Name == "Segredo-do-dono" {
			t.Errorf("o intruso vê o projeto do dono na lista: %+v", p)
		}
	}
	stAlheio, crAlheio := lerCru(t, a, http.MethodGet, rotaDeProjetos+"/"+novo+"/targets", intruso, nil)
	stInexistente, crInexistente := lerCru(t, a, http.MethodGet, rotaDeProjetos+"/"+uuid.NewString()+"/targets", intruso, nil)
	if stAlheio != http.StatusNotFound || stInexistente != http.StatusNotFound {
		t.Fatalf("projeto alheio deu %d, inexistente %d; queria 404 nos dois:\n%s", stAlheio, stInexistente, crAlheio)
	}
	if semTrace(t, crAlheio) != semTrace(t, crInexistente) {
		t.Errorf("o 404 do projeto alheio difere do inexistente:\n%s\n%s", crAlheio, crInexistente)
	}
}

// Nome vazio, só espaço, longo demais ou com caractere invisível: 400 com o
// campo culpado, e nenhum projeto gravado.
func TestCreateProjectRejectsInvalidName(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	for _, c := range []struct {
		caso, nome, motivo string
	}{
		{"vazio", "", "required"},
		{"só espaço", "   ", "required"},
		{"longo demais", strings.Repeat("é", tenancy.MaxProjectNameRunes+1), "too_long"},
		{"caractere invisível", "a\u200bb", "invalid"},
	} {
		t.Run(c.caso, func(t *testing.T) {
			antes := projetosDoTitular(t, a.userID)
			status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": c.nome})
			conferirRecusa(t, status, corpo, []apierr.FieldError{{Field: "name", Reason: c.motivo}})
			if depois := projetosDoTitular(t, a.userID); depois != antes {
				t.Errorf("a recusa gravou projeto: %d → %d", antes, depois)
			}
		})
	}
}

// O titular vem do token: um campo a mais no corpo é 400, e não ignorado.
func TestCreateProjectRejectsUnknownField(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	outro := outroUsuario(t, a)
	status, corpo := criarProjetoHTTP(t, a, token,
		map[string]string{"name": "x", "titular_id": outro.String()})
	if status != http.StatusBadRequest || codigoNoCorpo(t, corpo) != string(apierr.CodeValidation) {
		t.Errorf("campo desconhecido deu %d %s, queria 400 validation_failed", status, corpo)
	}
	if n := projetosDoTitular(t, a.userID); n != 0 {
		t.Errorf("a recusa gravou %d projetos", n)
	}
	if n := projetosDoTitular(t, outro); n != 0 {
		t.Errorf("a recusa gravou %d projetos no titular do corpo", n)
	}
}

// O Projeto padrão conta na cota: com ele e mais dois, o 4º projeto é 409.
func TestCreateProjectLimitIs409(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	testdb.ProjetoPadrao(t, a.userID)
	for i := 2; i <= tenancy.MaxProjectsPerTitular; i++ {
		if status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": "Extra"}); status != http.StatusCreated {
			t.Fatalf("o %dº projeto deu %d, queria 201: %s", i, status, corpo)
		}
	}
	status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": "Um a mais"})
	if status != http.StatusConflict || codigoNoCorpo(t, corpo) != string(apierr.CodeProjectLimit) {
		t.Errorf("o 4º projeto deu %d %s, queria 409 project_limit_reached", status, corpo)
	}
	if n := projetosDoTitular(t, a.userID); n != tenancy.MaxProjectsPerTitular {
		t.Errorf("o titular tem %d projetos, queria %d", n, tenancy.MaxProjectsPerTitular)
	}
}

// A criação tem balde próprio, mais apertado que o comum: com o relógio
// parado, cinco POSTs passam do limitador (os que estouram a cota dão 409,
// não 429), o sexto dá 429. O balde comum continua com folga: a lista passa.
func TestCreateProjectHasOwnBucket(t *testing.T) {
	a := newAPI(t)
	token := tokenDe(t, a, a.userID)
	testdb.ProjetoPadrao(t, a.userID)
	for i := 1; i <= 5; i++ {
		status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": "Rajada"})
		if status == http.StatusTooManyRequests {
			t.Fatalf("o %dº POST deu 429 dentro da rajada: %s", i, corpo)
		}
		if status != http.StatusCreated && status != http.StatusConflict {
			t.Fatalf("o %dº POST deu %d, queria 201 ou 409: %s", i, status, corpo)
		}
	}
	status, corpo := criarProjetoHTTP(t, a, token, map[string]string{"name": "Rajada"})
	if status != http.StatusTooManyRequests || codigoNoCorpo(t, corpo) != string(apierr.CodeRateLimited) {
		t.Errorf("o 6º POST deu %d %s, queria 429 rate_limited", status, corpo)
	}
	if status, corpo := lerCru(t, a, http.MethodGet, rotaDeProjetos, token, nil); status != http.StatusOK {
		t.Errorf("GET %s depois da rajada deu %d, queria 200: %s", rotaDeProjetos, status, corpo)
	}
}
