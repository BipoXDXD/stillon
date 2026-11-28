package httpapi_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// equipeHTTP é o Projeto padrão do usuário da API (titular), com um manager,
// um member e um viewer, cada um com o seu token.
type equipeHTTP struct {
	projeto                              string
	titular, manager, membro, leitor     uuid.UUID
	tTitular, tManager, tMembro, tLeitor string
}

func novaEquipeHTTP(t *testing.T, a *api) equipeHTTP {
	t.Helper()
	e := equipeHTTP{titular: a.userID, manager: outroUsuario(t, a), membro: outroUsuario(t, a), leitor: outroUsuario(t, a)}
	p := testdb.ProjetoPadrao(t, a.userID)
	e.projeto = p.String()
	testdb.Membro(t, p, e.manager, "manager")
	testdb.Membro(t, p, e.membro, "member")
	testdb.Membro(t, p, e.leitor, "viewer")
	e.tTitular, e.tManager = tokenDe(t, a, e.titular), tokenDe(t, a, e.manager)
	e.tMembro, e.tLeitor = tokenDe(t, a, e.membro), tokenDe(t, a, e.leitor)
	return e
}

func (e equipeHTTP) membros() string { return "/api/v1/projects/" + e.projeto + "/members" }
func (e equipeHTTP) vinculo(id uuid.UUID) string {
	return e.membros() + "/" + id.String()
}

// erroDe devolve o código do envelope de erro e os campos do 400.
func erroDe(t *testing.T, corpo string) (codigo string, campos []string) {
	t.Helper()
	erro, _ := decodificar(t, corpo)["error"].(map[string]any)
	codigo, _ = erro["code"].(string)
	lista, _ := erro["fields"].([]any)
	for _, f := range lista {
		m, _ := f.(map[string]any)
		campos = append(campos, m["field"].(string)+":"+m["reason"].(string))
	}
	return codigo, campos
}

type casoDeRota struct {
	nome         string
	metodo, rota string
	token        string
	corpo        any
	status       int
	codigo       string
	campos       []string
}

func conferirCasos(t *testing.T, a *api, casos []casoDeRota) {
	t.Helper()
	for _, c := range casos {
		esperarVagaNoLimite(a)
		status, corpo := lerCru(t, a, c.metodo, c.rota, c.token, c.corpo)
		if status != c.status {
			t.Errorf("%s: %s %s deu %d, queria %d: %s", c.nome, c.metodo, c.rota, status, c.status, corpo)
			continue
		}
		if c.codigo == "" {
			continue
		}
		codigo, campos := erroDe(t, corpo)
		if codigo != c.codigo || !slices.Equal(campos, c.campos) {
			t.Errorf("%s: erro %s %v, queria %s %v: %s", c.nome, codigo, campos, c.codigo, c.campos, corpo)
		}
	}
}

func TestListMembersRoute(t *testing.T) {
	a := newAPI(t)
	e := novaEquipeHTTP(t, a)

	status, corpo := lerCru(t, a, http.MethodGet, e.membros(), e.tLeitor, nil)
	if status != http.StatusOK {
		t.Fatalf("GET members do viewer deu %d, queria 200: %s", status, corpo)
	}
	envelope := decodificar(t, corpo)
	if chaves := chavesDe(envelope); !slices.Equal(chaves, []string{"items"}) {
		t.Errorf("o envelope tem as chaves %v, queria só items", chaves)
	}
	itens, _ := envelope["items"].([]any)
	quer := []struct {
		id      uuid.UUID
		papel   string
		titular bool
	}{{e.titular, "owner", true}, {e.manager, "manager", false}, {e.membro, "member", false}, {e.leitor, "viewer", false}}
	if len(itens) != len(quer) {
		t.Fatalf("a lista trouxe %d membros, queria %d: %s", len(itens), len(quer), corpo)
	}
	for i, q := range quer {
		m, _ := itens[i].(map[string]any)
		if chaves := chavesDe(m); !slices.Equal(chaves, []string{"email", "is_titular", "joined_at", "role", "user_id"}) {
			t.Errorf("o item %d tem as chaves %v", i, chaves)
		}
		if m["user_id"] != q.id.String() || m["role"] != q.papel || m["is_titular"] != q.titular {
			t.Errorf("item %d = %v, queria %s %s titular=%v", i, m, q.id, q.papel, q.titular)
		}
	}
}

func TestChangeMemberRoleRoute(t *testing.T) {
	a := newAPI(t)
	e := novaEquipeHTTP(t, a)

	status, corpo := lerCru(t, a, http.MethodPatch, e.vinculo(e.membro), e.tTitular, map[string]string{"role": "viewer"})
	if status != http.StatusOK {
		t.Fatalf("PATCH do papel pelo owner deu %d, queria 200: %s", status, corpo)
	}
	item := decodificar(t, corpo)
	if item["user_id"] != e.membro.String() || item["role"] != "viewer" || item["is_titular"] != false {
		t.Errorf("o item devolvido = %v, queria o member agora viewer", item)
	}

	conferirCasos(t, a, []casoDeRota{
		{"papel desconhecido", http.MethodPatch, e.vinculo(e.membro), e.tTitular, map[string]string{"role": "admin"},
			http.StatusBadRequest, "validation_failed", []string{"role:invalid"}},
		{"sem papel", http.MethodPatch, e.vinculo(e.membro), e.tTitular, map[string]any{},
			http.StatusBadRequest, "validation_failed", []string{"role:invalid"}},
		{"manager muda papel", http.MethodPatch, e.vinculo(e.leitor), e.tManager, map[string]string{"role": "member"},
			http.StatusForbidden, "forbidden", nil},
		{"alvo que não é membro", http.MethodPatch, e.vinculo(uuid.New()), e.tTitular, map[string]string{"role": "member"},
			http.StatusNotFound, "not_found", nil},
		{"alvo malformado", http.MethodPatch, e.membros() + "/nao-e-uuid", e.tTitular, map[string]string{"role": "member"},
			http.StatusNotFound, "not_found", nil},
		{"rebaixar o titular", http.MethodPatch, e.vinculo(e.titular), e.tTitular, map[string]string{"role": "manager"},
			http.StatusConflict, "titular_protected", nil},
	})
}

func TestRemoveMemberRoute(t *testing.T) {
	a := newAPI(t)
	e := novaEquipeHTTP(t, a)

	conferirCasos(t, a, []casoDeRota{
		{"manager remove", http.MethodDelete, e.vinculo(e.leitor), e.tManager, nil, http.StatusForbidden, "forbidden", nil},
		{"titular sai", http.MethodDelete, e.vinculo(e.titular), e.tTitular, nil, http.StatusConflict, "titular_protected", nil},
		{"alvo que não é membro", http.MethodDelete, e.vinculo(uuid.New()), e.tTitular, nil, http.StatusNotFound, "not_found", nil},
		{"owner remove o viewer", http.MethodDelete, e.vinculo(e.leitor), e.tTitular, nil, http.StatusNoContent, "", nil},
		{"member sai", http.MethodDelete, e.vinculo(e.membro), e.tMembro, nil, http.StatusNoContent, "", nil},
		// Quem saiu, ou foi removido, já não vê o projeto: o mesmo 404.
		{"removido lista", http.MethodGet, e.membros(), e.tLeitor, nil, http.StatusNotFound, "not_found", nil},
		{"quem saiu lista", http.MethodGet, e.membros(), e.tMembro, nil, http.StatusNotFound, "not_found", nil},
	})
}

func TestRenameProjectRoute(t *testing.T) {
	a := newAPI(t)
	e := novaEquipeHTTP(t, a)
	projeto := "/api/v1/projects/" + e.projeto

	status, corpo := lerCru(t, a, http.MethodPatch, projeto, e.tTitular, map[string]string{"name": "Renomeado"})
	if status != http.StatusOK {
		t.Fatalf("PATCH do projeto pelo owner deu %d, queria 200: %s", status, corpo)
	}
	item := decodificar(t, corpo)
	if chaves := chavesDe(item); !slices.Equal(chaves, []string{"created_at", "id", "is_default", "limits", "name", "role"}) {
		t.Errorf("o item tem as chaves %v, queria as de GET /api/v1/projects", chaves)
	}
	if item["id"] != e.projeto || item["name"] != "Renomeado" || item["is_default"] != true || item["role"] != "owner" {
		t.Errorf("o item devolvido = %v", item)
	}
	// Os tetos são os do titular, iguais aos que a lista traz para o projeto.
	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/projects", e.tTitular, nil)
	itens, _ := decodificar(t, lista)["items"].([]any)
	var daLista map[string]any
	for _, x := range itens {
		if m, _ := x.(map[string]any); m["id"] == e.projeto {
			daLista = m
		}
	}
	limites, _ := item["limits"].(map[string]any)
	if daLista == nil || !reflect.DeepEqual(limites, daLista["limits"]) || limites["projects"] == float64(0) {
		t.Errorf("limits do renomear = %v, queria os da lista %v (não zerados)", limites, daLista["limits"])
	}

	conferirCasos(t, a, []casoDeRota{
		{"nome vazio", http.MethodPatch, projeto, e.tTitular, map[string]string{"name": " "},
			http.StatusBadRequest, "validation_failed", []string{"name:required"}},
		{"nome longo", http.MethodPatch, projeto, e.tTitular, map[string]string{"name": strings.Repeat("a", 101)},
			http.StatusBadRequest, "validation_failed", []string{"name:too_long"}},
		{"manager renomeia", http.MethodPatch, projeto, e.tManager, map[string]string{"name": "Do manager"},
			http.StatusForbidden, "forbidden", nil},
		{"viewer renomeia", http.MethodPatch, projeto, e.tLeitor, map[string]string{"name": "Do viewer"},
			http.StatusForbidden, "forbidden", nil},
	})
}

// Um não membro recebe nas rotas de membros e no renomear o mesmo 404, byte a
// byte (fora o trace_id), que receberia de um projeto que não existe: nada diz
// que o projeto existe nem quem está nele.
func TestAnotherProjectsMembersIndistinguishableFromMissing(t *testing.T) {
	a := newAPI(t)
	e := novaEquipeHTTP(t, a)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	for _, c := range []struct {
		metodo, resto string
		corpo         any
	}{
		{http.MethodGet, "/members", nil},
		{http.MethodPatch, "/members/" + e.membro.String(), map[string]string{"role": "viewer"}},
		{http.MethodDelete, "/members/" + e.leitor.String(), nil},
		{http.MethodPatch, "", map[string]string{"name": "Invadido"}},
	} {
		esperarVagaNoLimite(a)
		stAlheio, crAlheio := lerCru(t, a, c.metodo, "/api/v1/projects/"+e.projeto+c.resto, intruso, c.corpo)
		esperarVagaNoLimite(a)
		stNada, crNada := lerCru(t, a, c.metodo, "/api/v1/projects/"+uuid.NewString()+c.resto, intruso, c.corpo)
		if stAlheio != http.StatusNotFound || stNada != http.StatusNotFound {
			t.Errorf("%s %s: alheio %d, inexistente %d; queria 404 nos dois: %s", c.metodo, c.resto, stAlheio, stNada, crAlheio)
			continue
		}
		if semTrace(t, crAlheio) != semTrace(t, crNada) {
			t.Errorf("%s %s: o 404 alheio difere do inexistente:\n%s\n%s", c.metodo, c.resto, crAlheio, crNada)
		}
		conferirSemDadosDoDono(t, c.metodo+" "+c.resto, crAlheio, []string{e.membro.String(), e.leitor.String(), "Projeto padrão"})
	}
	status, corpo := lerCru(t, a, http.MethodGet, e.membros(), e.tTitular, nil)
	if status != http.StatusOK || !strings.Contains(corpo, e.leitor.String()) || strings.Contains(corpo, "Invadido") {
		t.Errorf("o intruso mudou a equipe: %d %s", status, corpo)
	}
}
