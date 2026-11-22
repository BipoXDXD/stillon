package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// storeQueSeguraAntesDaTrava pausa somente o próximo Issue antes da trava
// real. O login já consultou IsEnabled=false e zerou as falhas, mas ainda não
// pode impedir o Confirm de comitar. As leituras e escritas continuam no PG.
type storeQueSeguraAntesDaTrava struct{ storeQueSegura }

func (s storeQueSeguraAntesDaTrava) InTx(ctx context.Context, fn func(context.Context, auth.Tx) error) error {
	return s.inner.InTx(ctx, func(ctx context.Context, tx auth.Tx) error {
		return fn(ctx, txQueSeguraAntesDaTrava{Tx: tx, s: s.storeQueSegura})
	})
}

type txQueSeguraAntesDaTrava struct {
	auth.Tx
	s storeQueSegura
}

func (t txQueSeguraAntesDaTrava) LockUserSessions(ctx context.Context, id uuid.UUID) error {
	if t.s.armado.CompareAndSwap(true, false) {
		close(t.s.travou)
		select {
		case <-t.s.solta:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return t.Tx.LockUserSessions(ctx, id)
}

// Confirm primeiro: o login viu o fator desligado, mas só tenta emitir sua
// família depois do commit que o ligou. A resposta deve ser o 401 genérico,
// sem cookie, token ou família nova. A família do dono permanece viva.
func TestLoginRacingTOTPConfirmationThatCommitsFirst(t *testing.T) {
	var armado atomic.Bool
	travou, solta := make(chan struct{}), make(chan struct{})
	var umaVez sync.Once
	soltar := func() { umaVez.Do(func() { close(solta) }) }
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Sessions = auth.NewManager(storeQueSeguraAntesDaTrava{storeQueSegura{
			inner: d.Store, armado: &armado, travou: travou, solta: solta,
		}}, d.Clock, testIPHashKey)
	})
	t.Cleanup(soltar)
	_, dono := entrar(t, a)
	segredo := iniciarCadastroHTTP(t, a, dono)
	// Duas falhas antes: a senha certa recusada na emissão não pode zerá-las.
	errarSenha(t, a, 2)
	armado.Store(true)
	_, fimLogin := loginAssincrono(t, a)
	select {
	case <-travou:
	case <-time.After(10 * time.Second):
		t.Fatal("o login não chegou à emissão depois de consultar o fator desligado")
	}

	confirm := postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", dono,
		map[string]string{"code": codigoDe(t, a, segredo)})
	confirm.Body.Close()
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirm devolveu %d", confirm.StatusCode)
	}
	soltar()
	resp := respostaDoLogin(t, fimLogin)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login sem fator após confirmação: %d, queria 401", resp.StatusCode)
	} else if codigo := codigoDoErro(t, resp); codigo != "invalid_credentials" {
		t.Errorf("código = %s, queria invalid_credentials", codigo)
	}
	if cookies := resp.Cookies(); len(cookies) != 0 {
		t.Errorf("login recusado entregou %d cookies", len(cookies))
	}
	if n := familiasVivas(t, a); n != 1 {
		t.Errorf("%d famílias vivas, queria só a do dono", n)
	}
	var inseridas int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, a.userID).Scan(&inseridas); err != nil {
		t.Fatal(err)
	}
	if inseridas != 1 {
		t.Errorf("%d sessões gravadas: o login recusado não pode criar família", inseridas)
	}
	// O login não terminou: a reserva volta, sem zerar o contador.
	if falhas, _ := estadoDoBloqueio(t, a); falhas != 2 {
		t.Errorf("failed_attempts = %d depois do login recusado na emissão, queria 2", falhas)
	}
	if st := statusEmMe(t, a, dono); st != http.StatusOK {
		t.Errorf("a sessão do dono deu %d em /me, queria 200", st)
	}
	// O caminho com prova continua funcionando depois da recusa.
	a.clk.Advance(30 * time.Second)
	if st := entrarCom(t, a, codigoDe(t, a, segredo)); st != http.StatusOK {
		t.Errorf("login com segundo fator deu %d, queria 200", st)
	}
}

// Issue primeiro: a família ainda não comitada precisa segurar o Confirm na
// trava do usuário. Quando ele prossegue, a revogação alcança a família nova.
func TestLoginRacingTOTPConfirmationThatCommitsSecond(t *testing.T) {
	var armado atomic.Bool
	travou, solta := make(chan struct{}), make(chan struct{})
	var umaVez sync.Once
	soltar := func() { umaVez.Do(func() { close(solta) }) }
	a := newAPICom(t, nil, func(d *httpapi.Deps) {
		d.Sessions = auth.NewManager(storeQueSegura{
			inner: d.Store, armado: &armado, travou: travou, solta: solta,
		}, d.Clock, testIPHashKey)
	})
	t.Cleanup(soltar)
	_, dono := entrar(t, a)
	segredo := iniciarCadastroHTTP(t, a, dono)
	armado.Store(true)
	c, fimLogin := loginAssincrono(t, a)
	select {
	case <-travou:
	case <-time.After(10 * time.Second):
		t.Fatal("o login não chegou à inserção da sessão")
	}
	fimConfirm := make(chan *http.Response, 1)
	code := codigoDe(t, a, segredo)
	go func() {
		resp, _ := postSemT(http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/confirm", dono,
			map[string]string{"code": code})
		fimConfirm <- resp
	}()
	esperou := testdb.EsperarBloqueados(t, a.pool, 1, fimConfirm)
	soltar()
	resp := respostaDoLogin(t, fimLogin)
	var entrada struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entrada); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	confirm := respostaDoLogin(t, fimConfirm)
	confirm.Body.Close()
	if !esperou {
		t.Error("o Confirm não esperou a trava da emissão")
	}
	if resp.StatusCode != http.StatusOK || refreshCookie(resp) == nil {
		t.Errorf("login antes de ligar o fator: %d, queria 200 com refresh", resp.StatusCode)
	}
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirm devolveu %d", confirm.StatusCode)
	}
	if renova(t, a, c) {
		t.Error("a família aberta antes da confirmação continuou renovando")
	}
	if entrada.AccessToken == "" || statusEmMe(t, a, entrada.AccessToken) != http.StatusUnauthorized {
		t.Error("o access token do login anterior à confirmação precisa ser recusado")
	}
	if n := familiasVivas(t, a); n != 1 {
		t.Errorf("%d famílias vivas, queria só a do dono", n)
	}
	if st := statusEmMe(t, a, dono); st != http.StatusOK {
		t.Errorf("a sessão do dono deu %d em /me, queria 200", st)
	}
}
