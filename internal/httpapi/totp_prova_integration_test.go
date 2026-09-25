package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// As provas do 2FA pelo HTTP depois do Plano 10: o código do login não
// desliga o 2FA (A4), a sessão só nasce com o segredo que aceitou o código
// (A4), o confirm exige o rotation da troca que conhece (A7), o código de
// recuperação digitado à mão vale (A8) e o usuário que some no meio é 401
// unauthorized (A10).

// resposta é o que uma requisição feita numa goroutine devolve ao teste.
type resposta struct {
	status int
	codigo string
	err    error
}

// postAssincrono manda o POST numa goroutine e entrega status e code do
// envelope no canal. A requisição é montada aqui, e as asserções ficam com
// quem lê o canal.
func postAssincrono(t *testing.T, url, token string, corpo any) <-chan resposta {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, jsonBody(t, corpo))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "vigilume")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	fim := make(chan resposta, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fim <- resposta{err: err}
			return
		}
		defer resp.Body.Close()
		corpo, err := io.ReadAll(resp.Body)
		var env envelope
		_ = json.Unmarshal(corpo, &env)
		fim <- resposta{status: resp.StatusCode, codigo: env.Error.Code, err: err}
	}()
	return fim
}

// prenderTravaDeSessoes toma a trava de sessões do usuário numa conexão do
// teste e devolve a função que a solta (idempotente; o Cleanup solta se o
// teste morrer antes).
func prenderTravaDeSessoes(t *testing.T, a *api) func() {
	t.Helper()
	ctx := context.Background()
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
	return soltar
}

// A4 (auditoria S4): o código do app que abriu o login não desliga o 2FA na
// mesma janela — o disable consome o timestep. O do período seguinte desliga.
func TestLoginCodeCannotDisableTheSecondFactor(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	codigo := codigoDe(t, a, segredo)
	if st := entrarCom(t, a, codigo); st != http.StatusOK {
		t.Fatalf("login com o código devolveu %d", st)
	}
	token := accessTokenValido(t, a)

	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/disable", token,
		map[string]string{"password": testPass, "code": codigo})
	got := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || got != "invalid_credentials" {
		t.Errorf("disable com o código do login: %d %s, queria 401 invalid_credentials", resp.StatusCode, got)
	}
	if confirmado, _, _, _ := estadoDoSegundoFator(t, a); !confirmado {
		t.Fatal("o código já usado no login desligou o 2FA")
	}

	a.clk.Advance(30 * time.Second)
	resp = postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/disable", token,
		map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("disable com o código do período seguinte: %d, queria 204", resp.StatusCode)
	}
}

// A4: a sessão depois do /totp/verify só nasce se o segredo que aceitou o
// código ainda é o ativo, conferido sob a trava do usuário. A trava fica
// presa pelo teste enquanto o verify, com o código já aceito e o desafio já
// consumido, espera para abrir a sessão; nesse intervalo o segredo muda (uma
// troca de autenticador) ou some (um disable). O verify responde 401
// invalid_credentials e nenhuma sessão nasce. A perna positiva: nada muda no
// intervalo, e a sessão nasce.
func TestSecondFactorProofDiesWithTheRotation(t *testing.T) {
	casos := []struct {
		nome   string
		mudar  string
		status int
		codigo string
	}{
		{"nada muda", ``, http.StatusOK, ""},
		{"troca de autenticador", `UPDATE users SET totp_secret_enc = totp_secret_enc || '\x00'::bytea WHERE id = $1`,
			http.StatusUnauthorized, "invalid_credentials"},
		{"disable", `UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL WHERE id = $1`,
			http.StatusUnauthorized, "invalid_credentials"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := newAPI(t)
			segredo := cadastrarTOTP(t, a, clientWithJar(t))
			a.clk.Advance(time.Second)
			desafio := desafioAtual(t, a, clientWithJar(t))
			antes := familiasVivas(t, a)

			soltar := prenderTravaDeSessoes(t, a)
			fim := postAssincrono(t, a.srv.URL+"/api/v1/auth/totp/verify", "",
				map[string]string{"challenge": desafio, "code": codigoDe(t, a, segredo)})
			esperou := esperarBloqueados(t, a.pool, 1)
			if caso.mudar != "" {
				if _, err := a.pool.Exec(context.Background(), caso.mudar, a.userID); err != nil {
					t.Fatal(err)
				}
			}
			soltar()
			r := <-fim
			if r.err != nil {
				t.Fatal(r.err)
			}
			if !esperou {
				t.Fatalf("o verify não esperou a trava do usuário (terminou com %d %s)", r.status, r.codigo)
			}
			if r.status != caso.status || r.codigo != caso.codigo {
				t.Errorf("verify: %d %s, queria %d %s", r.status, r.codigo, caso.status, caso.codigo)
			}
			novas := familiasVivas(t, a) - antes
			if quer := map[bool]int{true: 1, false: 0}[caso.status == http.StatusOK]; novas != quer {
				t.Errorf("%d famílias novas, queria %d", novas, quer)
			}
		})
	}
}

// A7: o rotate devolve rotation (32 caracteres hex, um por troca), e o
// rotate/confirm exige o da troca vigente. O de uma troca que outro rotate da
// MESMA família substituiu, ou nenhum, é o 403 forbidden de "sem troca",
// antes do código: não conta como prova errada (cinco seguidos não derrubam a
// família) e o pendente fica.
func TestRotateConfirmNeedsTheRotationItKnows(t *testing.T) {
	a := newAPI(t)
	velho := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)

	st, primeiro := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	if st != http.StatusOK {
		t.Fatalf("primeiro rotate: %d", st)
	}
	rot1 := a.rotacao
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(rot1) {
		t.Fatalf("rotation = %q, queria 32 caracteres hex", rot1)
	}
	a.clk.Advance(30 * time.Second)
	st, segundo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, velho)})
	if st != http.StatusOK || a.rotacao == rot1 {
		t.Fatalf("segundo rotate: %d, rotation igual ao primeiro: %v", st, a.rotacao == rot1)
	}

	confirmar := func(rot, codigo string) (int, string) {
		t.Helper()
		corpo := map[string]string{"code": codigo}
		if rot != "-" {
			corpo["rotation"] = rot
		}
		resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate/confirm", token, corpo)
		defer resp.Body.Close()
		return resp.StatusCode, codigoDoErro(t, resp)
	}
	for i := range provasPorFamilia {
		rot := rot1
		if i == 0 {
			rot = "-" // sem o campo
		}
		if st, codigo := confirmar(rot, codigoDe(t, a, primeiro)); st != http.StatusForbidden || codigo != "forbidden" {
			t.Fatalf("confirm %d com o rotation da troca substituída (ou sem ele): %d %s, queria 403 forbidden", i+1, st, codigo)
		}
	}
	if !temPendente(t, a) {
		t.Fatal("o confirm recusado apagou o pendente")
	}
	if st, codigo := confirmar(a.rotacao, codigoDe(t, a, segundo)); st != http.StatusOK {
		t.Errorf("confirm com o rotation da troca vigente: %d %s, queria 200", st, codigo)
	}
}

// A8: o código de recuperação vale como o dono o digitar — em minúsculas, com
// hífen e espaços — no segundo passo do login.
func TestRecoveryCodeIsNormalizedAtLogin(t *testing.T) {
	a := newAPI(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	c := a.recuperacao[0]
	digitado := " " + strings.ToLower(c[:4]) + "-" + c[4:8] + " " + strings.ToLower(c[8:])
	if st := entrarCom(t, a, digitado); st != http.StatusOK {
		t.Errorf("login com o código de recuperação %q digitado como %q: %d, queria 200", c, digitado, st)
	}
}

// A10: o usuário que some no meio de uma rota do 2FA (depois da guarda da
// sessão viva, antes do handler ler a conta) é 401 unauthorized, como no /me
// e na troca de senha, e não 500. A tabela users fica travada pelo teste, a
// rota para na primeira leitura dela, o usuário é apagado e a trava solta.
func TestTOTPRoutesAnswerUnauthorizedWhenTheUserVanishes(t *testing.T) {
	casos := []struct {
		rota     string
		ligar    bool
		trocando bool
		corpo    func(a *api, segredo string) map[string]string
	}{
		{"enroll", false, false, func(*api, string) map[string]string { return map[string]string{"password": testPass} }},
		{"confirm", false, false, func(*api, string) map[string]string { return map[string]string{"code": "000000"} }},
		{"disable", true, false, func(a *api, s string) map[string]string {
			return map[string]string{"password": testPass, "code": "000000"}
		}},
		{"rotate", true, false, func(a *api, s string) map[string]string {
			return map[string]string{"password": testPass, "code": "000000"}
		}},
		{"rotate/confirm", true, true, func(a *api, s string) map[string]string {
			return map[string]string{"code": "000000", "rotation": a.rotacao}
		}},
	}
	for _, caso := range casos {
		t.Run(caso.rota, func(t *testing.T) {
			a := newAPI(t)
			ctx := context.Background()
			segredo := ""
			if caso.ligar {
				segredo = cadastrarTOTP(t, a, clientWithJar(t))
			}
			token := accessTokenValido(t, a)
			if caso.trocando {
				if st, _ := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)}); st != http.StatusOK {
					t.Fatalf("rotate: %d", st)
				}
			}

			tx, err := a.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `LOCK TABLE users IN ACCESS EXCLUSIVE MODE`); err != nil {
				t.Fatal(err)
			}
			fim := postAssincrono(t, a.srv.URL+"/api/v1/auth/totp/"+caso.rota, token, caso.corpo(a, segredo))
			esperou := esperarBloqueados(t, a.pool, 1)
			if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, a.userID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			r := <-fim
			if r.err != nil {
				t.Fatal(r.err)
			}
			if !esperou {
				t.Fatalf("a rota não parou na tabela users (terminou com %d %s)", r.status, r.codigo)
			}
			if r.status != http.StatusUnauthorized || r.codigo != "unauthorized" {
				t.Errorf("%s com o usuário apagado no meio: %d %s, queria 401 unauthorized", caso.rota, r.status, r.codigo)
			}
		})
	}
}
