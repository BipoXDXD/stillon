package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/notify"
)

const chaveResendDeTeste = "re_chave_sintetica_de_teste"

// Nenhum teste aqui chama Send: montar o Resend não abre conexão.
func TestNotificadoresFollowTheResendKey(t *testing.T) {
	casos := []struct {
		nome  string
		chave string
		quer  []string
	}{
		{"sem chave", "", []string{"discord"}},
		{"com chave", chaveResendDeTeste, []string{"discord", "email"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			cfg := &config.Config{ResendAPIKey: c.chave, EmailFrom: "avisos@exemplo.test"}
			var tipos []string
			for _, n := range notificadores(cfg) {
				tipos = append(tipos, string(n.Kind()))
			}
			// O dispatcher guarda os notificadores num mapa por Kind: a ordem não importa.
			slices.Sort(tipos)
			if !slices.Equal(tipos, c.quer) {
				t.Errorf("notificadores = %v, queria %v", tipos, c.quer)
			}
		})
	}
}

// Um *Resend nil dentro da interface não é nil, e o cadastro acharia que há
// e-mail: a comparação tem de ser com a interface.
func TestVerificadorIsARealNilWithoutTheKey(t *testing.T) {
	if v := verificador(&config.Config{}); v != nil {
		t.Errorf("sem chave: verificador = %#v, queria a interface nil", v)
	}
}

func TestVerificadorIsResendWithTheKey(t *testing.T) {
	v := verificador(&config.Config{ResendAPIKey: chaveResendDeTeste, EmailFrom: "avisos@exemplo.test"})
	resend, ok := v.(*notify.Resend)
	if !ok || resend == nil {
		t.Fatalf("com chave: verificador = %#v, queria um *notify.Resend", v)
	}
	if resend.Kind() != "email" {
		t.Errorf("Kind = %q, queria email", resend.Kind())
	}
}

// run só monta o dispatcher e o cadastro depois de abrir o banco, e o Resend
// tem endpoint fixo: rodar o processo com a chave mandaria e-mail de verdade.
// Por isso a prova de que run usa as duas funções acima lê o próprio main.go.
func TestRunWiresEmailIntoDispatcherAndChannelService(t *testing.T) {
	casos := []struct {
		funcao  string
		chamada string
		quer    string
	}{
		{"run", "notify.NewDispatcher(", "notificadores(cfg)...)"},
		{"run", "notify.NewChannelService(", "verificador(cfg), cfg.PublicBaseURL)"},
		// crypto.Secret é []byte e os dois campos são string: a conversão
		// aceita qualquer um deles, e o compilador não barra a troca.
		{"notificadores", "notify.NewResend(", "notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)"},
		{"verificador", "notify.NewResend(", "notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)"},
	}
	funcoes := funcoesDeMain(t)
	for _, c := range casos {
		t.Run(c.funcao+"/"+c.chamada, func(t *testing.T) {
			corpo, ok := funcoes[c.funcao]
			if !ok {
				t.Fatalf("main.go não tem a função %s", c.funcao)
			}
			achadas := chamadasCom(t, corpo, c.chamada)
			if len(achadas) != 1 {
				t.Fatalf("%s em %s: %d chamadas, queria 1: %v", c.chamada, c.funcao, len(achadas), achadas)
			}
			if !strings.Contains(achadas[0], c.quer) {
				t.Errorf("chamada = %s; queria que contivesse %s", achadas[0], c.quer)
			}
		})
	}
}

func funcoesDeMain(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	arquivo, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	funcoes := map[string]*ast.FuncDecl{}
	for _, d := range arquivo.Decls {
		if f, ok := d.(*ast.FuncDecl); ok {
			funcoes[f.Name.Name] = f
		}
	}
	return funcoes
}

// chamadasCom devolve, como texto, as chamadas dentro de corpo que começam
// com prefixo.
func chamadasCom(t *testing.T, corpo *ast.FuncDecl, prefixo string) []string {
	t.Helper()
	var achadas []string
	ast.Inspect(corpo, func(n ast.Node) bool {
		chamada, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var texto bytes.Buffer
		if err := format.Node(&texto, token.NewFileSet(), chamada); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(texto.String(), prefixo) {
			achadas = append(achadas, texto.String())
		}
		return true
	})
	return achadas
}
