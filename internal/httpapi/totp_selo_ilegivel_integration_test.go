package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/crypto"
)

// selosIlegiveis são as duas formas de o segredo ativo não abrir: a restauração
// com a MASTER_KEY errada, que o runbook prevê, e o selo danificado no banco.
var selosIlegiveis = []struct {
	nome  string
	gerar func(t *testing.T, a *api, segredo string, atual []byte) []byte
}{
	{"selado com outra chave", func(t *testing.T, a *api, segredo string, _ []byte) []byte {
		t.Helper()
		outra, err := crypto.NewBox(bytes.Repeat([]byte{7}, crypto.KeySize))
		if err != nil {
			t.Fatal(err)
		}
		selo, err := outra.Seal([]byte(segredo), crypto.AAD("users", "totp_secret_enc", a.userID))
		if err != nil {
			t.Fatal(err)
		}
		return selo
	}},
	{"selo corrompido", func(_ *testing.T, _ *api, _ string, atual []byte) []byte {
		corrompido := bytes.Clone(atual)
		corrompido[len(corrompido)-1] ^= 0xFF
		return corrompido
	}},
}

func seloAtivo(t *testing.T, a *api) []byte {
	t.Helper()
	var selo []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, a.userID).Scan(&selo); err != nil {
		t.Fatal(err)
	}
	return selo
}

func gravarSeloAtivo(t *testing.T, a *api, selo []byte) {
	t.Helper()
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET totp_secret_enc = $2 WHERE id = $1`, a.userID, selo); err != nil {
		t.Fatal(err)
	}
}

// conferirErroInternoGenerico lê e fecha a resposta e devolve o corpo, para
// quem procura nele um detalhe próprio. A mensagem literal é a da tabela de
// erros; a lista pega o detalhe que chegasse em outro campo.
func conferirErroInternoGenerico(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, queria 500: %s", resp.StatusCode, corpo)
	}
	var env envelope
	if err := json.Unmarshal(corpo, &env); err != nil {
		t.Fatalf("resposta fora do envelope: %v: %s", err, corpo)
	}
	if env.Error.Code != "internal_error" || env.Error.Message != "Erro interno." {
		t.Errorf("erro %q %q, queria internal_error \"Erro interno.\"", env.Error.Code, env.Error.Message)
	}
	for _, detalhe := range []string{"decifrar", "TOTP", "cipher", "authentication failed"} {
		if strings.Contains(string(corpo), detalhe) {
			t.Errorf("o corpo trouxe %q: %s", detalhe, corpo)
		}
	}
	if ck := refreshCookie(resp); ck != nil {
		t.Errorf("a falha interna mexeu no cookie de refresh: %+v", ck)
	}
	return corpo
}

func desafioFoiConsumido(t *testing.T, a *api) bool {
	t.Helper()
	var consumido bool
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_challenge_mark IS NOT NULL FROM users WHERE id = $1`, a.userID).Scan(&consumido); err != nil {
		t.Fatal(err)
	}
	return consumido
}

// Depois de uma restauração com a chave errada, o dono insiste: o código é
// certo, o defeito é do servidor. Uma tentativa além do limite prova que ele
// não tranca a própria conta, e o código de recuperação enviado não é gasto.
// Com o selo de volta, o mesmo desafio e o mesmo código abrem a sessão.
func TestUnreadableTOTPSecretFailsVerifyWithoutPunishingTheOwner(t *testing.T) {
	for _, caso := range selosIlegiveis {
		t.Run(caso.nome, func(t *testing.T) {
			a := apiAtrasDeProxy(t)
			segredo := cadastrarTOTP(t, a, clientWithJar(t))
			a.clk.Advance(time.Second)
			desafio := desafioAtual(t, a, clientWithJar(t))
			original := seloAtivo(t, a)
			gravarSeloAtivo(t, a, caso.gerar(t, a, segredo, original))
			pedido := map[string]string{"challenge": desafio, "code": a.recuperacao[0]}

			for range auth.LockThreshold + 1 {
				resp, err := postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/verify", pedido)
				if err != nil {
					t.Fatal(err)
				}
				conferirErroInternoGenerico(t, resp)
			}

			if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != 0 || bloqueada {
				t.Errorf("%d falhas (bloqueada=%v), queria 0 e destravada", falhas, bloqueada)
			}
			if confirmado, temSegredo, codigos, _ := estadoDoSegundoFator(t, a); !confirmado || !temSegredo || codigos != 10 {
				t.Errorf("confirmado=%v, segredo=%v, %d códigos; queria ligado e 10 códigos", confirmado, temSegredo, codigos)
			}
			if n := eventosDe(t, a, "totp.recovery_used"); n != 0 {
				t.Errorf("%d eventos totp.recovery_used, queria 0", n)
			}
			if n := familiasVivas(t, a); n != 1 {
				t.Errorf("%d famílias vivas, queria só a do cadastro", n)
			}
			if desafioFoiConsumido(t, a) {
				t.Error("o desafio foi consumido sem o código ter sido julgado")
			}

			gravarSeloAtivo(t, a, original)
			depois, err := postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/verify", pedido)
			if err != nil {
				t.Fatal(err)
			}
			defer depois.Body.Close()
			if depois.StatusCode != http.StatusOK {
				t.Errorf("com o selo de volta: status %d, queria 200", depois.StatusCode)
			}
		})
	}
}

// Falha do servidor não é prova errada: contada, as cinco desta rota derrubariam
// a sessão do dono que tenta desligar um fator que o servidor não consegue ler.
func TestUnreadableTOTPSecretFailsDisableWithoutCountingWrongProofs(t *testing.T) {
	for _, caso := range selosIlegiveis {
		t.Run(caso.nome, func(t *testing.T) {
			a := apiComFichaPorPedido(t)
			c := clientWithJar(t)
			segredo := cadastrarTOTP(t, a, c)
			token := accessTokenDaSessao(t, a, c)
			gravarSeloAtivo(t, a, caso.gerar(t, a, segredo, seloAtivo(t, a)))
			pedido := map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)}

			for range auth.FamilyProofLimit + 1 {
				conferirErroInternoGenerico(t, postComToken(t, a.cliente(), a.srv.URL+"/api/v1/auth/totp/disable", token, pedido))
			}

			if n := familiasVivas(t, a); n != 1 {
				t.Errorf("%d famílias vivas, queria 1: as falhas internas contaram como provas erradas", n)
			}
			confirmado, temSegredo, codigos, desativacoes := estadoDoSegundoFator(t, a)
			if !confirmado || !temSegredo || codigos != 10 || desativacoes != 0 {
				t.Errorf("confirmado=%v, segredo=%v, %d códigos, %d desativações; queria ligado, 10 e 0",
					confirmado, temSegredo, codigos, desativacoes)
			}
		})
	}
}
