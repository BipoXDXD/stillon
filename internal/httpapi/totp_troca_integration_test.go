package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

// A troca de autenticador com segredo pendente (Plano 9, §3.2), pelo HTTP:
// POST /auth/totp/rotate {password, code} e POST /auth/totp/rotate/confirm
// {code}.

func codigoDe(t *testing.T, a *api, segredo string) string {
	t.Helper()
	c, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// iniciarTrocaHTTP chama o rotate e devolve o status e, no 200, o segredo novo
// tirado da otpauth_uri. O rotation da resposta (A7 do Plano 10) fica em
// a.rotacao, para o confirmarTrocaHTTP.
func iniciarTrocaHTTP(t *testing.T, a *api, token string, corpo map[string]string) (int, string) {
	t.Helper()
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate", token, corpo)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var r struct {
		URI      string `json:"otpauth_uri"`
		Rotation string `json:"rotation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if len(r.Rotation) != 32 {
		t.Fatalf("rotate devolveu rotation %q, queria 32 caracteres hex", r.Rotation)
	}
	a.rotacao = r.Rotation
	key, err := otp.NewKeyFromURL(r.URI)
	if err != nil {
		t.Fatalf("rotate não devolveu uma URI otpauth:// válida: %v", err)
	}
	return resp.StatusCode, key.Secret()
}

// confirmarTrocaHTTP chama o rotate/confirm com o rotation da última troca
// aceita e devolve o status, o code do envelope (vazio no sucesso) e, no 200,
// os códigos de recuperação.
func confirmarTrocaHTTP(t *testing.T, a *api, token, codigo string) (int, string, []string) {
	t.Helper()
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate/confirm", token,
		map[string]string{"code": codigo, "rotation": a.rotacao})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, codigoDoErro(t, resp), nil
	}
	var r struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, "", r.RecoveryCodes
}

// temPendente diz se há troca em andamento. O segredo pendente e a família
// que o iniciou (revisão final do Plano 9) andam juntos: gravados no rotate,
// apagados em toda limpeza. Um sem o outro é erro do teste que chamou — assim
// toda checagem de limpeza confere também a família.
func temPendente(t *testing.T, a *api) bool {
	t.Helper()
	var sealed []byte
	var familia *uuid.UUID
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_pending_secret_enc, totp_pending_family_id FROM users WHERE id = $1`, a.userID).
		Scan(&sealed, &familia); err != nil {
		t.Fatal(err)
	}
	if (len(sealed) > 0) != (familia != nil) {
		t.Errorf("pendente e família descasados: segredo gravado=%v, família=%v", len(sealed) > 0, familia)
	}
	return len(sealed) > 0
}

func eventosDe(t *testing.T, a *api, acao string) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2`, a.userID, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// familiaComRefresh abre uma família pelo Manager (sem o balde do login) e
// devolve o access token dela, o refresh (para o logout) e o id.
func familiaComRefresh(t *testing.T, a *api) (token, refresh string, familia uuid.UUID) {
	t.Helper()
	refresh, s, err := gerente(a).Issue(context.Background(), a.userID, carimboDe(t, a, a.userID), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	token, err = auth.NewAccessToken(testJWTSecret, a.userID, s.FamilyID, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token, refresh, s.FamilyID
}

// entrarCom faz o login em dois passos com o código dado (do app ou de
// recuperação) e devolve o status do /totp/verify. Um segundo antes do login,
// pela marca d'água do desafio (Plano 8).
func entrarCom(t *testing.T, a *api, codigo string) int {
	t.Helper()
	a.clk.Advance(time.Second)
	c := clientWithJar(t)
	desafio := desafioAtual(t, a, c)
	resp := post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": codigo})
	defer resp.Body.Close()
	return resp.StatusCode
}

// As provas do rotate são as do disable: a senha E a posse do fator atual.
// Senha errada, código errado e código ausente são 401 invalid_credentials
// (D24 do Plano 5), cada um conta na família (A3 do Plano 10, e não no
// bloqueio da conta), e nada é gravado.
func TestRotateRefusesWrongProofsAndCountsThem(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	certo := codigoDe(t, a, segredo)
	errado := "000000"
	if errado == certo {
		errado = "111111"
	}

	for nome, corpo := range map[string]map[string]string{
		"senha errada":   {"password": "senha-que-nao-e-a-dele", "code": certo},
		"código errado":  {"password": testPass, "code": errado},
		"código ausente": {"password": testPass},
	} {
		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate", token, corpo)
		codigo := codigoDoErro(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
			t.Errorf("rotate com %s: %d %s, queria 401 invalid_credentials", nome, resp.StatusCode, codigo)
		}
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas no bloqueio da conta, queria 0 (A3)", n)
	}
	if temPendente(t, a) {
		t.Error("um rotate recusado gravou o segredo pendente")
	}
	if n := eventosDe(t, a, "totp.rotation_started"); n != 0 {
		t.Errorf("%d eventos totp.rotation_started depois de rotates recusados", n)
	}

	// A perna positiva, no mesmo cenário.
	if st, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": certo}); st != http.StatusOK || novo == "" {
		t.Fatalf("rotate com senha e código certos devolveu %d", st)
	}
	if !temPendente(t, a) {
		t.Error("o rotate aceito não gravou o segredo pendente")
	}
	if n := eventosDe(t, a, "totp.rotation_started"); n != 1 {
		t.Errorf("%d eventos totp.rotation_started depois do rotate aceito, queria 1", n)
	}
}

// Com o 2FA desligado não há o que trocar: 403 forbidden, o espelho do enroll
// com o 2FA ligado. A perna positiva é o 200 depois de ligar.
func TestRotateWithTOTPOffIsForbidden(t *testing.T) {
	a := newAPI(t)
	token := accessTokenValido(t, a)
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate", token,
		map[string]string{"password": testPass, "code": "123456"})
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("rotate sem 2FA: %d %s, queria 403 forbidden", resp.StatusCode, codigo)
	}
	if temPendente(t, a) {
		t.Error("o rotate sem 2FA gravou um pendente")
	}

	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	if st, _ := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)}); st != http.StatusOK {
		t.Errorf("rotate com o 2FA ligado devolveu %d, queria 200", st)
	}
}

// O código de recuperação vale no lugar do código do app, pelos dois nomes que
// o disable aceita (code e recovery_code), e fica gasto.
func TestRotateAcceptsARecoveryCode(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	cadastrarTOTP(t, a, c)
	token := accessTokenValido(t, a)
	rec := codigosDeRecuperacao(t, a, c)

	for campo, codigo := range map[string]string{"code": rec[0], "recovery_code": rec[1]} {
		if st, _ := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, campo: codigo}); st != http.StatusOK {
			t.Errorf("rotate com código de recuperação em %q devolveu %d, queria 200", campo, st)
		}
	}
	if _, _, codigos, _ := estadoDoSegundoFator(t, a); codigos != 8 {
		t.Errorf("sobraram %d códigos de recuperação, queria 8 (os dois usados ficam gastos)", codigos)
	}
}

// Entre o rotate e o confirm, o login usa o app ANTIGO; depois do confirm, só
// o NOVO. São cinco logins: o balde do login tem burst 5 por IP.
func TestOldAppWorksUntilTheRotationIsConfirmed(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)

	st, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	if st != http.StatusOK {
		t.Fatalf("rotate devolveu %d", st)
	}

	a.clk.Advance(30 * time.Second)
	if got := entrarCom(t, a, codigoDe(t, a, velho)); got != http.StatusOK {
		t.Errorf("antes do confirm, o app antigo deu %d no login, queria 200", got)
	}
	if got := entrarCom(t, a, codigoDe(t, a, novo)); got != http.StatusUnauthorized {
		t.Errorf("antes do confirm, o app novo deu %d no login, queria 401", got)
	}

	a.clk.Advance(30 * time.Second)
	if st, _, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo)); st != http.StatusOK {
		t.Fatalf("confirm devolveu %d", st)
	}

	a.clk.Advance(30 * time.Second)
	if got := entrarCom(t, a, codigoDe(t, a, velho)); got != http.StatusUnauthorized {
		t.Errorf("depois do confirm, o app antigo deu %d no login, queria 401", got)
	}
	if got := entrarCom(t, a, codigoDe(t, a, novo)); got != http.StatusOK {
		t.Errorf("depois do confirm, o app novo deu %d no login, queria 200", got)
	}
}

// Código errado no confirm: 401 invalid_credentials, conta na família (não no
// bloqueio da conta, A3 do Plano 10), e o pendente fica; um confirm certo
// depois funciona.
func TestRotateConfirmWrongCodeKeepsThePending(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	_, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	a.clk.Advance(30 * time.Second)

	// O código do app antigo é o erro mais provável.
	st, codigo, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, velho))
	if st != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("confirm com o app antigo: %d %s, queria 401 invalid_credentials", st, codigo)
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("%d falhas no bloqueio da conta, queria 0 (A3)", n)
	}
	if !temPendente(t, a) {
		t.Fatal("o confirm recusado apagou o pendente")
	}
	st, _, codigos := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo))
	if st != http.StatusOK || len(codigos) != 10 {
		t.Errorf("confirm certo depois de um errado: %d com %d códigos, queria 200 com 10", st, len(codigos))
	}
}

// Sem troca em andamento, o confirm é 403 forbidden; a perna positiva é o 200
// depois do rotate, no mesmo cenário.
func TestRotateConfirmWithoutPendingIsForbidden(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)

	st, codigo, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, segredo))
	if st != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("confirm sem pendente: %d %s, queria 403 forbidden", st, codigo)
	}
	if n := falhasDeLogin(t, a); n != 0 {
		t.Errorf("o confirm sem pendente contou %d falhas", n)
	}

	a.clk.Advance(30 * time.Second)
	_, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)})
	a.clk.Advance(30 * time.Second)
	if st, _, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo)); st != http.StatusOK {
		t.Errorf("confirm com pendente devolveu %d, queria 200", st)
	}
}

// Depois do confirm: os códigos de recuperação antigos não valem, os novos
// valem, as outras famílias caem e a atual fica (spec do Plano 9, §3.2).
func TestRotateConfirmReplacesCodesAndOtherSessions(t *testing.T) {
	a := newAPI(t)
	outroAparelho := clientWithJar(t)
	velho := cadastrarTOTP(t, a, outroAparelho) // a família do login fica neste jar
	antigos := codigosDeRecuperacao(t, a, outroAparelho)
	token := accessTokenValido(t, a)

	_, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	a.clk.Advance(30 * time.Second)
	st, _, novos := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo))
	if st != http.StatusOK || len(novos) != 10 {
		t.Fatalf("confirm: %d com %d códigos", st, len(novos))
	}
	if temPendente(t, a) {
		t.Error("o pendente sobreviveu à confirmação")
	}

	if renova(t, a, outroAparelho) {
		t.Error("a sessão do outro aparelho sobreviveu à troca de autenticador")
	}
	sessoes := listarSessoes(t, a, token) // 200 só se a família atual está viva
	if len(sessoes) != 1 || !sessoes[0].Current {
		t.Errorf("sessões vivas depois da troca: %+v; queria só a atual", sessoes)
	}

	if got := entrarCom(t, a, antigos[2]); got != http.StatusUnauthorized {
		t.Errorf("um código de recuperação antigo deu %d no login, queria 401", got)
	}
	if got := entrarCom(t, a, novos[0]); got != http.StatusOK {
		t.Errorf("um código de recuperação novo deu %d no login, queria 200", got)
	}

	var rotacoes int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'totp.rotated'`, a.userID).Scan(&rotacoes); err != nil {
		t.Fatal(err)
	}
	if rotacoes != 1 {
		t.Errorf("%d eventos totp.rotated, queria 1", rotacoes)
	}
}

// O disable apaga o pendente: uma troca abandonada não confirma depois que o
// 2FA é ligado de novo com outro app.
func TestDisableDropsThePendingRotationOverHTTP(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	_, abandonado := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})

	a.clk.Advance(30 * time.Second)
	if got := desativar(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)}); got != http.StatusNoContent {
		t.Fatalf("disable devolveu %d", got)
	}
	if temPendente(t, a) {
		t.Error("o disable deixou o segredo pendente")
	}

	a.clk.Advance(30 * time.Second)
	cadastrarTOTP(t, a, clientWithJar(t))
	st, codigo, _ := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, abandonado))
	if st != http.StatusForbidden || codigo != "forbidden" {
		t.Errorf("confirm da troca abandonada antes do disable: %d %s, queria 403 forbidden", st, codigo)
	}
}

// A conta bloqueada não impede o dono logado de trocar de autenticador (A3 do
// Plano 10; antes, era recusada com 401 invalid_credentials nas duas rotas): o
// bloqueio é do login. Cada perna confere o error.code, e não só o status
// (E9): o 401 de prova errada é invalid_credentials, o de sessão derrubada é
// unauthorized, e o painel reage a eles de formas opostas.
//
// A prova errada, com a conta bloqueada, não mexe no bloqueio (conta na
// família); as provas certas o zeram, no rotate e no confirm (A6 do Plano 10,
// revendo a D5 do Plano 9).
func TestRotateRoutesIgnoreTheAccountLock(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	errado := "000000"
	if errado == codigoDe(t, a, velho) {
		errado = "111111"
	}
	bloquearConta(t, a)

	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate", token,
		map[string]string{"password": testPass, "code": errado})
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("rotate com código errado e a conta bloqueada: %d %s, queria 401 invalid_credentials", resp.StatusCode, codigo)
	}
	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueado {
		t.Errorf("a prova errada mexeu no bloqueio da conta: %d falhas, bloqueada=%v", falhas, bloqueado)
	}

	st, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	if st != http.StatusOK {
		t.Fatalf("rotate com as provas certas e a conta bloqueada: %d, queria 200", st)
	}
	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != 0 || bloqueado {
		t.Errorf("o rotate aceito não zerou o bloqueio: %d falhas, bloqueada=%v", falhas, bloqueado)
	}

	a.clk.Advance(30 * time.Second)
	bloquearConta(t, a)
	st, codigo, _ = confirmarTrocaHTTP(t, a, token, codigoDe(t, a, velho))
	if st != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("confirm com o app antigo e a conta bloqueada: %d %s, queria 401 invalid_credentials", st, codigo)
	}
	if !temPendente(t, a) {
		t.Error("o confirm recusado apagou o pendente")
	}
	st, codigo, codigos := confirmarTrocaHTTP(t, a, token, codigoDe(t, a, novo))
	if st != http.StatusOK || len(codigos) != 10 {
		t.Errorf("confirm com o app novo e a conta bloqueada: %d %s, queria 200 com 10 códigos", st, codigo)
	}
	if falhas, bloqueado := estadoDoBloqueio(t, a); falhas != 0 || bloqueado {
		t.Errorf("o confirm aceito não zerou o bloqueio: %d falhas, bloqueada=%v", falhas, bloqueado)
	}
}

// O pendente é da família de sessão que o iniciou (revisão final do Plano 9).
// Outra sessão viva do dono não o confirma: nem com a dona viva, nem depois
// de a dona ser encerrada por DELETE /sessions/{id} ou pelo logout — antes,
// o pendente sobrevivia à sessão que o criou e qualquer outra o confirmava. É
// o 403 forbidden de "sem troca em andamento", conferido antes do código e sem
// contar falha. A perna
// positiva, no mesmo cenário: rotate e confirm na mesma família.
func TestRotationBelongsToTheFamilyThatStartedIt(t *testing.T) {
	for _, caso := range []string{"dona viva", "DELETE /sessions", "logout"} {
		t.Run(caso, func(t *testing.T) {
			a := newAPI(t)
			velho := cadastrarTOTP(t, a, clientWithJar(t))
			tokenA, refreshA, familiaA := familiaComRefresh(t, a)
			tokenB := accessTokenValido(t, a)

			st, novo := iniciarTrocaHTTP(t, a, tokenA, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
			if st != http.StatusOK {
				t.Fatalf("rotate devolveu %d", st)
			}

			switch caso {
			case "DELETE /sessions":
				resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+familiaA.String(), tokenB)
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("DELETE /sessions/{A} devolveu %d", resp.StatusCode)
				}
			case "logout":
				c := clientWithJar(t)
				u, err := url.Parse(a.srv.URL + "/api/v1/auth")
				if err != nil {
					t.Fatal(err)
				}
				c.Jar.SetCookies(u, []*http.Cookie{{Name: "vigilume_refresh", Value: refreshA, Path: "/api/v1/auth"}})
				resp := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("logout devolveu %d", resp.StatusCode)
				}
			}
			if caso != "dona viva" {
				if st := statusDaListaDeSessoes(t, a, tokenA); st != http.StatusUnauthorized {
					t.Fatalf("a família A continuou viva depois de %s (GET /sessions deu %d)", caso, st)
				}
			}

			a.clk.Advance(30 * time.Second)
			// Com um código errado (o do app antigo) também é 403, e não o 401
			// que conta no bloqueio: a família é conferida antes do código.
			st, codigo, _ := confirmarTrocaHTTP(t, a, tokenB, codigoDe(t, a, velho))
			if st != http.StatusForbidden || codigo != "forbidden" {
				t.Errorf("confirm pela família B com código errado (%s): %d %s, queria 403 forbidden", caso, st, codigo)
			}
			st, codigo, _ = confirmarTrocaHTTP(t, a, tokenB, codigoDe(t, a, novo))
			if st != http.StatusForbidden || codigo != "forbidden" {
				t.Errorf("confirm pela família B (%s): %d %s, queria 403 forbidden", caso, st, codigo)
			}
			if n := falhasDeLogin(t, a); n != 0 {
				t.Errorf("o confirm pela outra família contou %d falhas", n)
			}
			if n := eventosDe(t, a, "totp.rotated"); n != 0 {
				t.Errorf("%d eventos totp.rotated depois da recusa", n)
			}

			// A perna positiva: a família B inicia e confirma a sua troca.
			a.clk.Advance(30 * time.Second)
			st, novoDeB := iniciarTrocaHTTP(t, a, tokenB, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
			if st != http.StatusOK {
				t.Fatalf("rotate pela família B devolveu %d", st)
			}
			a.clk.Advance(30 * time.Second)
			if st, _, _ := confirmarTrocaHTTP(t, a, tokenB, codigoDe(t, a, novoDeB)); st != http.StatusOK {
				t.Errorf("rotate e confirm na mesma família: confirm devolveu %d, queria 200", st)
			}
		})
	}
}

func statusDaListaDeSessoes(t *testing.T, a *api, token string) int {
	t.Helper()
	resp := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	resp.Body.Close()
	return resp.StatusCode
}

// O rotate reconfere a família sob a trava do usuário: uma revogação (aqui, a
// de uma troca de senha em outro aparelho) que comita enquanto ele espera a
// trava faz o rotate devolver 401 unauthorized — o da guarda da rota — sem
// gravar o pendente. Antes, o pendente de quem a troca de senha estava
// expulsando era gravado depois dela. A perna positiva é o mesmo rotate com
// uma família viva, sem ninguém segurando a trava.
func TestRotateRechecksTheFamilyUnderTheLock(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token, _, familia := familiaComRefresh(t, a)

	conn, err := a.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chave := "hashtextextended('vigilume.sessions:' || '" + a.userID.String() + "', 0)"
	var umaVez sync.Once
	soltar := func() {
		umaVez.Do(func() {
			if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chave+`)`); err != nil {
				t.Error(err)
			}
			conn.Release()
		})
	}
	t.Cleanup(soltar)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chave+`)`); err != nil {
		t.Fatal(err)
	}

	// A requisição é montada aqui, e a goroutine só a envia e lê a resposta:
	// nada de t.Fatal fora da goroutine do teste (ele não pararia o teste, e
	// o <-fim abaixo esperaria até o timeout). As asserções ficam depois do
	// <-fim.
	type resposta struct {
		status int
		corpo  []byte
		err    error
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/totp/rotate",
		jsonBody(t, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "vigilume")
	req.Header.Set("Authorization", "Bearer "+token)
	fim := make(chan resposta, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fim <- resposta{err: err}
			return
		}
		defer resp.Body.Close()
		corpo, err := io.ReadAll(resp.Body)
		fim <- resposta{status: resp.StatusCode, corpo: corpo, err: err}
	}()

	esperou := false
	for prazo := time.Now().Add(5 * time.Second); time.Now().Before(prazo); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := a.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			esperou = true
			break
		}
	}
	// A revogação comita enquanto o rotate espera (a troca de senha faria o
	// mesmo sob a trava; aqui, direto no banco).
	if _, err := a.pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`,
		familia, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	soltar()
	r := <-fim
	if r.err != nil {
		t.Fatalf("rotate: %v", r.err)
	}
	var env envelope
	codigo := ""
	if r.status != http.StatusOK {
		if err := json.Unmarshal(r.corpo, &env); err != nil {
			t.Fatalf("rotate %d sem envelope de erro: %q", r.status, r.corpo)
		}
		codigo = env.Error.Code
	}
	if !esperou {
		t.Fatalf("o rotate não esperou a trava do usuário (terminou com %d %s)", r.status, codigo)
	}
	if r.status != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("rotate com a família revogada sob a trava: %d %s, queria 401 unauthorized", r.status, codigo)
	}
	if temPendente(t, a) {
		t.Error("o rotate com a família revogada gravou o pendente")
	}
	if n := eventosDe(t, a, "totp.rotation_started"); n != 0 {
		t.Errorf("%d eventos totp.rotation_started com a família revogada", n)
	}

	// A perna positiva: outra família, viva, sem trava segura.
	a.clk.Advance(30 * time.Second)
	if st, _ := iniciarTrocaHTTP(t, a, accessTokenValido(t, a), map[string]string{"password": testPass, "code": codigoDe(t, a, velho)}); st != http.StatusOK {
		t.Errorf("rotate com a família viva devolveu %d, queria 200", st)
	}
}
