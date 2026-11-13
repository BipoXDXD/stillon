package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Lacunas de autenticação apontadas pela cobertura do pacote: ramos que só
// acontecem quando outra requisição (ou outro aparelho) mexe no estado no meio
// desta. A interferência entra por um trigger, para cair sempre no mesmo ponto
// da requisição, como em recusarCommitDeSessoes. O snapshot do testdb apaga
// funções e triggers no fim do teste.

// respostaLida é a resposta com o corpo já lido: os testes conferem o
// envelope e também o que o corpo NÃO pode trazer.
type respostaLida struct {
	status  int
	corpo   string
	cookies []*http.Cookie
}

func lerResposta(t *testing.T, resp *http.Response) respostaLida {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return respostaLida{status: resp.StatusCode, corpo: string(b), cookies: resp.Cookies()}
}

func (r respostaLida) envelope(t *testing.T) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(r.corpo), &env); err != nil {
		t.Fatalf("resposta %d não é o envelope de erro: %s", r.status, r.corpo)
	}
	return env
}

// conferirRecusaDeSessao afirma o status e o code do envelope, que nenhum cookie de
// sessão saiu, e que o corpo não traz credencial, dado da conta nem detalhe
// interno.
func conferirRecusaDeSessao(t *testing.T, a *api, r respostaLida, status int, codigo string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, queria %d: %s", r.status, status, r.corpo)
	}
	if got := r.envelope(t).Error.Code; got != codigo {
		t.Errorf("code = %q, queria %q", got, codigo)
	}
	for _, ck := range r.cookies {
		if ck.Value != "" {
			t.Errorf("a recusa emitiu o cookie %s", ck.Name)
		}
	}
	for _, proibido := range []string{
		"access_token", "recovery_codes", "otpauth", testEmail, a.userID.String(),
		"no rows", "SQL", "sql:", "pgx", "Exception", "goroutine", "internal/",
	} {
		if strings.Contains(r.corpo, proibido) {
			t.Errorf("o corpo da recusa trouxe %q: %s", proibido, r.corpo)
		}
	}
}

// errosDasFamilias soma as provas erradas contadas para o usuário.
func errosDasFamilias(t *testing.T, a *api) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT coalesce(sum(errors), 0) FROM session_family_proofs WHERE user_id = $1`, a.userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// consumirDesafioNoMeio faz o UPDATE que grava o timestep do código aceito
// avançar também a marca do desafio até issuedAt: é o que um /totp/verify
// concorrente com o mesmo desafio faria entre a pré-checagem da marca e o
// ConsumeChallenge desta requisição.
func consumirDesafioNoMeio(t *testing.T, a *api, issuedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `
		CREATE FUNCTION teste_consome_desafio() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN NEW.totp_challenge_mark := '`+issuedAt.UTC().Format(time.RFC3339Nano)+`'::timestamptz; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `
		CREATE TRIGGER teste_consome_desafio BEFORE UPDATE OF totp_last_timestep ON users
		FOR EACH ROW EXECUTE FUNCTION teste_consome_desafio()`); err != nil {
		t.Fatal(err)
	}
}

// revogarFamiliaNaReserva derruba a família no instante em que a rota reserva a
// prova dela: depois da guarda de sessão viva e antes do serviço. É a troca de
// senha ou o DELETE /sessions de outro aparelho chegando no meio.
func revogarFamiliaNaReserva(t *testing.T, a *api) {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `
		CREATE FUNCTION teste_revoga_na_reserva() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN
			UPDATE sessions SET revoked_at = now() WHERE family_id = NEW.family_id AND revoked_at IS NULL;
			RETURN NULL;
		END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `
		CREATE TRIGGER teste_revoga_na_reserva AFTER INSERT OR UPDATE ON session_family_proofs
		FOR EACH ROW EXECUTE FUNCTION teste_revoga_na_reserva()`); err != nil {
		t.Fatal(err)
	}
}

// A trava autoritativa contra o reuso do desafio é o ConsumeChallenge, e não a
// pré-checagem: dois verify com o mesmo desafio passam os dois pela
// pré-checagem. O perdedor acerta o código e ainda assim leva 401 unauthorized,
// sem sessão.
func TestTOTPVerifyRefusesAChallengeConsumedMidRequest(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)
	d, err := auth.ParseChallengeToken(testJWTSecret, desafio, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	antes, _ := sessoesDoUsuario(t, a)
	consumirDesafioNoMeio(t, a, d.IssuedAt)

	r := lerResposta(t, verificar(t, a, c, segredo, desafio))

	conferirRecusaDeSessao(t, a, r, http.StatusUnauthorized, "unauthorized")
	if depois, _ := sessoesDoUsuario(t, a); depois != antes {
		t.Errorf("sessões do usuário: %d antes, %d depois; o desafio consumido abriu sessão", antes, depois)
	}
	// O código foi aceito (o timestep gravado disparou o trigger e zerou as
	// falhas): o 401 veio do consumo do desafio, e não do código.
	var marca *time.Time
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_challenge_mark FROM users WHERE id = $1`, a.userID).Scan(&marca); err != nil {
		t.Fatal(err)
	}
	if marca == nil || !marca.Equal(d.IssuedAt) {
		t.Fatalf("marca = %v, queria %v: o código não chegou a ser aceito", marca, d.IssuedAt)
	}
	if falhas, _ := estadoDoBloqueio(t, a); falhas != 0 {
		t.Errorf("%d falhas contadas; quem perde o consumo acertou o código", falhas)
	}
}

// O rehash que grava: a sessão sai com o carimbo do hash NOVO. Com o carimbo
// antigo, Issue recusaria (o hash gravado mudou) e todo login depois de uma
// atualização dos parâmetros do Argon2id daria 401.
func TestLoginWithRehashIssuesTheSessionWithTheNewHash(t *testing.T) {
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

	c := clientWithJar(t)
	r := lerResposta(t, login(t, a, c, testEmail, testPass))
	if r.status != http.StatusOK {
		t.Fatalf("login com rehash: %d, queria 200: %s", r.status, r.corpo)
	}
	if !strings.Contains(r.corpo, "access_token") {
		t.Errorf("o login com rehash não devolveu access token: %s", r.corpo)
	}

	var gravado string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, a.userID).Scan(&gravado); err != nil {
		t.Fatal(err)
	}
	if gravado == fraco {
		t.Fatal("o rehash não gravou o hash novo")
	}
	ok, precisa, err := auth.VerifyPassword(context.Background(), gravado, testPass)
	if err != nil || !ok || precisa {
		t.Errorf("hash gravado: confere=%v, ainda pede rehash=%v, erro=%v; queria true, false, nil", ok, precisa, err)
	}
	if !renova(t, a, c) {
		t.Error("o refresh da sessão do login com rehash foi recusado: o carimbo não é o do hash novo")
	}

	segundo := lerResposta(t, login(t, a, clientWithJar(t), testEmail, testPass))
	if segundo.status != http.StatusOK {
		t.Errorf("segundo login, já sem rehash: %d, queria 200", segundo.status)
	}
}

// O usuário apagado entre o login e o segundo fator recebe o mesmo 401
// invalid_credentials do código errado: nada de 500, nem resposta distinta.
func TestTOTPVerifyForAUserDeletedAfterTheChallenge(t *testing.T) {
	a := newAPI(t)
	c := clientWithJar(t)
	segredo := cadastrarTOTP(t, a, c)
	desafio := desafioAtual(t, a, c)

	errado := "000000"
	if errado == codigoDe(t, a, segredo) {
		errado = "111111"
	}
	referencia := lerResposta(t, post(t, c, a.srv.URL+"/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": errado}))
	conferirRecusaDeSessao(t, a, referencia, http.StatusUnauthorized, "invalid_credentials")

	if _, err := a.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, a.userID); err != nil {
		t.Fatal(err)
	}

	r := lerResposta(t, verificar(t, a, c, segredo, desafio))
	conferirRecusaDeSessao(t, a, r, http.StatusUnauthorized, "invalid_credentials")
	if got, want := r.envelope(t).Error.Message, referencia.envelope(t).Error.Message; got != want {
		t.Errorf("mensagem = %q, queria a do código errado, %q", got, want)
	}
	if n, _ := sessoesDoUsuario(t, a); n != 0 {
		t.Errorf("%d sessões do usuário apagado", n)
	}
}

// A família cai entre a guarda da rota e o serviço: a troca de senha é 401
// unauthorized, a senha não muda e nada conta como prova errada.
func TestChangePasswordWithTheSessionRevokedMidRequest(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	revogarFamiliaNaReserva(t, a)

	r := lerResposta(t, trocarSenha(t, a, token,
		map[string]string{"current_password": testPass, "new_password": senhaNova}))

	conferirRecusaDeSessao(t, a, r, http.StatusUnauthorized, "unauthorized")
	if !senhaGravadaE(t, a, testPass) || senhaGravadaE(t, a, senhaNova) {
		t.Error("a troca com a sessão revogada no meio gravou a senha nova")
	}
	if n := errosDasFamilias(t, a); n != 0 {
		t.Errorf("%d provas erradas contadas; a senha estava certa", n)
	}
}

// A família cai entre a guarda e a trava do rotate/confirm: 401 unauthorized,
// nenhum código de recuperação no corpo, o segredo ativo continua o mesmo e
// nada conta como prova errada.
func TestTOTPRotateConfirmWithTheFamilyRevokedMidRequest(t *testing.T) {
	a := newAPI(t)
	segredo := cadastrarTOTP(t, a, clientWithJar(t))
	token := accessTokenValido(t, a)
	st, novo := iniciarTrocaHTTP(t, a, token, map[string]string{"password": testPass, "code": codigoDe(t, a, segredo)})
	if st != http.StatusOK {
		t.Fatalf("rotate devolveu %d", st)
	}
	var ativoAntes []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, a.userID).Scan(&ativoAntes); err != nil {
		t.Fatal(err)
	}
	revogarFamiliaNaReserva(t, a)

	r := lerResposta(t, postComToken(t, http.DefaultClient, a.srv.URL+"/api/v1/auth/totp/rotate/confirm", token,
		map[string]string{"code": codigoDe(t, a, novo), "rotation": a.rotacao}))

	conferirRecusaDeSessao(t, a, r, http.StatusUnauthorized, "unauthorized")
	var ativoDepois []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc FROM users WHERE id = $1`, a.userID).Scan(&ativoDepois); err != nil {
		t.Fatal(err)
	}
	if string(ativoDepois) != string(ativoAntes) {
		t.Error("a troca foi promovida com a família revogada")
	}
	if n := errosDasFamilias(t, a); n != 0 {
		t.Errorf("%d provas erradas contadas; o código estava certo", n)
	}
}
