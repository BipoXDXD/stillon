package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
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
	cfg := &config.Config{}
	if e := enviadorDeConta(cfg, &tetoFalso{}, &orcamentosFalsos{}); e != nil {
		t.Fatalf("sem chave: enviadorDeConta = %#v, queria nil", e)
	}
	if v := verificador(cfg, nil); v != nil {
		t.Errorf("sem chave: verificador = %#v, queria a interface nil", v)
	}
	if _, ok := mailerDeConta(nil).(contaemail.SemEnvio); !ok {
		t.Errorf("sem chave: mailerDeConta = %#v, queria contaemail.SemEnvio", mailerDeConta(nil))
	}
}

// cfgComEmail tem os dois remetentes diferentes: o teste vê qual saiu.
func cfgComEmail() *config.Config {
	return &config.Config{
		ResendAPIKey: chaveResendDeTeste, EmailFrom: "avisos@exemplo.test",
		AccountEmailFrom: "conta@exemplo.test", IPHashKey: chave,
	}
}

// A verificação de canal por e-mail é e-mail de conta (spec): passa pelo
// balde do destinatário e pelo teto, e sai pelo remetente de conta.
func TestVerificadorDeCanalPassaPeloTeto(t *testing.T) {
	rf := novoResendFalso(t)
	cfg := cfgComEmail()
	teto, orc := &tetoFalso{libera: true}, &orcamentosFalsos{libera: false}
	v := verificador(cfg, enviadorDeConta(cfg, teto, orc))
	if _, ok := v.(*notify.Resend); ok || v == nil {
		t.Fatalf("verificador = %#v, queria o decorador do contaemail", v)
	}

	if err := v.SendVerification(context.Background(), crypto.Secret("dono@exemplo.test"), "https://x/verify/1"); !errors.Is(err, notify.ErrVerificationSuppressed) {
		t.Fatalf("SendVerification com o balde vazio = %v, queria ErrVerificationSuppressed", err)
	}
	if n := len(rf.lista()); n != 0 {
		t.Fatalf("com o balde vazio, o Resend recebeu %d e-mails, queria 0", n)
	}

	orc.mudar(true)
	if err := v.SendVerification(context.Background(), crypto.Secret("dono@exemplo.test"), "https://x/verify/2"); err != nil {
		t.Fatalf("SendVerification = %v", err)
	}
	if teto.total() != 1 {
		t.Errorf("Reservar recebeu %d chamadas, queria 1", teto.total())
	}
	got := rf.lista()
	if len(got) != 1 || got[0].From != cfg.AccountEmailFrom {
		t.Errorf("Resend recebeu %+v, queria um e-mail de %s", got, cfg.AccountEmailFrom)
	}
}

// Os avisos de incidente nunca passam pelo teto dos e-mails de conta e
// saem pelo remetente dos avisos (EMAIL_FROM).
func TestNotificadoresNaoPassamPeloTeto(t *testing.T) {
	rf := novoResendFalso(t)
	cfg := cfgComEmail()
	var email notify.Notifier
	for _, n := range notificadores(cfg) {
		if n.Kind() == notify.KindEmail {
			email = n
		}
	}
	resend, ok := email.(*notify.Resend)
	if !ok || resend == nil {
		t.Fatalf("notificador de e-mail = %#v, queria um *notify.Resend", email)
	}
	ev := notify.Event{NotificationID: uuid.Must(uuid.NewV7()), Kind: "opened", TargetName: "site", StartedAt: time.Now()}
	if err := resend.Send(context.Background(), crypto.Secret("dono@exemplo.test"), ev); err != nil {
		t.Fatalf("Send = %v", err)
	}
	got := rf.lista()
	if len(got) != 1 || got[0].From != cfg.EmailFrom {
		t.Errorf("Resend recebeu %+v, queria um e-mail de %s", got, cfg.EmailFrom)
	}
}

// Os e-mails de conta (confirmação, redefinição) saem pela fila do enviador
// com o remetente de conta (ACCOUNT_EMAIL_FROM).
func TestRemetenteDeContaNaFiacao(t *testing.T) {
	rf := novoResendFalso(t)
	cfg := cfgComEmail()
	e := enviadorDeConta(cfg, &tetoFalso{libera: true}, &orcamentosFalsos{libera: true})
	ctx, cancel := context.WithCancel(context.Background())
	feito := make(chan struct{})
	go func() { e.Rodar(ctx); close(feito) }()
	t.Cleanup(func() { cancel(); <-feito })

	mailer := mailerDeConta(e)
	if mailer != account.AccountMailer(e) {
		t.Fatalf("mailerDeConta = %#v, queria o enviador", mailer)
	}
	if err := mailer.SendAccountEmail(context.Background(), "pessoa@exemplo.test", "Assunto", "Corpo", account.EntregaDeEmail{}); err != nil {
		t.Fatal(err)
	}
	got := rf.esperar(t, 1)
	if got[0].From != cfg.AccountEmailFrom || got[0].To[0] != "pessoa@exemplo.test" {
		t.Errorf("Resend recebeu %+v, queria um e-mail de %s para pessoa@exemplo.test", got[0], cfg.AccountEmailFrom)
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
		{"run", "notify.NewChannelService(", "verificador(cfg, enviador), cfg.PublicBaseURL)"},
		{"run", "account.NewCadastro(", "mailerDeConta(enviador)"},
		{"run", "account.NewRecuperacao(", "mailerDeConta(enviador)"},
		// crypto.Secret é []byte e os dois campos são string: a conversão
		// aceita qualquer um deles, e o compilador não barra a troca.
		{"notificadores", "novoResend(", "novoResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)"},
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
