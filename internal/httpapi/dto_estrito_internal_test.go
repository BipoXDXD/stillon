package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// corpoEstrito é um handler que lê corpo JSON. valido é um corpo que passa
// pelo decoder; aninhados têm um campo desconhecido dentro de um objeto.
type corpoEstrito struct {
	handler   http.HandlerFunc
	valido    string
	aninhados []string
}

const webhookValidoDoTeste = "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"

func corposEstritos() map[string]corpoEstrito {
	alvos, batidas, canais := &TargetHandlers{}, &HeartbeatHandlers{}, &ChannelHandlers{}
	paginas, conta, sessao := &StatusPageHandlers{}, &AccountHandlers{}, &AuthHandlers{}
	item := `{"target_id":"` + uuid.NewString() + `","display_name":"a","extra":1}`
	agendaComExtra := `{"kind":"period","period_sec":60,"extra":1}`

	return map[string]corpoEstrito{
		"TargetHandlers.Create": {handler: alvos.Create, valido: `{"name":"api","url":"https://exemplo.com/"}`},
		"TargetHandlers.Update": {handler: alvos.Update, valido: `{"name":"api"}`},
		"HeartbeatHandlers.Create": {handler: batidas.Create,
			valido:    `{"name":"x","schedule":{"kind":"period","period_sec":60}}`,
			aninhados: []string{`{"name":"x","schedule":` + agendaComExtra + `}`}},
		"HeartbeatHandlers.Update": {handler: batidas.Update, valido: `{"name":"x"}`,
			aninhados: []string{`{"schedule":` + agendaComExtra + `}`}},
		"ChannelHandlers.Create":     {handler: canais.Create, valido: `{"kind":"discord","url":"` + webhookValidoDoTeste + `"}`},
		"ChannelHandlers.SetEnabled": {handler: canais.SetEnabled, valido: `{"enabled":true}`},
		"StatusPageHandlers.Create": {handler: paginas.Create, valido: `{"slug":"empresa","title":"T"}`,
			aninhados: []string{`{"slug":"empresa","title":"T","items":[` + item + `]}`}},
		"StatusPageHandlers.Update": {handler: paginas.Update, valido: `{"title":"T"}`,
			aninhados: []string{`{"items":[` + item + `]}`}},
		"AccountHandlers.ChangePassword": {handler: conta.ChangePassword, valido: `{"current_password":"a","new_password":"b"}`},
		"AuthHandlers.Login":             {handler: sessao.Login, valido: `{"email":"a@exemplo.com","password":"x"}`},
		"AuthHandlers.TOTPEnroll":        {handler: sessao.TOTPEnroll, valido: `{"password":"x"}`},
		"AuthHandlers.TOTPConfirm":       {handler: sessao.TOTPConfirm, valido: `{"code":"1"}`},
		"AuthHandlers.TOTPVerify":        {handler: sessao.TOTPVerify, valido: `{"challenge":"x","code":"1"}`},
		"AuthHandlers.TOTPDisable":       {handler: sessao.TOTPDisable, valido: `{"password":"x","code":"1"}`},
		"AuthHandlers.TOTPRotate":        {handler: sessao.TOTPRotate, valido: `{"password":"x","code":"1"}`},
		"AuthHandlers.TOTPRotateConfirm": {handler: sessao.TOTPRotateConfirm, valido: `{"code":"1","rotation":"x"}`},
	}
}

// Todo handler que lê corpo JSON está na tabela: uma rota nova com
// decodeJSON e sem caso de campo desconhecido falha aqui.
func TestTodoHandlerComCorpoJSONEstaNaTabela(t *testing.T) {
	fontes, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var achados []string
	for _, nome := range fontes {
		if strings.HasSuffix(nome, "_test.go") {
			continue
		}
		arquivo, err := parser.ParseFile(fset, nome, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range arquivo.Decls {
			funcao, ok := decl.(*ast.FuncDecl)
			if ok && funcao.Body != nil && chamaDecodeJSON(funcao.Body) {
				achados = append(achados, nomeDoMetodo(funcao))
			}
		}
	}
	slices.Sort(achados)
	if len(achados) == 0 {
		t.Fatal("nenhum chamador de decodeJSON encontrado; o parser deixou de enxergá-los")
	}
	if tabela := slices.Sorted(maps.Keys(corposEstritos())); !slices.Equal(achados, tabela) {
		t.Errorf("chamadores de decodeJSON = %v;\na tabela tem %v", achados, tabela)
	}
}

func chamaDecodeJSON(corpo *ast.BlockStmt) bool {
	achou := false
	ast.Inspect(corpo, func(n ast.Node) bool {
		if chamada, ok := n.(*ast.CallExpr); ok {
			if ident, ok := chamada.Fun.(*ast.Ident); ok && ident.Name == "decodeJSON" {
				achou = true
			}
		}
		return !achou
	})
	return achou
}

func nomeDoMetodo(f *ast.FuncDecl) string {
	if f.Recv == nil || len(f.Recv.List) != 1 {
		return f.Name.Name
	}
	tipo := f.Recv.List[0].Type
	if ponteiro, ok := tipo.(*ast.StarExpr); ok {
		tipo = ponteiro.X
	}
	ident, ok := tipo.(*ast.Ident)
	if !ok {
		return f.Name.Name
	}
	return ident.Name + "." + f.Name.Name
}

// Os handlers não têm serviço nenhum: chegar a ele é panic. Por isso o 400
// prova que nada foi lido nem gravado, e o caso sem o campo extra prova que
// a recusa veio do campo, e não de outro defeito do corpo.
func TestCampoDesconhecidoNuncaChegaAoServico(t *testing.T) {
	for nome, caso := range corposEstritos() {
		t.Run(nome, func(t *testing.T) {
			if _, chegou := chamarSemServico(t, caso.handler, caso.valido); !chegou {
				t.Fatalf("o corpo válido %s não chegou ao serviço", caso.valido)
			}
			for _, extra := range []string{"id", "user_id", "owner_id", "role", "status", "created_at"} {
				var campos map[string]any
				if err := json.Unmarshal([]byte(caso.valido), &campos); err != nil {
					t.Fatal(err)
				}
				campos[extra] = "valor-do-cliente"
				corpo, err := json.Marshal(campos)
				if err != nil {
					t.Fatal(err)
				}
				conferirRecusaDeCampo(t, caso.handler, string(corpo))
			}
			for _, corpo := range caso.aninhados {
				conferirRecusaDeCampo(t, caso.handler, corpo)
			}
		})
	}
}

func conferirRecusaDeCampo(t *testing.T, h http.HandlerFunc, corpo string) {
	t.Helper()
	rec, chegou := chamarSemServico(t, h, corpo)
	if chegou {
		t.Errorf("%s chegou ao serviço, queria a recusa antes", corpo)
		return
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	err := json.Unmarshal(rec.Body.Bytes(), &envelope)
	if err != nil || rec.Code != http.StatusBadRequest || envelope.Error.Code != "validation_failed" {
		t.Errorf("%s deu %d %s, queria 400 validation_failed", corpo, rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "valor-do-cliente") {
		t.Errorf("%s: a recusa ecoou o valor recebido: %s", corpo, rec.Body)
	}
}

func chamarSemServico(t *testing.T, h http.HandlerFunc, corpo string) (rec *httptest.ResponseRecorder, chegou bool) {
	t.Helper()
	agora := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	chave := []byte("segredo-publico-de-teste-32bytes!!")
	acesso, err := auth.NewAccessToken(chave, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(corpo))
	req.SetPathValue("id", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+acesso)
	rec = httptest.NewRecorder()
	defer func() { chegou = recover() != nil }()
	middleware.Authn(chave, clock.NewFake(agora))(h).ServeHTTP(rec, req)
	return rec, chegou
}
