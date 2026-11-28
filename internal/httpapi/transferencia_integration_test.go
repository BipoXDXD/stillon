package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// cessaoHTTP é um projeto não padrão do usuário da API (titular), com outro
// owner (quem recebe), um segundo owner, um manager e um estranho que não é
// membro, cada um com o seu token.
type cessaoHTTP struct {
	projeto, padrao                              string
	titular, receptor, outroOwner, manager       uuid.UUID
	tTitular, tReceptor, tOutro, tManager, tFora string
}

func novaCessaoHTTP(t *testing.T, a *api) cessaoHTTP {
	t.Helper()
	c := cessaoHTTP{titular: a.userID, receptor: outroUsuario(t, a), outroOwner: outroUsuario(t, a), manager: outroUsuario(t, a)}
	p := testdb.NovoProjeto(t, a.userID, "Clientes")
	c.projeto, c.padrao = p.String(), testdb.ProjetoPadrao(t, a.userID).String()
	testdb.Membro(t, p, c.receptor, "owner")
	testdb.Membro(t, p, c.outroOwner, "owner")
	testdb.Membro(t, p, c.manager, "manager")
	c.tTitular, c.tReceptor = tokenDe(t, a, c.titular), tokenDe(t, a, c.receptor)
	c.tOutro, c.tManager = tokenDe(t, a, c.outroOwner), tokenDe(t, a, c.manager)
	c.tFora = tokenDe(t, a, outroUsuario(t, a))
	return c
}

func (c cessaoHTTP) rota() string   { return "/api/v1/projects/" + c.projeto + "/titular-transfer" }
func (c cessaoHTTP) aceite() string { return c.rota() + "/accept" }
func para(id uuid.UUID) map[string]string {
	return map[string]string{"user_id": id.String()}
}

// pendenteNaLista diz se GET /api/v1/projects, com o token dado, marca o
// projeto como pending_titular.
func pendenteNaLista(t *testing.T, a *api, token, projeto string) bool {
	t.Helper()
	esperarVagaNoLimite(a)
	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/projects", token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET projects deu %d: %s", status, corpo)
	}
	itens, _ := decodificar(t, corpo)["items"].([]any)
	for _, i := range itens {
		m, _ := i.(map[string]any)
		if m["id"] == projeto {
			pendente, _ := m["pending_titular"].(bool)
			return pendente
		}
	}
	t.Fatalf("o projeto %s não veio na lista", projeto)
	return false
}

func TestProposeTransferRoute(t *testing.T) {
	a := newAPI(t)
	c := novaCessaoHTTP(t, a)

	conferirCasos(t, a, []casoDeRota{
		{"quem não é membro", http.MethodPost, c.rota(), c.tFora, para(c.receptor), http.StatusNotFound, "not_found", nil},
		{"manager propõe", http.MethodPost, c.rota(), c.tManager, para(c.receptor), http.StatusForbidden, "forbidden", nil},
		{"owner que não é o titular", http.MethodPost, c.rota(), c.tOutro, para(c.receptor), http.StatusForbidden, "forbidden", nil},
		{"alvo manager", http.MethodPost, c.rota(), c.tTitular, para(c.manager), http.StatusConflict, "titular_transfer_target", nil},
		{"alvo que não é membro", http.MethodPost, c.rota(), c.tTitular, para(uuid.New()), http.StatusConflict, "titular_transfer_target", nil},
		{"o próprio titular", http.MethodPost, c.rota(), c.tTitular, para(c.titular), http.StatusConflict, "titular_transfer_target", nil},
		{"Projeto padrão", http.MethodPost, "/api/v1/projects/" + c.padrao + "/titular-transfer", c.tTitular, para(c.receptor),
			http.StatusConflict, "titular_transfer_target", nil},
		{"user_id malformado", http.MethodPost, c.rota(), c.tTitular, map[string]string{"user_id": "nao-e-uuid"},
			http.StatusBadRequest, "validation_failed", []string{"user_id:invalid"}},
		{"titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
	})

	if !pendenteNaLista(t, a, c.tReceptor, c.projeto) {
		t.Error("quem recebe não vê pending_titular na lista")
	}
	for nome, token := range map[string]string{"titular": c.tTitular, "outro owner": c.tOutro, "manager": c.tManager} {
		if pendenteNaLista(t, a, token, c.projeto) {
			t.Errorf("o %s vê pending_titular, que é só de quem recebe", nome)
		}
	}
}

func TestAcceptTransferRoute(t *testing.T) {
	a := newAPI(t)
	c := novaCessaoHTTP(t, a)

	conferirCasos(t, a, []casoDeRota{
		{"aceite sem proposta", http.MethodPost, c.aceite(), c.tReceptor, nil, http.StatusForbidden, "forbidden", nil},
		{"titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
		{"quem não é membro aceita", http.MethodPost, c.aceite(), c.tFora, nil, http.StatusNotFound, "not_found", nil},
		{"outro owner aceita", http.MethodPost, c.aceite(), c.tOutro, nil, http.StatusForbidden, "forbidden", nil},
		{"o titular aceita", http.MethodPost, c.aceite(), c.tTitular, nil, http.StatusForbidden, "forbidden", nil},
	})

	esperarVagaNoLimite(a)
	status, corpo := lerCru(t, a, http.MethodPost, c.aceite(), c.tReceptor, nil)
	if status != http.StatusOK {
		t.Fatalf("aceite de quem recebe deu %d, queria 200: %s", status, corpo)
	}
	item := decodificar(t, corpo)
	if item["id"] != c.projeto || item["role"] != "owner" || item["is_default"] != false {
		t.Errorf("o item devolvido = %v, queria o projeto com papel owner", item)
	}
	if _, tem := item["pending_titular"]; tem {
		t.Errorf("o item depois do aceite ainda traz pending_titular: %v", item)
	}

	// O titular antigo é um owner comum: já sai; o novo é protegido.
	conferirCasos(t, a, []casoDeRota{
		{"novo titular sai", http.MethodDelete, "/api/v1/projects/" + c.projeto + "/members/" + c.receptor.String(), c.tReceptor, nil,
			http.StatusConflict, "titular_protected", nil},
		{"antigo titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.outroOwner), http.StatusForbidden, "forbidden", nil},
		{"antigo titular sai", http.MethodDelete, "/api/v1/projects/" + c.projeto + "/members/" + c.titular.String(), c.tTitular, nil,
			http.StatusNoContent, "", nil},
	})
}

func TestAcceptTransferOverReceiverProjectLimit(t *testing.T) {
	a := newAPI(t)
	c := novaCessaoHTTP(t, a)
	// Quem recebe já é titular de 3 projetos (o padrão e mais 2): o teto.
	testdb.ProjetoPadrao(t, c.receptor)
	testdb.NovoProjeto(t, c.receptor, "Um")
	testdb.NovoProjeto(t, c.receptor, "Dois")

	conferirCasos(t, a, []casoDeRota{
		{"titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
		{"aceite acima do teto", http.MethodPost, c.aceite(), c.tReceptor, nil, http.StatusConflict, "project_limit_reached", nil},
		// A recusa por cota deixa a proposta: quem recebe ainda pode recusar.
		{"quem recebe recusa", http.MethodDelete, c.rota(), c.tReceptor, nil, http.StatusNoContent, "", nil},
	})
}

func TestCancelTransferRoute(t *testing.T) {
	a := newAPI(t)
	c := novaCessaoHTTP(t, a)

	conferirCasos(t, a, []casoDeRota{
		{"titular cancela sem proposta", http.MethodDelete, c.rota(), c.tTitular, nil, http.StatusNoContent, "", nil},
		{"titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
		{"quem não é membro cancela", http.MethodDelete, c.rota(), c.tFora, nil, http.StatusNotFound, "not_found", nil},
		{"outro owner cancela", http.MethodDelete, c.rota(), c.tOutro, nil, http.StatusForbidden, "forbidden", nil},
		{"manager cancela", http.MethodDelete, c.rota(), c.tManager, nil, http.StatusForbidden, "forbidden", nil},
		{"quem recebe recusa", http.MethodDelete, c.rota(), c.tReceptor, nil, http.StatusNoContent, "", nil},
		{"aceite depois da recusa", http.MethodPost, c.aceite(), c.tReceptor, nil, http.StatusForbidden, "forbidden", nil},
		{"titular propõe de novo", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
		{"titular cancela", http.MethodDelete, c.rota(), c.tTitular, nil, http.StatusNoContent, "", nil},
		{"aceite depois do cancelamento", http.MethodPost, c.aceite(), c.tReceptor, nil, http.StatusForbidden, "forbidden", nil},
	})
	if pendenteNaLista(t, a, c.tReceptor, c.projeto) {
		t.Error("a proposta cancelada continua na lista de quem recebe")
	}
}

// O teto de regras de conteúdo por host de quem recebe dá o 409 da criação de
// alvo.
func TestAcceptTransferOverReceiverContentHostLimit(t *testing.T) {
	a := newAPI(t)
	c := novaCessaoHTTP(t, a)
	const regras = `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, next_run_at, content_mode, content_value)
		SELECT gen_random_uuid(), $1, $2, 'regra ' || i, 'https://regra.exemplo.com/' || i, 'GET', 60, 10000, 200,
		       now(), 'contains', 'ok'
		  FROM generate_series(1, $3::int) i`
	admin := testdb.Admin(t)
	if _, err := admin.Exec(context.Background(), regras, c.receptor, testdb.ProjetoPadrao(t, c.receptor), targets.DefaultContentRulesPerHost); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(context.Background(), regras, c.titular, uuid.MustParse(c.projeto), 1); err != nil {
		t.Fatal(err)
	}

	conferirCasos(t, a, []casoDeRota{
		{"titular propõe", http.MethodPost, c.rota(), c.tTitular, para(c.receptor), http.StatusAccepted, "", nil},
		{"aceite acima do teto por host", http.MethodPost, c.aceite(), c.tReceptor, nil,
			http.StatusConflict, "content_rule_host_limit_reached", nil},
	})
}
