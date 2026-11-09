package httpapi

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/notify"
)

// O acordo entre channelResponse e o schema Canal do painel. Os testes daqui
// conferem que o JSON real, de toChannelResponse e apierr.WriteJSON, continua
// igual a testdata/contrato-canais.json; o painel faz o parse desse arquivo em
// web/src/lib/api/schemas-contrato.test.ts. O mesmo desenho de faixa.json.
//
// Mudou o DTO de propósito? Regrave com a variável abaixo e rode o vitest: é
// ele que diz se o painel aceita a forma nova.
const (
	arquivoDoContrato = "contrato-canais.json"
	regravarContrato  = "STILLON_REGRAVAR_CONTRATO"
)

type casoDoContrato struct {
	Caso     string          `json:"caso"`
	Resposta json.RawMessage `json:"resposta"`
}

func TestChannelResponseKeepsThePanelContract(t *testing.T) {
	gerado := contratoGerado(t)
	caminho := filepath.Join("testdata", arquivoDoContrato)
	if os.Getenv(regravarContrato) != "" {
		if err := os.WriteFile(caminho, gerado, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	gravado, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gerado, gravado) {
		t.Errorf("a resposta de canal mudou; se foi de propósito, regrave com %s=1, confira o git diff de %s e rode o vitest do painel",
			regravarContrato, caminho)
	}
}

// Um valor novo de status ou kind no Go tem de entrar nos casos: é assim que
// ele chega ao z.enum do painel.
func TestContractCasesCoverEveryKindAndStatus(t *testing.T) {
	var kinds, status []string
	for _, c := range canaisDoContrato(t) {
		kinds = append(kinds, string(c.canal.Kind))
		status = append(status, string(c.canal.Status))
	}
	for _, faltou := range valoresAusentes(constantesDoTipo(t, "Kind"), kinds) {
		t.Errorf("notify.Kind %q sem caso no contrato", faltou)
	}
	for _, faltou := range valoresAusentes(constantesDoTipo(t, "ChannelStatus"), status) {
		t.Errorf("notify.ChannelStatus %q sem caso no contrato", faltou)
	}
}

func contratoGerado(t *testing.T) []byte {
	t.Helper()
	var casos []casoDoContrato
	for _, c := range canaisDoContrato(t) {
		casos = append(casos, casoDoContrato{Caso: c.nome, Resposta: respostaReal(t, c.canal)})
	}
	b, err := json.MarshalIndent(casos, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func respostaReal(t *testing.T, c notify.Channel) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	apierr.WriteJSON(rec, http.StatusOK, toChannelResponse(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("WriteJSON deu %d", rec.Code)
	}
	return bytes.TrimSpace(rec.Body.Bytes())
}

type canalDoContrato struct {
	nome  string
	canal notify.Channel
}

// canaisDoContrato é a entrada do domínio, e nunca o JSON: a forma da resposta
// sai só de toChannelResponse. Os motivos de falha vêm do código de notify: um
// motivo novo muda o artefato, e o vitest confere se o painel o traduz.
func canaisDoContrato(t *testing.T) []canalDoContrato {
	t.Helper()
	discord := notify.Channel{
		ID:        uuid.MustParse("01900000-0000-7000-8000-000000000001"),
		Kind:      notify.KindDiscord,
		Hint:      "webhook …6789",
		Status:    notify.StatusVerified,
		Enabled:   true,
		CreatedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	}
	email := discord
	email.ID = uuid.MustParse("01900000-0000-7000-8000-000000000002")
	email.Kind = notify.KindEmail
	email.Hint = "p***@exemplo.com"
	falhouEm := time.Date(2026, 10, 25, 3, 4, 5, 678000000, time.UTC)

	variar := func(nome string, base notify.Channel, muda func(*notify.Channel)) canalDoContrato {
		muda(&base)
		return canalDoContrato{nome: nome, canal: base}
	}
	falhou := func(c *notify.Channel) { c.LastFailureAt, c.LastError = falhouEm, notify.ReasonRejected }
	casos := []canalDoContrato{
		variar("discord verificado, nunca falhou", discord, func(*notify.Channel) {}),
		variar("discord falhando, com motivo e hora", discord, func(c *notify.Channel) {
			c.Status = notify.StatusFailing
			falhou(c)
		}),
		variar("discord pausado, voltou a entregar depois da falha", discord, func(c *notify.Channel) {
			c.Enabled = false
			falhou(c)
		}),
		variar("discord com destino ilegível, sem hint", discord, func(c *notify.Channel) { c.Hint = "" }),
		variar("discord com falha sem motivo gravado", discord, func(c *notify.Channel) {
			c.Status = notify.StatusFailing
			c.LastFailureAt = falhouEm
		}),
		variar("e-mail verificado, nunca falhou", email, func(*notify.Channel) {}),
		variar("e-mail pendente", email, func(c *notify.Channel) { c.Status = notify.StatusPending }),
		variar("e-mail expirado", email, func(c *notify.Channel) { c.Status = notify.StatusExpired }),
		variar("e-mail falhando, com motivo e hora", email, func(c *notify.Channel) {
			c.Status = notify.StatusFailing
			falhou(c)
		}),
	}
	for _, motivo := range constantesDoTipo(t, "Reason") {
		casos = append(casos, variar("discord falhando por "+motivo, discord, func(c *notify.Channel) {
			c.Status = notify.StatusFailing
			c.LastFailureAt, c.LastError = falhouEm, notify.Reason(motivo)
		}))
	}
	return casos
}

func valoresAusentes(todos, cobertos []string) []string {
	var ausentes []string
	for _, v := range todos {
		if !slices.Contains(cobertos, v) {
			ausentes = append(ausentes, v)
		}
	}
	return ausentes
}

// constantesDoTipo lê do código-fonte de notify os valores das constantes de
// texto do tipo: o Go não enumera constantes em tempo de execução.
func constantesDoTipo(t *testing.T, tipo string) []string {
	t.Helper()
	arquivos, err := filepath.Glob(filepath.Join("..", "notify", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var valores []string
	for _, caminho := range arquivos {
		if strings.HasSuffix(caminho, "_test.go") {
			continue
		}
		arquivo, err := parser.ParseFile(token.NewFileSet(), caminho, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(arquivo, func(n ast.Node) bool {
			if bloco, ok := n.(*ast.GenDecl); ok && bloco.Tok == token.CONST {
				valores = append(valores, valoresDoBloco(t, bloco, tipo)...)
			}
			return true
		})
	}
	if len(valores) == 0 {
		t.Fatalf("nenhuma constante do tipo %s em internal/notify", tipo)
	}
	return valores
}

func valoresDoBloco(t *testing.T, bloco *ast.GenDecl, tipo string) []string {
	t.Helper()
	var valores []string
	for _, spec := range bloco.Specs {
		if declaracao, ok := spec.(*ast.ValueSpec); ok {
			valores = append(valores, valoresDaDeclaracao(t, declaracao, tipo)...)
		}
	}
	return valores
}

// valoresDaDeclaracao aceita as duas formas de dar tipo à constante:
// X Tipo = "a" e X = Tipo("a"). Uma forma que não dá para ler para o teste, em
// vez de deixar a constante de fora.
func valoresDaDeclaracao(t *testing.T, spec *ast.ValueSpec, tipo string) []string {
	t.Helper()
	var valores []string
	for _, expr := range spec.Values {
		valor, convertida := argumentoDaConversao(expr, tipo)
		if !convertida && !nomeiaOTipo(spec.Type, tipo) {
			continue
		}
		literal, ok := valor.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Fatalf("constante de %s sem literal de texto", tipo)
		}
		texto, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		valores = append(valores, texto)
	}
	return valores
}

func argumentoDaConversao(expr ast.Expr, tipo string) (ast.Expr, bool) {
	chamada, ok := expr.(*ast.CallExpr)
	if !ok || len(chamada.Args) != 1 || !nomeiaOTipo(chamada.Fun, tipo) {
		return expr, false
	}
	return chamada.Args[0], true
}

func nomeiaOTipo(expr ast.Expr, tipo string) bool {
	nome, ok := expr.(*ast.Ident)
	return ok && nome.Name == tipo
}
