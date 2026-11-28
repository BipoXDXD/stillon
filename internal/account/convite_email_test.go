package account_test

import (
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/account"
)

func TestLinkDoConviteLevaOTokenNoFragmento(t *testing.T) {
	for _, base := range []string{"https://stillon.tech", "https://stillon.tech/"} {
		if got := (account.Links{Base: base}).AceitarConvite("abc"); got != "https://stillon.tech/invite#token=abc" {
			t.Errorf("base %q: %q", base, got)
		}
	}
}

// O texto só depende dos argumentos: o mesmo para quem tem conta e para quem
// não tem, e nada nele fala da conta do destinatário.
func TestInvitationEmailSameForAnyRecipient(t *testing.T) {
	a1, c1 := account.EmailDeConvite("Loja", "ana@x.com", "member", "https://stillon.tech/invite#token=t")
	a2, c2 := account.EmailDeConvite("Loja", "ana@x.com", "member", "https://stillon.tech/invite#token=t")
	if a1 != a2 || c1 != c2 {
		t.Fatal("o texto mudou entre duas chamadas iguais")
	}
	for _, proibido := range []string{"crie", "já tem conta", "cadastr", "member", "viewer", "manager", "owner"} {
		if strings.Contains(strings.ToLower(a1+c1), proibido) {
			t.Errorf("o texto contém %q:\n%s\n%s", proibido, a1, c1)
		}
	}
	for _, quer := range []string{"Olá,", "ana@x.com", "Loja", "7 dias", "uma vez", "ignore"} {
		if !strings.Contains(c1, quer) {
			t.Errorf("o corpo não contém %q:\n%s", quer, c1)
		}
	}
}

// Cada papel tem rótulo próprio em português; papel desconhecido não vaza.
func TestInvitationEmailRoleLabels(t *testing.T) {
	vistos := map[string]string{}
	for _, papel := range []string{"owner", "manager", "member", "viewer"} {
		_, corpo := account.EmailDeConvite("Loja", "ana@x.com", papel, "L")
		for p, c := range vistos {
			if c == corpo {
				t.Errorf("%s e %s dão o mesmo texto", p, papel)
			}
		}
		vistos[papel] = corpo
	}
	if _, corpo := account.EmailDeConvite("Loja", "ana@x.com", "admin", "L"); strings.Contains(corpo, "admin") {
		t.Errorf("papel desconhecido no texto:\n%s", corpo)
	}
}

func TestInvitationEmailCarriesLinkAloneOnALine(t *testing.T) {
	link := "https://stillon.tech/invite#token=abc_DEF-123"
	_, corpo := account.EmailDeConvite("Loja", "ana@x.com", "viewer", link)
	sozinho := false
	for _, linha := range strings.Split(corpo, "\n") {
		if linha == link {
			sozinho = true
		}
	}
	if !sozinho {
		t.Errorf("o link não está sozinho numa linha:\n%s", corpo)
	}
}

// Um nome antigo com quebra de linha ou outro caractere de controle não
// quebra o texto nem o assunto.
func TestInvitationEmailStripsControlCharsFromProjectName(t *testing.T) {
	assunto, corpo := account.EmailDeConvite("Lo\r\nBcc: x@y.com\x00ja\t", "ana@x\n.com", "viewer", "L")
	if strings.ContainsAny(assunto, "\r\n\x00\t") {
		t.Errorf("assunto com caractere de controle: %q", assunto)
	}
	if strings.Contains(corpo, "\r") || strings.Contains(corpo, "\x00") || strings.Contains(corpo, "\t") {
		t.Errorf("corpo com caractere de controle: %q", corpo)
	}
	for _, linha := range strings.Split(corpo, "\n") {
		if strings.HasPrefix(linha, "Bcc:") || linha == ".com" {
			t.Errorf("o nome ou o e-mail quebrou a linha: %q", corpo)
		}
	}
	if !strings.Contains(corpo, "LoBcc: x@y.comja") {
		t.Errorf("o nome não aparece limpo:\n%s", corpo)
	}
}
