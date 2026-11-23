package account

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// O token vai no fragmento: não aparece em log de acesso nem no Referer.
func TestLinksLevamOTokenNoFragmento(t *testing.T) {
	l := Links{Base: "https://stillon.tech"}
	casos := map[string]string{
		l.ConfirmarEmail("abc"): "https://stillon.tech/confirm-email#token=abc",
		l.RedefinirSenha("abc"): "https://stillon.tech/reset-password#token=abc",
		l.EsqueciASenha():       "https://stillon.tech/forgot-password",
	}
	for got, queria := range casos {
		if got != queria {
			t.Errorf("link = %q, queria %q", got, queria)
		}
	}
}

func TestTokenDeContaTem256Bits(t *testing.T) {
	token, hash, err := novoTokenDeConta()
	if err != nil {
		t.Fatal(err)
	}
	cru, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token fora do base64url sem padding: %v", err)
	}
	if len(cru) != 32 {
		t.Errorf("token com %d bytes, queria 32", len(cru))
	}
	if !bytes.Equal(hash, crypto.HashToken(token)) {
		t.Error("hash diferente de crypto.HashToken(token)")
	}
	outro, _, err := novoTokenDeConta()
	if err != nil {
		t.Fatal(err)
	}
	if outro == token {
		t.Error("dois tokens iguais")
	}
}

func TestEmailsLevamOLink(t *testing.T) {
	const link = "https://stillon.tech/algum-link#token=xyz"
	casos := map[string]func(string) (string, string){
		"confirmação":  emailDeConfirmacao,
		"já tem conta": emailJaTemConta,
		"redefinição":  emailDeRedefinicao,
	}
	for nome, gerar := range casos {
		assunto, corpo := gerar(link)
		if strings.TrimSpace(assunto) == "" {
			t.Errorf("%s: assunto vazio", nome)
		}
		if !strings.Contains(corpo, link) {
			t.Errorf("%s: corpo sem o link: %q", nome, corpo)
		}
		if strings.Contains(assunto+corpo, "{{") {
			t.Errorf("%s: sobrou marcador de template", nome)
		}
	}
}

// Os propósitos são os valores que o CHECK de account_tokens.purpose aceita
// (migration 00047): trocar um aqui exige trocar lá.
func TestPropositosDosTokensDeConta(t *testing.T) {
	if propositoConfirmacao != "confirm_email" || propositoRedefinicao != "reset_password" {
		t.Errorf("propósitos = %q e %q; queria confirm_email e reset_password",
			propositoConfirmacao, propositoRedefinicao)
	}
}
