package account_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// emailEnviado é o que o mailer falso recebeu.
type emailEnviado struct{ to, subject, body string }

// mailerFalso grava os e-mails de conta, em vez de enfileirá-los.
type mailerFalso struct {
	mu       sync.Mutex
	enviados []emailEnviado
}

func (m *mailerFalso) SendAccountEmail(ctx context.Context, to, subject, body string, entrega account.EntregaDeEmail) error {
	m.mu.Lock()
	m.enviados = append(m.enviados, emailEnviado{to, subject, body})
	m.mu.Unlock()
	if entrega.Concluir != nil {
		return entrega.Concluir(ctx, account.AceitoPeloTransporte)
	}
	return nil
}

func (m *mailerFalso) todos() []emailEnviado {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.enviados)
}

func (m *mailerFalso) limpar() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enviados = nil
}

// hasherContado conta as derivações e espera `demora` em cada uma.
type hasherContado struct {
	chamadas atomic.Int32
	demora   time.Duration
}

func (h *hasherContado) hash(_ context.Context, plain string) (string, error) {
	h.chamadas.Add(1)
	if h.demora > 0 {
		time.Sleep(h.demora)
	}
	return prefixoBarato + plain, nil
}

var linksDeTeste = account.Links{Base: "https://painel.exemplo"}

type ambienteCadastro struct {
	cad    *account.Cadastro
	pool   *pgxpool.Pool
	clk    *clock.Fake
	mailer *mailerFalso
	hasher *hasherContado
}

func novoCadastro(t *testing.T) ambienteCadastro {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	mailer := &mailerFalso{}
	hasher := &hasherContado{}
	cad := account.NewCadastro(pool, clk, mailer, linksDeTeste)
	cad.SetHash(hasher.hash)
	return ambienteCadastro{cad: cad, pool: pool, clk: clk, mailer: mailer, hasher: hasher}
}

// tokenDoEmail extrai o token do link de confirmação no corpo do e-mail.
func tokenDoEmail(t *testing.T, e emailEnviado) string {
	t.Helper()
	_, depois, ok := strings.Cut(e.body, "#token=")
	if !ok {
		t.Fatalf("e-mail sem link com token: %q", e.body)
	}
	token, _, _ := strings.Cut(depois, "\n")
	return token
}

func contar(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := testdb.Admin(t).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func idDoEmail(t *testing.T, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func emailConfirmado(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var ok bool
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT email_verified_at IS NOT NULL FROM users WHERE id = $1`, id).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// cadastrarEPegarToken cadastra um e-mail novo e devolve o token do e-mail de
// confirmação, com o mailer limpo de novo.
func cadastrarEPegarToken(t *testing.T, a ambienteCadastro, email string) string {
	t.Helper()
	if err := a.cad.Cadastrar(context.Background(), email, senha); err != nil {
		t.Fatal(err)
	}
	enviados := a.mailer.todos()
	if len(enviados) != 1 {
		t.Fatalf("%d e-mails no cadastro; queria 1", len(enviados))
	}
	a.mailer.limpar()
	return tokenDoEmail(t, enviados[0])
}

func TestCadastrarContaNovaCriaUsuarioProjetoEToken(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	if err := a.cad.Cadastrar(ctx, "  fulano@exemplo.com ", senha); err != nil {
		t.Fatal(err)
	}

	if n := contar(t, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("%d usuários; queria 1", n)
	}
	id := idDoEmail(t, "fulano@exemplo.com")
	if n := contar(t, `SELECT count(*) FROM users WHERE id = $1 AND email_verified_at IS NULL AND NOT is_operator`, id); n != 1 {
		t.Fatal("o usuário do cadastro nasceu confirmado ou operador")
	}
	if n := contar(t, `SELECT count(*) FROM projects p JOIN project_members m ON m.project_id = p.id
		WHERE p.titular_id = $1 AND p.is_default AND m.user_id = $1 AND m.role = 'owner'`, id); n != 1 {
		t.Fatalf("%d Projetos padrão com owner; queria 1", n)
	}
	vence := a.clk.Now().Add(24 * time.Hour)
	if n := contar(t, `SELECT count(*) FROM account_tokens
		WHERE user_id = $1 AND purpose = 'confirm_email' AND used_at IS NULL AND expires_at = $2`, id, vence); n != 1 {
		t.Fatalf("%d tokens de confirmação vencendo em 24 h; queria 1", n)
	}

	enviados := a.mailer.todos()
	if len(enviados) != 1 {
		t.Fatalf("%d e-mails; queria 1", len(enviados))
	}
	token := tokenDoEmail(t, enviados[0])
	if enviados[0].to != "fulano@exemplo.com" {
		t.Fatalf("destinatário %q; queria o e-mail normalizado", enviados[0].to)
	}
	if !strings.Contains(enviados[0].body, linksDeTeste.ConfirmarEmail(token)) {
		t.Fatalf("corpo sem o link de confirmação: %q", enviados[0].body)
	}
	if n := contar(t, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.signed_up'`, id); n != 1 {
		t.Fatalf("%d auditorias user.signed_up; queria 1", n)
	}
}

func TestCadastrarContaExistenteMandaJaTemConta(t *testing.T) {
	casos := []struct {
		nome  string
		criar func(t *testing.T, a ambienteCadastro, email string)
	}{
		{"confirmada", func(t *testing.T, a ambienteCadastro, email string) {
			svc := comHasherBarato(account.NewService(a.pool, nil, a.clk, nil))
			if _, err := svc.CreateUser(context.Background(), email, senha); err != nil {
				t.Fatal(err)
			}
		}},
		{"não confirmada", func(t *testing.T, a ambienteCadastro, email string) {
			cadastrarEPegarToken(t, a, email)
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoCadastro(t)
			const email = "fulano@exemplo.com"
			c.criar(t, a, email)
			usuarios := contar(t, `SELECT count(*) FROM users`)
			tokens := contar(t, `SELECT count(*) FROM account_tokens`)
			var hashAntes string
			if err := testdb.Admin(t).QueryRow(context.Background(),
				`SELECT password_hash FROM users WHERE email = $1`, email).Scan(&hashAntes); err != nil {
				t.Fatal(err)
			}

			if err := a.cad.Cadastrar(context.Background(), email, "outra-senha-bem-comprida"); err != nil {
				t.Fatalf("Cadastrar com conta existente: %v; queria nil", err)
			}

			if n := contar(t, `SELECT count(*) FROM users`); n != usuarios {
				t.Fatalf("%d usuários; queria %d", n, usuarios)
			}
			if n := contar(t, `SELECT count(*) FROM account_tokens`); n != tokens {
				t.Fatalf("%d tokens; queria %d", n, tokens)
			}
			if n := contar(t, `SELECT count(*) FROM users WHERE email = $1 AND password_hash = $2`, email, hashAntes); n != 1 {
				t.Fatal("o cadastro repetido trocou a senha da conta existente")
			}
			enviados := a.mailer.todos()
			if len(enviados) != 1 {
				t.Fatalf("%d e-mails; queria 1", len(enviados))
			}
			if enviados[0].to != email || !strings.Contains(enviados[0].body, linksDeTeste.EsqueciASenha()) {
				t.Fatalf("e-mail %+v; queria o \"já tem conta\" com o link de esqueci a senha", enviados[0])
			}
			if strings.Contains(enviados[0].body, "#token=") {
				t.Fatal("o e-mail de conta existente leva um token")
			}
		})
	}
}

func TestCadastrarComCaixaDiferenteContaComoExistente(t *testing.T) {
	a := novoCadastro(t)
	const emailCadastrado = "FuLaNo@x.com"
	cadastrarEPegarToken(t, a, emailCadastrado)

	if err := a.cad.Cadastrar(context.Background(), "Fulano@X.com", senha); err != nil {
		t.Fatal(err)
	}
	if n := contar(t, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("%d usuários; queria 1", n)
	}
	enviados := a.mailer.todos()
	if len(enviados) != 1 || !strings.Contains(enviados[0].body, linksDeTeste.EsqueciASenha()) {
		t.Fatalf("e-mails %+v; queria 1 \"já tem conta\"", enviados)
	}
	if enviados[0].to != emailCadastrado {
		t.Fatalf("destinatário %q; queria o endereço cadastrado %q", enviados[0].to, emailCadastrado)
	}
}

func TestCadastrarSimultaneoUsaGrafiaDaContaCriada(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	admin := testdb.Admin(t)
	trava, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cadastros sync.WaitGroup
	defer func() {
		_ = trava.Rollback(context.Background())
		cadastros.Wait()
	}()
	// Quem insere a conta fica preso no Projeto padrão enquanto o outro
	// cadastro espera pela restrição de e-mail único.
	if _, err := trava.Exec(ctx, `LOCK TABLE projects IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	fim := make(chan error, 2)
	for _, email := range []string{"FuLaNo@exemplo.com", "fulano@exemplo.com"} {
		cadastros.Go(func() { fim <- a.cad.Cadastrar(ctx, email, senha) })
	}
	if !testdb.EsperarBloqueados(t, admin, 2, fim) {
		t.Fatal("um cadastro terminou antes de os dois alcançarem as travas")
	}
	if err := trava.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-fim; err != nil {
			t.Fatalf("Cadastrar: %v; queria nil", err)
		}
	}

	if n := contar(t, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("%d usuários; queria 1", n)
	}
	if n := contar(t, `SELECT count(*) FROM projects`); n != 1 {
		t.Fatalf("%d projetos; queria 1", n)
	}
	var cadastrado string
	if err := admin.QueryRow(ctx, `SELECT email FROM users`).Scan(&cadastrado); err != nil {
		t.Fatal(err)
	}
	enviados := a.mailer.todos()
	if len(enviados) != 2 {
		t.Fatalf("%d e-mails; queria 2", len(enviados))
	}
	var confirmacoes, avisos int
	for _, email := range enviados {
		if email.to != cadastrado {
			t.Errorf("destinatário %q; queria a grafia gravada %q", email.to, cadastrado)
		}
		switch {
		case strings.Contains(email.body, linksDeTeste.ConfirmarEmail("")):
			confirmacoes++
		case strings.Contains(email.body, linksDeTeste.EsqueciASenha()):
			avisos++
		}
	}
	if confirmacoes != 1 || avisos != 1 {
		t.Errorf("%d confirmações e %d avisos de conta existente; queria 1 de cada", confirmacoes, avisos)
	}
}

func TestCadastrarFazOMesmoTrabalhoNosDoisRamos(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()

	if err := a.cad.Cadastrar(ctx, "fulano@exemplo.com", senha); err != nil {
		t.Fatal(err)
	}
	if h, e := a.hasher.chamadas.Load(), len(a.mailer.todos()); h != 1 || e != 1 {
		t.Fatalf("conta nova: %d hashes e %d e-mails; queria 1 e 1", h, e)
	}

	a.hasher.chamadas.Store(0)
	a.mailer.limpar()
	if err := a.cad.Cadastrar(ctx, "fulano@exemplo.com", senha); err != nil {
		t.Fatal(err)
	}
	if h, e := a.hasher.chamadas.Load(), len(a.mailer.todos()); h != 1 || e != 1 {
		t.Fatalf("conta existente: %d hashes e %d e-mails; queria 1 e 1", h, e)
	}
}

func mediana(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

func TestCadastrarLevaTempoParecidoNosDoisRamos(t *testing.T) {
	a := novoCadastro(t)
	a.hasher.demora = 50 * time.Millisecond
	ctx := context.Background()
	const rodadas = 5
	const existente = "existente@exemplo.com"
	cadastrarEPegarToken(t, a, existente)

	var novos, existentes []time.Duration
	for i := range rodadas {
		inicio := time.Now()
		if err := a.cad.Cadastrar(ctx, fmt.Sprintf("novo%d@exemplo.com", i), senha); err != nil {
			t.Fatal(err)
		}
		novos = append(novos, time.Since(inicio))

		inicio = time.Now()
		if err := a.cad.Cadastrar(ctx, existente, senha); err != nil {
			t.Fatal(err)
		}
		existentes = append(existentes, time.Since(inicio))
	}

	diferenca := mediana(novos) - mediana(existentes)
	if diferenca < 0 {
		diferenca = -diferenca
	}
	if diferenca >= 40*time.Millisecond {
		t.Fatalf("medianas: conta nova %v, existente %v; a diferença %v denuncia a conta",
			mediana(novos), mediana(existentes), diferenca)
	}
}

func TestCadastrarValidaAntesDeTudo(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()

	if err := a.cad.Cadastrar(ctx, "Nome <a@b.com>", senha); !errors.Is(err, account.ErrInvalidEmail) {
		t.Fatalf("e-mail inválido: %v; queria ErrInvalidEmail", err)
	}
	err := a.cad.Cadastrar(ctx, "fulano@exemplo.com", strings.Repeat("a", 14))
	var pe account.PolicyError
	if !errors.As(err, &pe) || pe.Reason != auth.ReasonTooShort {
		t.Fatalf("senha de 14: %v; queria PolicyError too_short", err)
	}
	if h, e := a.hasher.chamadas.Load(), len(a.mailer.todos()); h != 0 || e != 0 {
		t.Fatalf("%d hashes e %d e-mails com entrada inválida; queria 0 e 0", h, e)
	}
	if n := contar(t, `SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("%d usuários; queria 0", n)
	}
}

func TestConfirmarEmailMarcaEConsome(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	if err := a.cad.ConfirmarEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	if !emailConfirmado(t, id) {
		t.Fatal("o e-mail continua não confirmado")
	}
	if n := contar(t, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.email_verified'`, id); n != 1 {
		t.Fatalf("%d auditorias user.email_verified; queria 1", n)
	}
	if err := a.cad.ConfirmarEmail(ctx, token); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("segundo uso do token: %v; queria ErrTokenInvalido", err)
	}
}

func TestConfirmarEmailVencido(t *testing.T) {
	a := novoCadastro(t)
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	a.clk.Advance(24*time.Hour + time.Second)

	if err := a.cad.ConfirmarEmail(context.Background(), token); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("token vencido: %v; queria ErrTokenInvalido", err)
	}
	if emailConfirmado(t, id) {
		t.Fatal("o token vencido confirmou o e-mail")
	}
}

func TestConfirmarEmailTokenLixo(t *testing.T) {
	a := novoCadastro(t)
	cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	for _, token := range []string{"x", ""} {
		if err := a.cad.ConfirmarEmail(context.Background(), token); !errors.Is(err, account.ErrTokenInvalido) {
			t.Fatalf("token %q: %v; queria ErrTokenInvalido", token, err)
		}
	}
}

func TestConfirmacaoSimultaneaSoUmaVence(t *testing.T) {
	a := novoCadastro(t)
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	const n = 20

	var largada sync.WaitGroup
	largada.Add(1)
	erros := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			largada.Wait()
			erros[i] = a.cad.ConfirmarEmail(context.Background(), token)
		})
	}
	largada.Done()
	wg.Wait()

	var venceram, invalidos int
	for _, err := range erros {
		switch {
		case err == nil:
			venceram++
		case errors.Is(err, account.ErrTokenInvalido):
			invalidos++
		default:
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	if venceram != 1 || invalidos != n-1 {
		t.Fatalf("%d venceram e %d inválidos; queria 1 e %d", venceram, invalidos, n-1)
	}
	if c := contar(t, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.email_verified'`, id); c != 1 {
		t.Fatalf("%d auditorias user.email_verified; queria 1", c)
	}
}

func TestReenviarInvalidaOLinkAnterior(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	antigo := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	if err := a.cad.ReenviarConfirmacao(ctx, id); err != nil {
		t.Fatal(err)
	}
	enviados := a.mailer.todos()
	if len(enviados) != 1 || enviados[0].to != "fulano@exemplo.com" {
		t.Fatalf("e-mails %+v; queria 1 para fulano@exemplo.com", enviados)
	}
	novoToken := tokenDoEmail(t, enviados[0])
	if novoToken == antigo {
		t.Fatal("o reenvio repetiu o token")
	}
	if !strings.Contains(enviados[0].body, linksDeTeste.ConfirmarEmail(novoToken)) {
		t.Fatalf("corpo sem o link de confirmação: %q", enviados[0].body)
	}

	if err := a.cad.ConfirmarEmail(ctx, antigo); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("token antigo: %v; queria ErrTokenInvalido", err)
	}
	if err := a.cad.ConfirmarEmail(ctx, novoToken); err != nil {
		t.Fatalf("token novo: %v", err)
	}
	if !emailConfirmado(t, id) {
		t.Fatal("o token novo não confirmou o e-mail")
	}
}

func TestReenviarJaConfirmadoNaoManda(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	if err := a.cad.ConfirmarEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	tokens := contar(t, `SELECT count(*) FROM account_tokens WHERE user_id = $1`, id)

	if err := a.cad.ReenviarConfirmacao(ctx, id); err != nil {
		t.Fatalf("reenviar já confirmado: %v; queria nil", err)
	}
	if e := len(a.mailer.todos()); e != 0 {
		t.Fatalf("%d e-mails; queria 0", e)
	}
	if n := contar(t, `SELECT count(*) FROM account_tokens WHERE user_id = $1`, id); n != tokens {
		t.Fatalf("%d tokens; queria %d (nenhum novo)", n, tokens)
	}
}

// mailerForaDoAr recusa todo e-mail, como uma fila que não aceita mais nada.
type mailerForaDoAr struct{ chamadas atomic.Int32 }

func (m *mailerForaDoAr) SendAccountEmail(context.Context, string, string, string, account.EntregaDeEmail) error {
	m.chamadas.Add(1)
	return errors.New("falha sintética: endereço, corpo e token da mensagem")
}

// capturarLog troca o logger padrão por um que escreve no buffer devolvido, e
// desfaz a troca no fim do teste.
func capturarLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var saida bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&saida, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	return &saida
}

// Com o mailer fora do ar, o cadastro devolve nil nos dois ramos: um erro só
// no ramo que manda e-mail seria um oráculo de conta, e os dois ramos mandam.
// O erro vai para o log, sem o endereço e sem o token.
func TestCadastrarComMailerForaDoArDevolveNilNosDoisRamos(t *testing.T) {
	a := novoCadastro(t)
	mailer := &mailerForaDoAr{}
	cad := account.NewCadastro(a.pool, a.clk, mailer, linksDeTeste)
	cad.SetHash(a.hasher.hash)
	log := capturarLog(t)
	ctx := context.Background()

	if err := cad.Cadastrar(ctx, "fulano@exemplo.com", senha); err != nil {
		t.Fatalf("conta nova com o mailer fora do ar: %v; queria nil", err)
	}
	if err := cad.Cadastrar(ctx, "fulano@exemplo.com", senha); err != nil {
		t.Fatalf("conta existente com o mailer fora do ar: %v; queria nil", err)
	}
	if n := mailer.chamadas.Load(); n != 2 {
		t.Fatalf("%d e-mails tentados; queria 2", n)
	}
	if n := contar(t, `SELECT count(*) FROM users WHERE email = 'fulano@exemplo.com'`); n != 1 {
		t.Fatalf("%d usuários; queria 1 (a conta nasce mesmo sem o e-mail)", n)
	}
	saida := log.String()
	if !strings.Contains(saida, "e-mail de conta não enfileirado") {
		t.Errorf("o erro do mailer não foi para o log: %s", saida)
	}
	if strings.Contains(saida, "fulano") || strings.Contains(saida, "token") {
		t.Errorf("o log traz o endereço ou o token: %s", saida)
	}
}
