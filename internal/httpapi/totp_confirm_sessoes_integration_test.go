package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// iniciarCadastroHTTP faz o enroll com o token dado e devolve o segredo base32.
func iniciarCadastroHTTP(t *testing.T, a *api, token string) string {
	t.Helper()
	er := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/enroll", token, map[string]string{"password": testPass})
	defer er.Body.Close()
	if er.StatusCode != http.StatusOK {
		t.Fatalf("enroll devolveu %d", er.StatusCode)
	}
	var enroll struct {
		URI string `json:"otpauth_uri"`
	}
	if err := json.NewDecoder(er.Body).Decode(&enroll); err != nil {
		t.Fatal(err)
	}
	key, err := otp.NewKeyFromURL(enroll.URI)
	if err != nil {
		t.Fatal(err)
	}
	return key.Secret()
}

// ligarPelaSessao cadastra e confirma o 2FA com o token dado e devolve o
// segredo. Ligar encerra as OUTRAS sessões, então o teste que segue usando o
// token liga por ele, e não pelo cadastrarTOTP (que abre uma sessão nova).
func ligarPelaSessao(t *testing.T, a *api, token string) string {
	t.Helper()
	segredo := iniciarCadastroHTTP(t, a, token)
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", token,
		map[string]string{"code": codigoDe(t, a, segredo)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmar o 2FA devolveu %d", resp.StatusCode)
	}
	var confirmado struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&confirmado); err != nil {
		t.Fatal(err)
	}
	a.recuperacao = confirmado.RecoveryCodes
	// Como no cadastrarTOTP: o timestep da confirmação já entra consumido.
	a.clk.Advance(30 * time.Second)
	return segredo
}

// Ligar o 2FA derruba as outras sessões do usuário e mantém a de quem ligou.
func TestTOTPConfirmRevokesTheOtherSessions(t *testing.T) {
	a := newAPI(t)
	atual, _, _ := familiaComRefresh(t, a)
	outra, _, _ := familiaComRefresh(t, a)
	segredo := iniciarCadastroHTTP(t, a, atual)

	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", atual,
		map[string]string{"code": codigoDe(t, a, segredo)})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm devolveu %d", resp.StatusCode)
	}
	if st := statusEmMe(t, a, atual); st != http.StatusOK {
		t.Errorf("a sessão que ligou o 2FA deu %d em /me, queria 200", st)
	}
	if st := statusEmMe(t, a, outra); st != http.StatusUnauthorized {
		t.Errorf("a outra sessão deu %d em /me depois de ligar o 2FA, queria 401", st)
	}
}

// O confirm reconfere a família sob a trava do usuário: uma revogação que
// comita enquanto ele espera a trava faz o confirm devolver o 401 unauthorized
// da guarda da rota, sem ligar o 2FA. A perna positiva é o mesmo código com
// uma família viva, sem ninguém segurando a trava.
func TestTOTPConfirmRechecksTheFamilyUnderTheLock(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	token, _, familia := familiaComRefresh(t, a)
	segredo := iniciarCadastroHTTP(t, a, token)

	conn, err := a.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chave := "hashtextextended('stillon.sessions:' || '" + a.userID.String() + "', 0)"
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

	// A goroutine só envia e lê: nada de t.Fatal fora da goroutine do teste.
	type resposta struct {
		status int
		corpo  []byte
		err    error
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/totp/confirm",
		jsonBody(t, map[string]string{"code": codigoDe(t, a, segredo)}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
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
	if _, err := testdb.Admin(t).Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`,
		familia, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	soltar()
	r := <-fim
	if r.err != nil {
		t.Fatalf("confirm: %v", r.err)
	}
	var env envelope
	codigo := ""
	if r.status != http.StatusOK {
		if err := json.Unmarshal(r.corpo, &env); err != nil {
			t.Fatalf("confirm %d sem envelope de erro: %q", r.status, r.corpo)
		}
		codigo = env.Error.Code
	}
	if !esperou {
		t.Fatalf("o confirm não esperou a trava do usuário (terminou com %d %s)", r.status, codigo)
	}
	if r.status != http.StatusUnauthorized || codigo != "unauthorized" {
		t.Errorf("confirm com a família revogada sob a trava: %d %s, queria 401 unauthorized", r.status, codigo)
	}
	if n := eventosDe(t, a, "totp.enabled"); n != 0 {
		t.Errorf("%d eventos totp.enabled com a família revogada", n)
	}

	// A perna positiva: outra família, viva, sem trava segura; o mesmo código
	// confirma, porque a tentativa recusada não consumiu o timestep.
	viva := accessTokenValido(t, a)
	resp := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", viva,
		map[string]string{"code": codigoDe(t, a, segredo)})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("confirm com a família viva devolveu %d, queria 200", resp.StatusCode)
	}
}
