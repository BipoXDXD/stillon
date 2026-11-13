package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O bloqueio da conta deixa rastro: um evento em audit_log e uma linha de log.
// A resposta ao cliente continua sem revelar nada, e é por isso que o
// rastro precisa existir: quem está trancado não recebe explicação, e a coluna
// locked_until é sobrescrita no ciclo seguinte.

const (
	acaoDeBloqueio = "user.locked"
	emailSemConta  = "ninguem@exemplo.com"
	senhaErrada    = "senha-errada-bem-comprida"
	codigoErrado   = "000000"
)

type eventoDeBloqueio struct {
	userID *uuid.UUID
	ipHash []byte
	at     time.Time
}

func eventosDeBloqueio(t *testing.T, a *api) []eventoDeBloqueio {
	t.Helper()
	rows, err := a.pool.Query(context.Background(),
		`SELECT user_id, ip_hash, at FROM audit_log WHERE action = $1 ORDER BY at, id`, acaoDeBloqueio)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var eventos []eventoDeBloqueio
	for rows.Next() {
		var e eventoDeBloqueio
		if err := rows.Scan(&e.userID, &e.ipHash, &e.at); err != nil {
			t.Fatal(err)
		}
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return eventos
}

func totalDeAuditoria(t *testing.T, a *api) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// enviarDe faz um POST de corpo vindo de origem.
func enviarDe(t *testing.T, a *api, caminho string, corpo any, origem string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+caminho, jsonBody(t, corpo))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	req.Header.Set("X-Forwarded-For", origem)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func corpoDeLogin(email, senha string) map[string]string {
	return map[string]string{"email": email, "password": senha}
}

// tentarLogin faz um login vindo de origem e devolve o status e o envelope.
func tentarLogin(t *testing.T, a *api, email, senha, origem string) (int, envelope) {
	t.Helper()
	resp := enviarDe(t, a, "/api/v1/auth/login", corpoDeLogin(email, senha), origem)
	defer resp.Body.Close()
	return resp.StatusCode, decodeEnvelope(t, resp)
}

// errarSenha manda n senhas erradas, cada uma de uma origem nova.
func errarSenha(t *testing.T, a *api, n int) {
	t.Helper()
	for range n {
		if status, _ := tentarLogin(t, a, testEmail, senhaErrada, novaOrigem()); status != http.StatusUnauthorized {
			t.Fatalf("senha errada devolveu %d, queria 401", status)
		}
	}
}

func exigirEvento(t *testing.T, a *api, e eventoDeBloqueio, origem string, at time.Time) {
	t.Helper()
	if e.userID == nil || *e.userID != a.userID {
		t.Errorf("user_id = %v, queria %v", e.userID, a.userID)
	}
	if !bytes.Equal(e.ipHash, crypto.HashIP(testIPHashKey, origem)) {
		t.Errorf("ip_hash = %x, queria o HMAC de %s, a origem da tentativa que travou", e.ipHash, origem)
	}
	if !e.at.Equal(at) {
		t.Errorf("at = %v, queria %v", e.at, at)
	}
}

// A quinta senha errada grava o evento; a quarta não, e a sexta, recusada sem
// reservar nada, não duplica.
func TestAccountLockRecordsExactlyOneEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)

	errarSenha(t, a, auth.LockThreshold-1)
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 0 {
		t.Fatalf("%d eventos antes do limiar, queria 0", len(eventos))
	}

	origemDoBloqueio := novaOrigem()
	if status, _ := tentarLogin(t, a, testEmail, senhaErrada, origemDoBloqueio); status != http.StatusUnauthorized {
		t.Fatalf("a quinta senha errada devolveu %d, queria 401", status)
	}
	eventos := eventosDeBloqueio(t, a)
	if len(eventos) != 1 {
		t.Fatalf("%d eventos depois da quinta senha errada, queria 1", len(eventos))
	}
	exigirEvento(t, a, eventos[0], origemDoBloqueio, a.clk.Now())

	if status, _ := tentarLogin(t, a, testEmail, senhaErrada, novaOrigem()); status != http.StatusUnauthorized {
		t.Fatalf("a sexta senha errada devolveu %d, queria 401", status)
	}
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Errorf("%d eventos depois da sexta tentativa, recusada; queria 1", len(eventos))
	}
	if n := totalDeAuditoria(t, a); n != 1 {
		t.Errorf("audit_log tem %d linhas, queria só a do bloqueio", n)
	}
}

// Conta inexistente não tem o que bloquear: gravar qualquer coisa aqui
// encheria a auditoria com lixo escolhido por quem chama.
func TestAccountLockUnknownEmailRecordsNothing(t *testing.T) {
	a := apiAtrasDeProxy(t)

	for range auth.LockThreshold + 1 {
		if status, _ := tentarLogin(t, a, emailSemConta, senhaErrada, novaOrigem()); status != http.StatusUnauthorized {
			t.Fatalf("e-mail inexistente devolveu %d, queria 401", status)
		}
	}
	if n := totalDeAuditoria(t, a); n != 0 {
		t.Errorf("audit_log tem %d linhas depois de logins em e-mail inexistente, queria 0", n)
	}
}

// Da primeira à sexta, com o evento sendo gravado no meio, a resposta é a de
// um e-mail que nunca existiu.
func TestAccountLockResponseMatchesUnknownEmail(t *testing.T) {
	a := apiAtrasDeProxy(t)
	statusSemConta, semConta := tentarLogin(t, a, emailSemConta, senhaErrada, novaOrigem())

	for tentativa := 1; tentativa <= auth.LockThreshold+1; tentativa++ {
		status, env := tentarLogin(t, a, testEmail, senhaErrada, novaOrigem())
		if status != statusSemConta {
			t.Errorf("tentativa %d: status %d, e-mail inexistente deu %d", tentativa, status, statusSemConta)
		}
		if env.Error != semConta.Error {
			t.Errorf("tentativa %d: corpo %+v, e-mail inexistente deu %+v", tentativa, env.Error, semConta.Error)
		}
	}
}

// linhasDoUsuario devolve as linhas de log que citam o usuário no campo
// user_id.
func linhasDoUsuario(t *testing.T, saida string, userID uuid.UUID) []string {
	t.Helper()
	var linhas []string
	for linha := range strings.Lines(saida) {
		var campos map[string]any
		if err := json.Unmarshal([]byte(linha), &campos); err != nil {
			t.Fatalf("linha de log não é JSON: %q: %v", linha, err)
		}
		if campos["user_id"] == userID.String() {
			linhas = append(linhas, linha)
		}
	}
	return linhas
}

// O log aponta a conta pelo id, uma vez por bloqueio. E-mail, senha e IP não
// aparecem: o ip_hash fica na auditoria, e log é passivo.
func TestAccountLockLogOmitsSensitiveData(t *testing.T) {
	a := apiAtrasDeProxy(t)
	log := capturarLog(t)
	const senhaCanario = "CANARY-senha-do-teste-de-bloqueio"

	for range auth.LockThreshold - 1 {
		if status, _ := tentarLogin(t, a, testEmail, senhaCanario, novaOrigem()); status != http.StatusUnauthorized {
			t.Fatalf("senha errada devolveu %d, queria 401", status)
		}
	}
	origemDoBloqueio := novaOrigem()
	tentarLogin(t, a, testEmail, senhaCanario, origemDoBloqueio)
	tentarLogin(t, a, testEmail, senhaCanario, novaOrigem())

	saida := log.String()
	linhas := linhasDoUsuario(t, saida, a.userID)
	if len(linhas) != 1 {
		t.Fatalf("%d linhas de log citam o usuário, queria 1:\n%s", len(linhas), saida)
	}
	for _, proibido := range []string{origemDoBloqueio, testEmail} {
		if strings.Contains(linhas[0], proibido) {
			t.Errorf("a linha do bloqueio contém %q: %s", proibido, linhas[0])
		}
	}
	for _, proibido := range []string{senhaCanario, testEmail} {
		if strings.Contains(saida, proibido) {
			t.Errorf("o log da requisição contém %q", proibido)
		}
	}
}

// recusarAuditoria faz todo INSERT em audit_log falhar de verdade, no banco.
func recusarAuditoria(t *testing.T, a *api) {
	t.Helper()
	if _, err := a.pool.Exec(context.Background(), `
		CREATE FUNCTION recusar_auditoria() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'auditoria indisponível'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `
		CREATE TRIGGER recusar_auditoria BEFORE INSERT ON audit_log
		FOR EACH ROW EXECUTE FUNCTION recusar_auditoria()`); err != nil {
		t.Fatal(err)
	}
}

// Trava e evento vivem na mesma transação: sem o evento, a trava não fica. A
// auditoria aqui falha de verdade, por um gatilho no banco.
func TestAccountLockRollsBackWithItsAuditEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	errarSenha(t, a, auth.LockThreshold-1)
	recusarAuditoria(t, a)

	status, env := tentarLogin(t, a, testEmail, senhaErrada, novaOrigem())

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, queria 500", status)
	}
	if strings.Contains(env.Error.Message, "auditoria") {
		t.Errorf("o erro do banco vazou para o cliente: %q", env.Error.Message)
	}
	falhas, bloqueada := estadoDoBloqueio(t, a)
	if falhas != auth.LockThreshold-1 || bloqueada {
		t.Errorf("falhas = %d, bloqueada = %v; queria %d e false: a trava ficou sem o evento",
			falhas, bloqueada, auth.LockThreshold-1)
	}
}

// Cada bloqueio é um evento próprio. A tentativa que só reinicia a contagem,
// depois de o bloqueio vencer, não é bloqueio.
func TestAccountLockEachCycleRecordsItsOwnEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	errarSenha(t, a, auth.LockThreshold)
	primeiro := a.clk.Now()

	a.clk.Advance(auth.LockDuration)
	errarSenha(t, a, auth.LockThreshold-1)
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Fatalf("%d eventos com a contagem reiniciada abaixo do limiar, queria 1", len(eventos))
	}

	a.clk.Advance(time.Second)
	origem := novaOrigem()
	tentarLogin(t, a, testEmail, senhaErrada, origem)

	eventos := eventosDeBloqueio(t, a)
	if len(eventos) != 2 {
		t.Fatalf("%d eventos depois de dois bloqueios, queria 2", len(eventos))
	}
	if !eventos[0].at.Equal(primeiro) {
		t.Errorf("o primeiro evento mudou: at = %v, queria %v", eventos[0].at, primeiro)
	}
	exigirEvento(t, a, eventos[1], origem, a.clk.Now())
}

// acertarSenhaComSegundoFator manda a senha certa de uma conta com 2FA e exige
// o desafio: a reserva volta, e uma quinta reserva tira a trava que aplicou.
func acertarSenhaComSegundoFator(t *testing.T, a *api) {
	t.Helper()
	if status, _ := tentarLogin(t, a, testEmail, testPass, novaOrigem()); status != http.StatusOK {
		t.Fatalf("a senha certa devolveu %d, queria 200", status)
	}
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold-1 || bloqueada {
		t.Fatalf("falhas = %d, bloqueada = %v; queria %d e false: a trava devia ter sido desfeita",
			falhas, bloqueada, auth.LockThreshold-1)
	}
}

// Com 2FA, a senha certa no limiar trava e destrava na mesma requisição, e o
// contador fica parado logo abaixo do limiar. Sem conta travada de verdade,
// repetir o login não pode encher a auditoria.
func TestAccountLockUndoneBySecondFactorRecordsOneEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	errarSenha(t, a, auth.LockThreshold-1)

	acertarSenhaComSegundoFator(t, a)
	acertarSenhaComSegundoFator(t, a)

	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Errorf("%d eventos em dois ciclos com a trava desfeita, queria 1", len(eventos))
	}
}

// Escolha aceita: a janela não separa trava desfeita de trava real. Um bloqueio
// real logo depois fica representado pelo evento anterior, mas a conta trava.
func TestAccountLockAfterAnUndoneLockStaysOnTheSameEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	errarSenha(t, a, auth.LockThreshold-1)
	acertarSenhaComSegundoFator(t, a)

	errarSenha(t, a, 1)

	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != auth.LockThreshold || !bloqueada {
		t.Errorf("falhas = %d, bloqueada = %v; queria %d e true", falhas, bloqueada, auth.LockThreshold)
	}
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Errorf("%d eventos depois do bloqueio real, queria 1", len(eventos))
	}
}

// A janela é a duração do bloqueio, aberta na ponta antiga: um segundo antes
// de completar, nada novo; ao completar, outro evento. Fechada, ela engoliria
// o bloqueio real que começa no instante em que o anterior vence.
func TestAccountLockUndoneRecordsAgainOnceLockDurationPasses(t *testing.T) {
	a := apiAtrasDeProxy(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	errarSenha(t, a, auth.LockThreshold-1)
	acertarSenhaComSegundoFator(t, a)
	primeiro := a.clk.Now()

	a.clk.Advance(auth.LockDuration - time.Second)
	acertarSenhaComSegundoFator(t, a)
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Fatalf("%d eventos um segundo antes de a janela fechar, queria 1", len(eventos))
	}

	a.clk.Advance(time.Second)
	acertarSenhaComSegundoFator(t, a)

	eventos := eventosDeBloqueio(t, a)
	if len(eventos) != 2 {
		t.Fatalf("%d eventos depois de passar LockDuration, queria 2", len(eventos))
	}
	if !eventos[0].at.Equal(primeiro) || !eventos[1].at.Equal(primeiro.Add(auth.LockDuration)) {
		t.Errorf("eventos em %v e %v, queria %v e %v",
			eventos[0].at, eventos[1].at, primeiro, primeiro.Add(auth.LockDuration))
	}
}

// A janela é por conta: o bloqueio de uma não cala o da outra.
func TestAccountLockEventWindowIsPerAccount(t *testing.T) {
	a := apiAtrasDeProxy(t)
	errarSenha(t, a, auth.LockThreshold)

	outro := outroUsuario(t, a)
	store := auth.NewPGStore(a.pool)
	for range auth.LockThreshold {
		if _, err := store.ReserveAttempt(context.Background(), outro, nil, a.clk.Now()); err != nil {
			t.Fatal(err)
		}
	}

	if eventos := eventosDeBloqueio(t, a); len(eventos) != 2 {
		t.Errorf("%d eventos com duas contas travadas, queria 2", len(eventos))
	}
}

// errarProva manda um código errado para o desafio e exige o 401.
func errarProva(t *testing.T, a *api, desafio, origem string) {
	t.Helper()
	resp := enviarDe(t, a, "/api/v1/auth/totp/verify",
		map[string]string{"challenge": desafio, "code": codigoErrado}, origem)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("prova errada devolveu %d, queria 401", resp.StatusCode)
	}
}

// O segundo fator entra no mesmo contador do login: a quinta prova errada
// trava a conta e deixa o mesmo rastro de uma quinta senha errada.
func TestAccountLockFifthWrongSecondFactorRecordsEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	cadastrarTOTP(t, a, clientWithJar(t))
	desafio := desafioAtual(t, a, clientWithJar(t))

	for range auth.LockThreshold - 1 {
		errarProva(t, a, desafio, novaOrigem())
	}
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 0 {
		t.Fatalf("%d eventos antes do limiar, queria 0", len(eventos))
	}

	origemDoBloqueio := novaOrigem()
	errarProva(t, a, desafio, origemDoBloqueio)

	eventos := eventosDeBloqueio(t, a)
	if len(eventos) != 1 {
		t.Fatalf("%d eventos depois da quinta prova errada, queria 1", len(eventos))
	}
	exigirEvento(t, a, eventos[0], origemDoBloqueio, a.clk.Now())
}

// O evento registra a trava aplicada antes da verificação: quatro erros
// seguidos de um acerto ficam no rastro, mesmo com o login dando certo.
func TestAccountLockFifthCorrectPasswordRecordsEvent(t *testing.T) {
	a := apiAtrasDeProxy(t)
	errarSenha(t, a, auth.LockThreshold-1)

	origem := novaOrigem()
	if status, _ := tentarLogin(t, a, testEmail, testPass, origem); status != http.StatusOK {
		t.Fatalf("a quinta tentativa, com a senha certa, devolveu %d, queria 200", status)
	}

	eventos := eventosDeBloqueio(t, a)
	if len(eventos) != 1 {
		t.Fatalf("%d eventos depois da quinta senha certa, queria 1", len(eventos))
	}
	exigirEvento(t, a, eventos[0], origem, a.clk.Now())
	if falhas, bloqueada := estadoDoBloqueio(t, a); falhas != 0 || bloqueada {
		t.Errorf("falhas = %d, bloqueada = %v; o login certo devia ter zerado", falhas, bloqueada)
	}
}

// Sem conseguir gravar o rastro, a senha certa não abre porta nenhuma: a
// reserva falha antes da verificação, e nada de sessão nasce.
func TestAccountLockCorrectPasswordWithFailingAuditOpensNoSession(t *testing.T) {
	a := apiAtrasDeProxy(t)
	errarSenha(t, a, auth.LockThreshold-1)
	recusarAuditoria(t, a)

	resp := enviarDe(t, a, "/api/v1/auth/login", corpoDeLogin(testEmail, testPass), novaOrigem())
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, queria 500", resp.StatusCode)
	}
	if cookies := resp.Cookies(); len(cookies) != 0 {
		t.Errorf("a resposta emitiu cookies: %v", cookies)
	}
	if strings.Contains(string(corpo), "access_token") {
		t.Errorf("a resposta trouxe token: %s", corpo)
	}
	var sessoes int
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, a.userID).Scan(&sessoes); err != nil {
		t.Fatal(err)
	}
	if sessoes != 0 {
		t.Errorf("%d sessões criadas, queria 0", sessoes)
	}
}

// Oito palpites simultâneos travam a conta uma vez só: só a reserva que aplica
// a trava grava o evento.
func TestAccountLockParallelGuessesRecordOneEvent(t *testing.T) {
	a := newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		cfg.Argon2MaxConcurrent = 16
	})
	dispararContraALinhaTravada(t, a, 8, func() (*http.Response, error) {
		return postDe(http.DefaultClient, a.srv.URL+"/api/v1/auth/login",
			map[string]string{"email": testEmail, "password": senhaErrada})
	})
	if eventos := eventosDeBloqueio(t, a); len(eventos) != 1 {
		t.Errorf("%d eventos em 8 palpites simultâneos, queria 1", len(eventos))
	}
}
