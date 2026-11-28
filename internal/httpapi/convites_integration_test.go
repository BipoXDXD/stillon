package httpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/convitemail"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
)

// convitesDe é a rota de convites do projeto.
func convitesDe(projeto uuid.UUID) string {
	return "/api/v1/projects/" + projeto.String() + "/invitations"
}

// usuarioComEmailConfirmado cria uma conta confirmada com o e-mail dado.
func usuarioComEmailConfirmado(t *testing.T, a *api, email string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash, email_verified_at) VALUES ($1, $2, 'x', now())`, id, email); err != nil {
		t.Fatal(err)
	}
	return id
}

// semCamposDoConvite tira do corpo o que muda de um convite para outro.
func semCamposDoConvite(t *testing.T, corpo string) string {
	t.Helper()
	m := decodificar(t, corpo)
	for _, campo := range []string{"id", "email", "created_at", "expires_at"} {
		if _, ok := m[campo]; !ok {
			t.Fatalf("resposta sem %q: %s", campo, corpo)
		}
		delete(m, campo)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// convidarDuasContas convida, como o titular, um e-mail com conta e um sem
// conta, e devolve as duas respostas.
func convidarDuasContas(t *testing.T, a *api) (status [2]int, corpos [2]string) {
	t.Helper()
	usuarioComEmailConfirmado(t, a, "tem@exemplo.com")
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	for i, email := range []string{"tem@exemplo.com", "nao@exemplo.com"} {
		status[i], corpos[i] = lerCru(t, a, http.MethodPost, convitesDe(projeto), dono,
			map[string]string{"email": email, "role": "member"})
	}
	return status, corpos
}

// Convidar quem tem conta e quem não tem dá a mesma resposta e o mesmo
// e-mail, a menos do link: nada diz a quem convida se o endereço tem conta.
func TestInviteResponseIdenticalWithOrWithoutAccount(t *testing.T) {
	a := newAPI(t)
	status, corpos := convidarDuasContas(t, a)
	if status[0] != http.StatusCreated || status[1] != http.StatusCreated {
		t.Fatalf("convites deram %d e %d, queria 201: %s / %s", status[0], status[1], corpos[0], corpos[1])
	}
	if x, y := semCamposDoConvite(t, corpos[0]), semCamposDoConvite(t, corpos[1]); x != y {
		t.Errorf("respostas diferentes:\n%s\n%s", x, y)
	}
	enviados := a.conta.todos()
	if len(enviados) != 2 {
		t.Fatalf("%d e-mails enfileirados, queria 2", len(enviados))
	}
	if enviados[0].assunto != enviados[1].assunto {
		t.Errorf("assuntos diferentes: %q × %q", enviados[0].assunto, enviados[1].assunto)
	}
	semLink := func(e emailDeConta) string { return strings.Replace(e.corpo, tokenDo(t, e), "TOKEN", 1) }
	if semLink(enviados[0]) != semLink(enviados[1]) {
		t.Errorf("corpos diferentes a menos do link:\n%s\n%s", enviados[0].corpo, enviados[1].corpo)
	}
	if enviados[0].para != "tem@exemplo.com" || enviados[1].para != "nao@exemplo.com" {
		t.Errorf("destinatários: %q e %q", enviados[0].para, enviados[1].para)
	}
}

// Com o enviador real e o teto diário esgotado, a resposta continua a
// mesma, e nada sai.
func TestInviteResponseIdenticalWithExhaustedCeiling(t *testing.T) {
	a := newAPI(t)
	transporte := &transporteContado{}
	teto := &tetoFalso{libera: false}
	balde := &baldeDeDestinatarioFalso{libera: true}
	enviador := contaemail.New(transporte, teto, balde, testIPHashKey, slog.New(slog.DiscardHandler))
	ctx, parar := context.WithCancel(context.Background())
	rodando := make(chan struct{})
	go func() { defer close(rodando); enviador.Rodar(ctx) }()
	t.Cleanup(func() { parar(); <-rodando })
	a.deps.Invitations = tenancy.NewInvitations(pgstore.NewInvitations(a.pool),
		convitemail.New(a.pool, a.clk, enviador, linksDoPainel), a.clk)
	a.reiniciar(t)

	status, corpos := convidarDuasContas(t, a)
	parar()
	<-rodando
	if status[0] != http.StatusCreated || status[1] != http.StatusCreated {
		t.Fatalf("convites deram %d e %d, queria 201: %s / %s", status[0], status[1], corpos[0], corpos[1])
	}
	if x, y := semCamposDoConvite(t, corpos[0]), semCamposDoConvite(t, corpos[1]); x != y {
		t.Errorf("respostas diferentes com o teto esgotado:\n%s\n%s", x, y)
	}
	if n := teto.consultas.Load(); n != 2 {
		t.Errorf("%d tentativas de reservar o teto, queria 2 (as duas mensagens chegaram ao trabalhador)", n)
	}
	if n := transporte.entregas.Load(); n != 0 {
		t.Errorf("%d entregas com o teto esgotado, queria 0", n)
	}
}

// Sem e-mail confirmado não se convida (convidar manda e-mail e ocupa vaga).
func TestUnverifiedAccountCannotInvite(t *testing.T) {
	a, token, projeto, _ := contaNaoConfirmada(t)
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/projects/"+projeto+"/invitations", token,
		map[string]string{"email": "x@exemplo.com", "role": "viewer"})
	if codigo, _ := erroDe(t, corpo); status != http.StatusForbidden || codigo != "email_not_verified" {
		t.Errorf("convite de conta não confirmada deu %d %s, queria 403 email_not_verified", status, corpo)
	}
	if n := contarConvites(t); n != 0 {
		t.Errorf("%d convites gravados, queria 0", n)
	}
}

func contarConvites(t *testing.T) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(), `SELECT count(*) FROM project_invitations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// aceitar chama POST /invitations/accept.
func aceitar(t *testing.T, a *api, token, conviteToken string) (int, string) {
	t.Helper()
	return lerCru(t, a, http.MethodPost, "/api/v1/invitations/accept", token, map[string]string{"token": conviteToken})
}

// O aceite não exige e-mail confirmado: o token prova a caixa de entrada. E
// não confirma o e-mail.
func TestUnverifiedAccountCanAccept(t *testing.T) {
	a := newAPI(t)
	b := usuarioComEmailConfirmado(t, a, "b@exemplo.com")
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET email_verified_at = NULL WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	projeto := testdb.ProjetoPadrao(t, a.userID)
	convite := testdb.Convite(t, projeto, "b@exemplo.com", "viewer")
	if status, corpo := aceitar(t, a, tokenDe(t, a, b), convite); status != http.StatusOK {
		t.Fatalf("aceite de conta não confirmada deu %d %s, queria 200", status, corpo)
	}
	var confirmado *time.Time
	if err := a.pool.QueryRow(context.Background(), `SELECT email_verified_at FROM users WHERE id = $1`, b).Scan(&confirmado); err != nil {
		t.Fatal(err)
	}
	if confirmado != nil {
		t.Error("o aceite confirmou o e-mail da conta")
	}
}

// Depois do aceite, B vê o projeto na lista dele com o papel do convite, e A
// vê B na equipe. A resposta do aceite é o item de GET /projects.
func TestAcceptJoinsProjectAndListsIt(t *testing.T) {
	a := newAPI(t)
	b := usuarioComEmailConfirmado(t, a, "b@exemplo.com")
	tokB := tokenDe(t, a, b)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	convite := testdb.Convite(t, projeto, "b@exemplo.com", "member")

	status, corpo := aceitar(t, a, tokB, convite)
	if status != http.StatusOK {
		t.Fatalf("aceite deu %d %s", status, corpo)
	}
	aceito := decodificar(t, corpo)
	if aceito["id"] != projeto.String() || aceito["role"] != "member" || aceito["is_default"] != true {
		t.Errorf("resposta do aceite = %s", corpo)
	}
	if _, ok := aceito["limits"].(map[string]any); !ok {
		t.Errorf("resposta do aceite sem limits: %s", corpo)
	}

	_, lista := lerCru(t, a, http.MethodGet, "/api/v1/projects", tokB, nil)
	var projetos struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(lista), &projetos); err != nil {
		t.Fatal(err)
	}
	achou := false
	for _, p := range projetos.Items {
		if p["id"] == projeto.String() {
			achou = true
			if string(mustJSON(t, p)) != string(mustJSON(t, aceito)) {
				t.Errorf("o item da lista difere da resposta do aceite:\n%v\n%v", p, aceito)
			}
		}
	}
	if !achou {
		t.Errorf("GET /projects de B não traz o projeto: %s", lista)
	}

	_, membros := lerCru(t, a, http.MethodGet, "/api/v1/projects/"+projeto.String()+"/members", tokenDe(t, a, a.userID), nil)
	if !strings.Contains(membros, `"email":"b@exemplo.com","role":"member"`) {
		t.Errorf("GET /members de A não traz B como member: %s", membros)
	}

	if status, corpo := aceitar(t, a, tokB, convite); status != http.StatusBadRequest || !strings.Contains(corpo, `"token_invalid"`) {
		t.Errorf("segundo aceite deu %d %s, queria 400 token_invalid", status, corpo)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// O link de outra conta: 403 invitation_other_account; lixo, vazio e link
// inexistente: 400 token_invalid, o mesmo corpo.
func TestAcceptErrors(t *testing.T) {
	a := newAPI(t)
	c := usuarioComEmailConfirmado(t, a, "c@exemplo.com")
	tokC := tokenDe(t, a, c)
	convite := testdb.Convite(t, testdb.ProjetoPadrao(t, a.userID), "b@exemplo.com", "member")

	status, corpo := aceitar(t, a, tokC, convite)
	if codigo, _ := erroDe(t, corpo); status != http.StatusForbidden || codigo != "invitation_other_account" {
		t.Errorf("aceite por outra conta deu %d %s, queria 403 invitation_other_account", status, corpo)
	}
	if strings.Contains(corpo, "b@exemplo.com") {
		t.Errorf("o erro mostra o e-mail do convite: %s", corpo)
	}
	var invalidos []string
	for _, lixo := range []string{"", "abc", strings.Repeat("A", 43), strings.Repeat("x", 200)} {
		status, corpo := aceitar(t, a, tokC, lixo)
		if codigo, _ := erroDe(t, corpo); status != http.StatusBadRequest || codigo != "token_invalid" {
			t.Errorf("token %q deu %d %s, queria 400 token_invalid", lixo, status, corpo)
		}
		invalidos = append(invalidos, semTrace(t, corpo))
	}
	for _, c := range invalidos[1:] {
		if c != invalidos[0] {
			t.Errorf("token_invalid com corpos diferentes:\n%s\n%s", invalidos[0], c)
		}
	}
}

// A tradução dos erros do POST e o ciclo listar → revogar.
func TestInviteErrorsAndRevoke(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	membro := outroUsuario(t, a)
	testdb.Membro(t, projeto, membro, "viewer")

	casos := []struct {
		nome          string
		corpo         map[string]string
		status        int
		codigo, campo string
	}{
		{"e-mail inválido", map[string]string{"email": "sem-arroba", "role": "viewer"}, http.StatusBadRequest, "validation_failed", "email:invalid"},
		{"papel inválido", map[string]string{"email": "x@exemplo.com", "role": "admin"}, http.StatusBadRequest, "validation_failed", "role:invalid"},
		{"já é membro", map[string]string{"email": membro.String() + "@exemplo.com", "role": "member"}, http.StatusConflict, "already_member", ""},
	}
	for _, c := range casos {
		status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, c.corpo)
		codigo, campos := erroDe(t, corpo)
		if status != c.status || codigo != c.codigo || (c.campo != "" && (len(campos) != 1 || campos[0] != c.campo)) {
			t.Errorf("%s: %d %s, queria %d %s %s", c.nome, status, corpo, c.status, c.codigo, c.campo)
		}
	}

	// As três tentativas recusadas gastaram a rajada do balde de convites;
	// um servidor novo começa com o balde cheio.
	a.reiniciar(t)
	status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, map[string]string{"email": "Novo@Exemplo.com", "role": "manager"})
	if status != http.StatusCreated {
		t.Fatalf("convite deu %d %s", status, corpo)
	}
	criado := decodificar(t, corpo)
	if criado["email"] != "novo@exemplo.com" || criado["role"] != "manager" {
		t.Errorf("convite criado = %s (o e-mail sai normalizado)", corpo)
	}
	_, lista := lerCru(t, a, http.MethodGet, convitesDe(projeto), dono, nil)
	if !strings.Contains(lista, criado["id"].(string)) {
		t.Errorf("a lista não traz o convite: %s", lista)
	}
	if status, corpo := lerCru(t, a, http.MethodDelete, convitesDe(projeto)+"/"+criado["id"].(string), dono, nil); status != http.StatusNoContent {
		t.Errorf("revogar deu %d %s, queria 204", status, corpo)
	}
	for _, id := range []string{criado["id"].(string), "nao-e-uuid"} {
		if status, corpo := lerCru(t, a, http.MethodDelete, convitesDe(projeto)+"/"+id, dono, nil); status != http.StatusNotFound {
			t.Errorf("revogar %s deu %d %s, queria 404", id, status, corpo)
		}
	}
}

// O teto de membros do titular, contando os pendentes: 409.
func TestInviteMemberLimitIsConflict(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	for range 2 {
		testdb.Membro(t, projeto, outroUsuario(t, a), "viewer")
	}
	if status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, map[string]string{"email": "x@exemplo.com", "role": "viewer"}); status != http.StatusCreated {
		t.Fatalf("terceira vaga deu %d %s", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, map[string]string{"email": "y@exemplo.com", "role": "viewer"})
	if codigo, _ := erroDe(t, corpo); status != http.StatusConflict || codigo != "member_limit_reached" {
		t.Errorf("quarta vaga deu %d %s, queria 409 member_limit_reached", status, corpo)
	}
}

// Convidar tem balde próprio por usuário: três seguidos, o quarto é 429.
func TestInviteRateLimitedPerUser(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	testdb.Operador(t, a.userID) // folga no teto de membros: o teste é do balde
	for i, email := range []string{"a1@exemplo.com", "a2@exemplo.com", "a3@exemplo.com"} {
		if status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, map[string]string{"email": email, "role": "viewer"}); status != http.StatusCreated {
			t.Fatalf("convite %d deu %d %s", i+1, status, corpo)
		}
	}
	status, corpo := lerCru(t, a, http.MethodPost, convitesDe(projeto), dono, map[string]string{"email": "a4@exemplo.com", "role": "viewer"})
	if codigo, _ := erroDe(t, corpo); status != http.StatusTooManyRequests || codigo != "rate_limited" {
		t.Errorf("quarto convite deu %d %s, queria 429 rate_limited", status, corpo)
	}
	if n := contarConvites(t); n != 3 {
		t.Errorf("%d convites gravados, queria 3", n)
	}
}
