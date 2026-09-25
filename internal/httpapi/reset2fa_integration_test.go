package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/account"
)

// resetarPeloCLI é o que `vigilume user reset-2fa` chama (Plano 9, §3.1): o
// serviço sem segundo fator e sem chave de IP, como o CLI o monta.
func resetarPeloCLI(t *testing.T, a *api) {
	t.Helper()
	ligado, _, err := account.NewService(a.pool, nil, a.clk, nil).ResetTwoFactor(context.Background(), testEmail)
	if err != nil {
		t.Fatal(err)
	}
	if !ligado {
		t.Fatal("o reset-2fa não encontrou o 2FA ligado")
	}
}

// Depois do reset-2fa, a senha sozinha volta a entrar: o login devolve access
// token, sem desafio. A perna oposta (com o 2FA ligado, o login pede o
// desafio) é a do começo.
func TestLoginAfterReset2FANeedsOnlyThePassword(t *testing.T) {
	a := newAPI(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	desafioAtual(t, a, clientWithJar(t)) // com o 2FA ligado, há desafio

	resetarPeloCLI(t, a)

	resp := login(t, a, clientWithJar(t), testEmail, testPass)
	defer resp.Body.Close()
	var corpo struct {
		AccessToken  string `json:"access_token"`
		TOTPRequired bool   `json:"totp_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.TOTPRequired || corpo.AccessToken == "" {
		t.Errorf("depois do reset-2fa o login ainda pede o segundo fator (status %d)", resp.StatusCode)
	}
}

// O reset-2fa apaga a troca pendente: ligado de novo com outro app, o QR da
// troca abandonada não confirma (spec do Plano 9, §6, linha 3.2).
func TestReset2FADropsThePendingRotation(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	st, abandonado := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	if st != http.StatusOK {
		t.Fatalf("rotate devolveu %d", st)
	}

	resetarPeloCLI(t, a)
	if temPendente(t, a) {
		t.Error("o reset-2fa deixou o segredo pendente")
	}

	a.clk.Advance(30 * time.Second)
	cadastrarTOTP(t, a, clientWithJar(t))
	novoToken := accessTokenValido(t, a) // o reset encerrou todas as sessões
	st, codigo, _ := confirmarTrocaHTTP(t, a, novoToken, codigoDe(t, a, abandonado))
	if st != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("confirm da troca abandonada antes do reset-2fa: %d %s, queria 403 forbidden", st, codigo)
	}
}
