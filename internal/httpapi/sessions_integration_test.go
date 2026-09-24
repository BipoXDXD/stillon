package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/auth"
)

type sessaoItem struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

func listarSessoes(t *testing.T, a *api, token string) []sessaoItem {
	t.Helper()
	resp := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions devolveu %d", resp.StatusCode)
	}
	var corpo struct {
		Items []sessaoItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatal(err)
	}
	return corpo.Items
}

func deleteComToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "vigilume")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// gerente abre famílias direto pelo Manager, sem passar pelo login e seu
// balde de 5 por IP.
func gerente(a *api) *auth.Manager {
	return auth.NewManager(auth.NewPGStore(a.pool), a.clk, testIPHashKey)
}

// carimboDe é a prova de senha verificada que Issue exige: o carimbo do hash
// gravado do usuário.
func carimboDe(t *testing.T, a *api, userID uuid.UUID) string {
	t.Helper()
	var hash string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return auth.PasswordStamp(hash)
}

func familiaDe(t *testing.T, token string, a *api) uuid.UUID {
	t.Helper()
	c, err := auth.ParseAccessToken(testJWTSecret, token, a.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c.FamilyID
}

func ids(itens []sessaoItem) []string {
	out := make([]string, 0, len(itens))
	for _, i := range itens {
		out = append(out, i.ID)
	}
	return out
}

// Só famílias vivas: a revogada, a vencida e a de outro usuário não aparecem;
// só a do token é current.
func TestListSessionsShowsOnlyLiveFamilies(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	atual := familiaDe(t, token, a)
	mgr := gerente(a)
	ctx := context.Background()
	meta := auth.Meta{IP: "203.0.113.9", UserAgent: "outro aparelho"}

	_, viva, err := mgr.Issue(ctx, a.userID, carimboDe(t, a, a.userID), meta)
	if err != nil {
		t.Fatal(err)
	}
	revogada, _, err := mgr.Issue(ctx, a.userID, carimboDe(t, a, a.userID), meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Revoke(ctx, revogada); err != nil {
		t.Fatal(err)
	}
	_, vencida, err := mgr.Issue(ctx, a.userID, carimboDe(t, a, a.userID), meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE family_id = $1`,
		vencida.FamilyID, a.clk.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Issue(ctx, outroUsuario(t, a), auth.PasswordStamp("x"), meta); err != nil {
		t.Fatal(err)
	}

	itens := listarSessoes(t, a, token)
	if len(itens) != 2 {
		t.Fatalf("sessões = %v, queria só a atual e a viva", ids(itens))
	}
	vistas := map[string]bool{}
	for _, i := range itens {
		vistas[i.ID] = i.Current
	}
	if cur, ok := vistas[atual.String()]; !ok || !cur {
		t.Errorf("a sessão do token não veio como current: %+v", itens)
	}
	if cur, ok := vistas[viva.FamilyID.String()]; !ok || cur {
		t.Errorf("a outra sessão viva faltou ou veio como current: %+v", itens)
	}
}

// Nada de IP, hash, vencimento nem id de linha: as chaves são exatamente as do
// contrato, e os bytes gravados de ip_hash e token_hash não aparecem.
func TestListSessionsNeverExposesIPOrHash(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)

	resp := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer resp.Body.Close()
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var cru struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(corpo, &cru); err != nil {
		t.Fatal(err)
	}
	if len(cru.Items) != 1 {
		t.Fatalf("%d itens, queria 1", len(cru.Items))
	}
	permitidas := map[string]bool{"id": true, "created_at": true, "last_used_at": true, "user_agent": true, "current": true}
	for chave := range cru.Items[0] {
		if !permitidas[chave] {
			t.Errorf("chave %q fora do contrato na resposta", chave)
		}
	}

	var ipHash, tokenHash []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT ip_hash, token_hash FROM sessions WHERE user_id = $1`, a.userID).Scan(&ipHash, &tokenHash); err != nil {
		t.Fatal(err)
	}
	for _, segredo := range [][]byte{ipHash, tokenHash} {
		for _, forma := range []string{hex.EncodeToString(segredo), base64.StdEncoding.EncodeToString(segredo),
			base64.RawURLEncoding.EncodeToString(segredo)} {
			if bytes.Contains(corpo, []byte(forma)) {
				t.Errorf("um hash gravado apareceu na resposta: %s", forma)
			}
		}
	}
	if bytes.Contains(corpo, []byte("127.0.0.1")) {
		t.Error("o IP apareceu na resposta")
	}
}

// A ordem é a do login (created_at, o UUIDv7 da família), e não a do último
// uso: renovar a sessão mais antiga não a traz para o topo. last_used_at é o
// instante do último refresh.
//
// Guarda dupla (D23): a primeira família foi renovada, então tem uma linha
// usada e uma viva, e duas coisas fazem a lista mostrar a viva, uma vez só: o
// filtro used_at IS NULL de ListLiveFamilies e a dedupe de ListSessions, que
// fica com a linha mais nova de cada família. Sabotar só uma deixa este teste
// verde; a sabotagem registrada (Task 11) tira o filtro E faz a dedupe ficar
// com a linha mais velha, e o last_used_at da renovada sai errado.
func TestListSessionsOrderAndTimes(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	primeira := familiaDe(t, token, a)
	mgr := gerente(a)
	ctx := context.Background()

	var familias []uuid.UUID
	for range 2 {
		time.Sleep(5 * time.Millisecond) // o UUIDv7 tem resolução de milissegundo
		antes := time.Now()
		_, s, err := mgr.Issue(ctx, a.userID, carimboDe(t, a, a.userID), auth.Meta{IP: "203.0.113.9"})
		if err != nil {
			t.Fatal(err)
		}
		depois := time.Now()
		seg, nseg := s.FamilyID.Time().UnixTime()
		if criada := time.Unix(seg, nseg); criada.Before(antes.Truncate(time.Millisecond)) || criada.After(depois) {
			t.Fatalf("o UUIDv7 da família (%v) está fora do login (%v a %v)", criada, antes, depois)
		}
		familias = append(familias, s.FamilyID)
	}

	time.Sleep(5 * time.Millisecond)
	antesDoRefresh := time.Now()
	novo := accessTokenDaSessao(t, a, c) // renova a PRIMEIRA

	itens := listarSessoes(t, a, novo)
	queria := []string{familias[1].String(), familias[0].String(), primeira.String()}
	if got := ids(itens); strings.Join(got, ",") != strings.Join(queria, ",") {
		t.Fatalf("ordem = %v, queria %v (login mais novo primeiro)", got, queria)
	}
	if !itens[0].CreatedAt.After(itens[1].CreatedAt) || !itens[1].CreatedAt.After(itens[2].CreatedAt) {
		t.Errorf("created_at fora de ordem: %+v", itens)
	}
	// O refresh foi agora: last_used_at da primeira passa o das outras.
	// Folga de 1 s entre o relógio do banco e o do processo.
	if itens[2].LastUsedAt.Before(antesDoRefresh.Add(-time.Second)) || !itens[2].LastUsedAt.After(itens[0].LastUsedAt) {
		t.Errorf("last_used_at da sessão renovada = %v; queria o instante do refresh (%v)", itens[2].LastUsedAt, antesDoRefresh)
	}
}

// O user agent é cortado em 256 code points, sem partir caractere.
func TestListSessionsTruncatesUserAgent(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	if _, _, err := gerente(a).Issue(context.Background(), a.userID, carimboDe(t, a, a.userID),
		auth.Meta{IP: "203.0.113.9", UserAgent: strings.Repeat("é", 300)}); err != nil {
		t.Fatal(err)
	}
	for _, i := range listarSessoes(t, a, token) {
		if i.Current {
			continue
		}
		if n := utf8.RuneCountInString(i.UserAgent); n != 256 || !utf8.ValidString(i.UserAgent) {
			t.Errorf("user_agent com %d code points (UTF-8 válido: %v), queria 256", n, utf8.ValidString(i.UserAgent))
		}
	}
}

// Sessão de outro usuário é 404, e continua viva: a prova é o refresh dela.
func TestRevokeSessionOfAnotherUserIs404(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	mgr := gerente(a)
	meta := auth.Meta{IP: "203.0.113.9"}
	doOutro, s, err := mgr.Issue(context.Background(), outroUsuario(t, a), auth.PasswordStamp("x"), meta)
	if err != nil {
		t.Fatal(err)
	}

	// O code distingue o 404 do handler (envelope not_found) do 404 do
	// ServeMux sem a rota, que é texto: só o status deixava este teste verde
	// com a rota inexistente.
	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	codigo := codigoDoErro(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || codigo != "not_found" {
		t.Errorf("DELETE da sessão alheia devolveu %d %s, queria 404 not_found", resp.StatusCode, codigo)
	}
	if _, _, err := mgr.Rotate(context.Background(), doOutro, meta); err != nil {
		t.Errorf("a sessão do outro usuário caiu: %v", err)
	}
}

func TestRevokeSessionTwiceIs404(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	doOutroAparelho, s, err := gerente(a).Issue(context.Background(), a.userID, carimboDe(t, a, a.userID), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	url := a.srv.URL + "/api/v1/sessions/" + s.FamilyID.String()

	primeiro := deleteComToken(t, url, token)
	primeiro.Body.Close()
	segundo := deleteComToken(t, url, token)
	codigo := codigoDoErro(t, segundo)
	segundo.Body.Close()
	if primeiro.StatusCode != http.StatusNoContent || segundo.StatusCode != http.StatusNotFound || codigo != "not_found" {
		t.Fatalf("primeiro %d, segundo %d %s; queria 204 e 404 not_found", primeiro.StatusCode, segundo.StatusCode, codigo)
	}
	if _, _, err := gerente(a).Rotate(context.Background(), doOutroAparelho, auth.Meta{IP: "203.0.113.9"}); err == nil {
		t.Error("a sessão encerrada ainda renova")
	}
	var n int
	if err := a.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'session.revoked' AND entity = 'session_family' AND entity_id = $1 AND ip_hash IS NOT NULL`,
		s.FamilyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d registros session.revoked, queria 1", n)
	}

	lixo := deleteComToken(t, a.srv.URL+"/api/v1/sessions/nao-e-uuid", token)
	codigo = codigoDoErro(t, lixo)
	lixo.Body.Close()
	if lixo.StatusCode != http.StatusNotFound || codigo != "not_found" {
		t.Errorf("id malformado devolveu %d %s, queria 404 not_found", lixo.StatusCode, codigo)
	}
}

// Encerrar a própria sessão é o logout daquela família: 204 e o cookie de
// refresh apagado (Path=/api/v1/auth, vindo de /api/v1/sessions).
func TestRevokeCurrentSessionClearsCookie(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)

	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+familiaDe(t, token, a).String(), token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, queria 204", resp.StatusCode)
	}
	ck := refreshCookie(resp)
	if ck == nil || ck.Value != "" || ck.MaxAge >= 0 || ck.Path != "/api/v1/auth" {
		t.Fatalf("cookie = %+v; queria vigilume_refresh vazio, vencido, Path=/api/v1/auth", ck)
	}
	if renova(t, a, c) {
		t.Error("a sessão encerrada ainda renova")
	}
}

func TestRevokeOtherSessionKeepsCookie(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	_, s, err := gerente(a).Issue(context.Background(), a.userID, carimboDe(t, a, a.userID), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	resp := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, queria 204", resp.StatusCode)
	}
	if len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("encerrar OUTRA sessão mexeu no cookie: %v", resp.Header.Values("Set-Cookie"))
	}
}

// Um token de sessão já encerrada não lista nem derruba sessões (D11): sem
// isso, o invasor encerrado continuaria 15 minutos derrubando o dono.
//
// Guarda dupla (D23) nas duas rotas: o Authn da cadeia recusa o token
// ausente ou inválido, e o sessaoViva do handler recusa de novo (sem usuário
// ou família no contexto) antes de conferir a família. Este teste trava a
// família viva, que só o sessaoViva confere; o token ausente é o
// TestPlano5RoutesRequireAuthentication, que fica verde sem o Authn OU sem o
// sessaoViva. A sabotagem registrada (Task 11) tira os dois de GET /sessions,
// e ele fica vermelho (200).
func TestSessionRoutesRejectDeadFamily(t *testing.T) {
	a := newAPI(t)
	c, token := entrar(t, a)
	mgr := gerente(a)
	meta := auth.Meta{IP: "203.0.113.9"}
	doDono, s, err := mgr.Issue(context.Background(), a.userID, carimboDe(t, a, a.userID), meta)
	if err != nil {
		t.Fatal(err)
	}
	sair := post(t, c, a.srv.URL+"/api/v1/auth/logout", nil)
	sair.Body.Close()

	lista := getComToken(t, a.srv.URL+"/api/v1/sessions", token)
	defer lista.Body.Close()
	if lista.StatusCode != http.StatusUnauthorized || codigoDoErro(t, lista) != "unauthorized" {
		t.Errorf("GET /sessions com a família morta devolveu %d", lista.StatusCode)
	}
	del := deleteComToken(t, a.srv.URL+"/api/v1/sessions/"+s.FamilyID.String(), token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusUnauthorized || codigoDoErro(t, del) != "unauthorized" {
		t.Errorf("DELETE com a família morta devolveu %d", del.StatusCode)
	}
	if _, _, err := mgr.Rotate(context.Background(), doDono, meta); err != nil {
		t.Errorf("o token morto derrubou a sessão do dono: %v", err)
	}
}
