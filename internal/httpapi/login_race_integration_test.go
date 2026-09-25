package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

// Um login que verificou a senha ANTIGA não pode deixar uma sessão viva
// depois de um set-password (D10: "todas as sessões") ou de uma troca de
// senha (que encerra as outras). As duas redefinições pegam a trava do
// usuário (D1) e trocam o hash; Issue pega a mesma trava e reconfere o hash
// verificado (Task 11 do Plano 5).
//
// Cada caso roda nas duas ordens possíveis, e cada ordem trava uma metade do
// conserto:
//   - "redefinição primeiro": o login verificou a senha e está a caminho de
//     Issue quando a redefinição comita. Sem a reconferência do hash, a
//     sessão nasce da senha antiga e sobrevive.
//   - "sessão primeiro": Issue já inseriu a sessão e não comitou quando a
//     redefinição chega. Sem a trava em Issue, a redefinição não espera, não
//     enxerga a sessão ainda não comitada, e ela sobrevive.

// redefinicao é o que corre contra o login: o set-password do CLI ou a troca
// de senha pela API, a partir de outro aparelho.
type redefinicao struct {
	nome string
	// preparar roda antes da corrida (a troca precisa de uma sessão sua).
	preparar func(t *testing.T, a *api) (rodar func() error, vivasDepois int)
}

var redefinicoes = []redefinicao{
	{"set-password", func(t *testing.T, a *api) (func() error, int) {
		svc := account.NewService(a.pool, nil, a.clk, nil)
		return func() error {
			_, err := svc.ResetPassword(context.Background(), testEmail, senhaNova)
			return err
		}, 0
	}},
	{"troca de senha", func(t *testing.T, a *api) (func() error, int) {
		_, token := entrar(t, a)
		return func() error {
			resp, err := postSemT(http.DefaultClient, a.srv.URL+"/api/v1/auth/password", token,
				map[string]string{"current_password": testPass, "new_password": senhaNova})
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				return fmt.Errorf("a troca devolveu %d", resp.StatusCode)
			}
			return nil
		}, 1 // a sessão de quem trocou continua viva
	}},
}

// postSemT é o POST dos helpers sem o *testing.T: roda em goroutine, onde
// t.Fatal não pode ser chamado. token vazio é requisição sem Authorization.
func postSemT(c *http.Client, url, token string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "vigilume")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.Do(req)
}

// familiasVivas conta as famílias com a linha corrente viva.
func familiasVivas(t *testing.T, a *api) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(DISTINCT family_id) FROM sessions
		  WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $2`,
		a.userID, a.clk.Now()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// esperarBloqueados espera n backends deste banco esperando uma trava (de
// linha, de transação ou advisory). Devolve false se não chegarem em 5 s.
func esperarBloqueados(t *testing.T, pool *pgxpool.Pool, n int) bool {
	t.Helper()
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		var m int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&m); err != nil {
			t.Fatal(err)
		}
		if m >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// travarLinhaDoUsuario segura a linha de users com FOR UPDATE numa transação
// do teste: todo UPDATE nela espera. Devolve a função que solta (idempotente;
// o t.Cleanup solta se o teste morrer antes, para nada ficar pendurado).
func travarLinhaDoUsuario(t *testing.T, a *api) func() {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var umaVez sync.Once
	soltar := func() { umaVez.Do(func() { _ = tx.Rollback(ctx) }) }
	t.Cleanup(soltar)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, a.userID); err != nil {
		t.Fatal(err)
	}
	return soltar
}

// loginAssincrono faz o login num aparelho novo, numa goroutine. Um erro de
// transporte chega como resposta nil.
func loginAssincrono(t *testing.T, a *api) (*http.Client, <-chan *http.Response) {
	c := clientWithJar(t)
	fim := make(chan *http.Response, 1)
	go func() {
		resp, err := postSemT(c, a.srv.URL+"/api/v1/auth/login", "",
			map[string]string{"email": testEmail, "password": testPass})
		if err != nil {
			resp = nil
		}
		fim <- resp
	}()
	return c, fim
}

// respostaDoLogin espera o login assíncrono.
func respostaDoLogin(t *testing.T, fim <-chan *http.Response) *http.Response {
	t.Helper()
	resp := <-fim
	if resp == nil {
		t.Fatal("o login falhou no transporte")
	}
	return resp
}

// Redefinição primeiro. A ordem é imposta pelo banco, sem gancho no código: a
// linha do usuário fica travada pelo teste; a redefinição chega ao UPDATE do
// hash (já com a trava do usuário) e espera; o login confere a senha antiga e
// para no UPDATE que zera as falhas, logo antes de Issue. Soltas, as duas
// seguem na ordem da fila: a redefinição comita, e só então o login chega a
// Issue.
func TestLoginRacingAPasswordResetThatCommitsFirst(t *testing.T) {
	for _, r := range redefinicoes {
		t.Run(r.nome, func(t *testing.T) {
			a := newAPI(t)
			redefinir, vivas := r.preparar(t, a)

			soltar := travarLinhaDoUsuario(t, a)
			fimRedef := make(chan error, 1)
			go func() { fimRedef <- redefinir() }()
			if !esperarBloqueados(t, a.pool, 1) {
				t.Fatal("a redefinição não chegou ao UPDATE do hash")
			}
			_, fimLogin := loginAssincrono(t, a)
			if !esperarBloqueados(t, a.pool, 2) {
				t.Fatal("o login não chegou ao UPDATE de antes da sessão")
			}
			soltar()

			if err := <-fimRedef; err != nil {
				t.Fatalf("a redefinição falhou: %v", err)
			}
			resp := respostaDoLogin(t, fimLogin)
			codigo := codigoDoErro(t, resp)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
				t.Errorf("login com a senha antiga depois da redefinição: %d %s; queria 401 invalid_credentials",
					resp.StatusCode, codigo)
			}
			if refreshCookie(resp) != nil {
				t.Error("o login recusado entregou cookie de refresh")
			}
			if n := familiasVivas(t, a); n != vivas {
				t.Errorf("%d sessões vivas, queria %d: a sessão aberta pela senha antiga sobreviveu à redefinição", n, vivas)
			}
		})
	}
}

// storeQueSegura segura a transação de Issue logo depois de inserir a sessão,
// antes do commit, quando armado. Rotate também insere, então só a primeira
// inserção depois de armar é segurada.
type storeQueSegura struct {
	inner  auth.Store
	armado *atomic.Bool
	travou chan struct{}
	solta  chan struct{}
}

func (s storeQueSegura) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		return fn(ctx, txQueSegura{Tx: tx, s: s})
	})
}

type txQueSegura struct {
	auth.Tx
	s storeQueSegura
}

func (t txQueSegura) InsertSession(ctx context.Context, r auth.SessionRow) error {
	err := t.Tx.InsertSession(ctx, r)
	if t.s.armado.CompareAndSwap(true, false) {
		close(t.s.travou)
		<-t.s.solta
	}
	return err
}

// Sessão primeiro. O login insere a sessão e fica parado antes do commit; a
// redefinição tem de esperar por ele na trava do usuário (e a espera aparece
// no pg_stat_activity); solto, o login comita, e a redefinição, que só então
// começa a revogar, encerra a sessão nova junto com as outras.
func TestLoginRacingAPasswordResetThatCommitsSecond(t *testing.T) {
	for _, r := range redefinicoes {
		t.Run(r.nome, func(t *testing.T) {
			var armado atomic.Bool
			travou, solta := make(chan struct{}), make(chan struct{})
			var umaVez sync.Once
			soltar := func() { umaVez.Do(func() { close(solta) }) }
			a := newAPICom(t, nil, func(d *httpapi.Deps) {
				d.Sessions = auth.NewManager(
					storeQueSegura{inner: d.Store, armado: &armado, travou: travou, solta: solta}, d.Clock, testIPHashKey)
			})
			// Registrado depois do newAPICom, roda antes do fechamento do
			// servidor e do pool: um login parado não os deixa esperando.
			t.Cleanup(soltar)
			redefinir, vivas := r.preparar(t, a)

			armado.Store(true)
			c, fimLogin := loginAssincrono(t, a)
			select {
			case <-travou:
			case <-time.After(10 * time.Second):
				t.Fatal("o login nunca chegou à inserção da sessão")
			}
			fimRedef := make(chan error, 1)
			go func() { fimRedef <- redefinir() }()
			esperou := esperarBloqueados(t, a.pool, 1)
			soltar()

			resp := respostaDoLogin(t, fimLogin)
			resp.Body.Close()
			if err := <-fimRedef; err != nil {
				t.Fatalf("a redefinição falhou: %v", err)
			}
			if !esperou {
				t.Error("a redefinição não esperou o login que estava abrindo sessão (Issue sem a trava do usuário)")
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login devolveu %d; a senha ainda era a certa quando ele abriu a sessão", resp.StatusCode)
			}
			if renova(t, a, c) {
				t.Error("a sessão aberta pela senha antiga sobreviveu à redefinição")
			}
			if n := familiasVivas(t, a); n != vivas {
				t.Errorf("%d sessões vivas, queria %d", n, vivas)
			}
		})
	}
}

// O rehash do login (parâmetros do Argon2id mais fracos que os de hoje) não
// pode desfazer um set-password concorrente. O banco impõe a ordem, como no
// "redefinição primeiro": o set-password espera no UPDATE do hash, o login
// confere a senha antiga e espera no UPDATE do rehash, atrás dele. Antes, o
// rehash gravava sem condição o hash da senha ANTIGA por cima da redefinição,
// e a senha antiga voltava a valer.
func TestLoginRehashDoesNotUndoAPasswordReset(t *testing.T) {
	a := newAPI(t)
	fraco, err := auth.HashPasswordWith(testPass, []byte("sal-de-16-bytes!"), 8*1024, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET password_hash = $2 WHERE id = $1`, a.userID, fraco); err != nil {
		t.Fatal(err)
	}
	if _, precisa, err := auth.VerifyPassword(context.Background(), fraco, testPass); err != nil || !precisa {
		t.Fatalf("o hash fraco não pede rehash (%v): o teste não exercitaria o caminho", err)
	}
	svc := account.NewService(a.pool, nil, a.clk, nil)

	soltar := travarLinhaDoUsuario(t, a)
	fimRedef := make(chan error, 1)
	go func() {
		_, err := svc.ResetPassword(context.Background(), testEmail, senhaNova)
		fimRedef <- err
	}()
	if !esperarBloqueados(t, a.pool, 1) {
		t.Fatal("o set-password não chegou ao UPDATE do hash")
	}
	_, fimLogin := loginAssincrono(t, a)
	if !esperarBloqueados(t, a.pool, 2) {
		t.Fatal("o login não chegou ao UPDATE do rehash")
	}
	soltar()

	if err := <-fimRedef; err != nil {
		t.Fatalf("o set-password falhou: %v", err)
	}
	resp := respostaDoLogin(t, fimLogin)
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()

	if senhaGravadaE(t, a, testPass) || !senhaGravadaE(t, a, senhaNova) {
		t.Error("o rehash do login desfez o set-password: a senha antiga voltou a valer")
	}
	if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("login com a senha antiga: %d %s; queria 401 invalid_credentials", resp.StatusCode, codigo)
	}
	if n := familiasVivas(t, a); n != 0 {
		t.Errorf("%d sessões vivas depois do set-password", n)
	}
}

// Com segundo fator, a senha é verificada no login e a sessão só nasce no
// /totp/verify, até cinco minutos depois (ChallengeTTL). O desafio obtido com a
// senha antiga não pode abrir sessão depois de um set-password, nem com o
// código certo: ele leva o carimbo do hash verificado, e Issue o reconfere.
// Não é corrida de milissegundos: a janela é o prazo inteiro do desafio.
func TestTOTPChallengeFromTheOldPasswordDiesWithTheReset(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)

	if _, err := account.NewService(a.pool, nil, a.clk, nil).ResetPassword(context.Background(), testEmail, senhaNova); err != nil {
		t.Fatal(err)
	}

	code, err := totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	outro := clientWithJar(t)
	resp := post(t, outro, a.srv.URL+"/api/v1/auth/totp/verify", map[string]string{"challenge": desafio, "code": code})
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || codigo != "invalid_credentials" {
		t.Errorf("verify com o desafio da senha antiga: %d %s; queria 401 invalid_credentials", resp.StatusCode, codigo)
	}
	if n := familiasVivas(t, a); n != 0 {
		t.Errorf("%d sessões vivas: o desafio da senha antiga abriu sessão depois do set-password", n)
	}

	// O contraponto: um desafio novo, da senha nova, abre (a recusa acima não
	// vem de o verify recusar tudo).
	a.clk.Advance(30 * time.Second)
	novo := login(t, a, outro, testEmail, senhaNova)
	var d struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(novo.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	novo.Body.Close()
	code, err = totp.GenerateCode(segredo, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := post(t, outro, a.srv.URL+"/api/v1/auth/totp/verify", map[string]string{"challenge": d.Challenge, "code": code})
	ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Errorf("verify com o desafio da senha nova: %d, queria 200", ok.StatusCode)
	}
}
