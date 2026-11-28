package account_test

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/legal"
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

// hasherContado conta as derivações e espera `demora` em cada uma. Grava um
// Argon2id de verdade, com o custo mínimo: a confirmação do e-mail o verifica
// com o mecanismo do login.
type hasherContado struct {
	chamadas atomic.Int32
	demora   time.Duration
}

func (h *hasherContado) hash(ctx context.Context, plain string) (string, error) {
	h.chamadas.Add(1)
	if h.demora > 0 {
		time.Sleep(h.demora)
	}
	return hashArgon2Barato(ctx, plain)
}

// hashArgon2Barato é um Argon2id válido com os parâmetros mínimos (8 KiB, t=1,
// p=1): auth.VerifyPassword o confere em microssegundos.
func hashArgon2Barato(_ context.Context, plain string) (string, error) {
	sal := make([]byte, 16)
	if _, err := rand.Read(sal); err != nil {
		return "", err
	}
	return auth.HashPasswordWith(plain, sal, 8, 1, 1)
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

func conferirVinculoDoTitular(t *testing.T, id uuid.UUID) {
	t.Helper()
	if n := contar(t, `SELECT count(*) FROM projects p JOIN project_members m ON m.project_id = p.id
		WHERE p.titular_id = $1 AND p.is_default AND m.user_id = $1 AND m.role = 'owner'`, id); n != 1 {
		t.Fatalf("%d Projetos padrão com owner; queria 1", n)
	}
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
	if err := a.cad.Cadastrar(context.Background(), email, senha, legal.VersaoAtual); err != nil {
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
	if err := a.cad.Cadastrar(ctx, "  fulano@exemplo.com ", senha, legal.VersaoAtual); err != nil {
		t.Fatal(err)
	}

	if n := contar(t, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("%d usuários; queria 1", n)
	}
	id := idDoEmail(t, "fulano@exemplo.com")
	if n := contar(t, `SELECT count(*) FROM users WHERE id = $1 AND email_verified_at IS NULL AND NOT is_operator`, id); n != 1 {
		t.Fatal("o usuário do cadastro nasceu confirmado ou operador")
	}
	conferirVinculoDoTitular(t, id)
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

			if err := a.cad.Cadastrar(context.Background(), email, "outra-senha-bem-comprida", legal.VersaoAtual); err != nil {
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

	if err := a.cad.Cadastrar(context.Background(), "Fulano@X.com", senha, legal.VersaoAtual); err != nil {
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
		cadastros.Go(func() { fim <- a.cad.Cadastrar(ctx, email, senha, legal.VersaoAtual) })
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
	var titular uuid.UUID
	var cadastrado string
	if err := admin.QueryRow(ctx, `SELECT id, email FROM users`).Scan(&titular, &cadastrado); err != nil {
		t.Fatal(err)
	}
	conferirVinculoDoTitular(t, titular)
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

type observarCadastro func(emailEnviado, account.EntregaDeEmail)

func (o observarCadastro) SendAccountEmail(_ context.Context, to, subject, body string, entrega account.EntregaDeEmail) error {
	o(emailEnviado{to: to, subject: subject, body: body}, entrega)
	return nil
}

func TestCadastrarFazOMesmoTrabalhoNosDoisRamos(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	const email = "fulano@exemplo.com"

	casos := []struct {
		nome  string
		senha string
		novo  bool
	}{
		{nome: "conta nova", senha: senha, novo: true},
		{nome: "conta existente", senha: "outra-senha-bem-comprida"},
	}
	for _, caso := range casos {
		chamadasDoBanco := a.pool.Stat().AcquireCount()
		var hashes, envios int
		mailer := observarCadastro(func(e emailEnviado, entrega account.EntregaDeEmail) {
			envios++
			if hashes != 1 {
				t.Errorf("%s: começou a enfileirar após %d derivações, queria uma", caso.nome, hashes)
			}
			if n := contar(t, `SELECT count(*) FROM users WHERE email = $1 AND password_hash = $2`, email, prefixoBarato+senha); n != 1 {
				t.Errorf("%s: a fila não viu o hash original já efetivado no banco", caso.nome)
			}
			if caso.novo {
				tokenHash := crypto.HashToken(tokenDoEmail(t, e))
				if n := contar(t, `SELECT count(*) FROM account_tokens t JOIN users u ON t.user_id = u.id
					WHERE u.email = $1 AND t.token_hash = $2 AND t.purpose = 'confirm_email'`, email, tokenHash); n != 1 {
					t.Error("a fila não viu o hash do link de confirmação já efetivado no banco")
				}
				if entrega.Preparar == nil || entrega.Concluir == nil {
					t.Error("confirmação sem preparação ou conclusão para o trabalhador")
				}
			} else if entrega.Preparar != nil || entrega.Concluir != nil || !strings.Contains(e.body, linksDeTeste.EsqueciASenha()) {
				t.Error("o aviso de conta existente deveria apontar para a recuperação sem emitir um link")
			}
		})
		cad := account.NewCadastro(a.pool, a.clk, mailer, linksDeTeste)
		cad.SetHash(func(ctx context.Context, plain string) (string, error) {
			hashes++
			if plain != caso.senha {
				t.Errorf("%s: o hasher não recebeu a senha deste pedido", caso.nome)
			}
			if a.pool.Stat().AcquireCount() != chamadasDoBanco {
				t.Errorf("%s: começou a derivar depois de acessar o banco", caso.nome)
			}
			return hashBarato(ctx, plain)
		})

		if err := cad.Cadastrar(ctx, email, caso.senha, legal.VersaoAtual); err != nil {
			t.Fatal(err)
		}
		if hashes != 1 || envios != 1 {
			t.Errorf("%s: %d derivações e %d e-mails, queria um de cada", caso.nome, hashes, envios)
		}
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
	ctx := t.Context()
	const rodadas = 5
	const existente = "existente@exemplo.com"
	// A chamada pública termina depois de enfileirar. A confirmação do
	// transporte e o SQL do trabalhador ficam fora do cronômetro.
	fila := make(mailerAdiado, 1+2*rodadas)
	cad := account.NewCadastro(a.pool, a.clk, fila, linksDeTeste)
	cad.SetHash(a.hasher.hash)
	if err := cad.Cadastrar(ctx, existente, senha, legal.VersaoAtual); err != nil {
		t.Fatal(err)
	}

	var novos, existentes []time.Duration
	for i := range rodadas {
		inicio := time.Now()
		if err := cad.Cadastrar(ctx, fmt.Sprintf("novo%d@exemplo.com", i), senha, legal.VersaoAtual); err != nil {
			t.Fatal(err)
		}
		novos = append(novos, time.Since(inicio))

		inicio = time.Now()
		if err := cad.Cadastrar(ctx, existente, senha, legal.VersaoAtual); err != nil {
			t.Fatal(err)
		}
		existentes = append(existentes, time.Since(inicio))
	}

	if n := len(fila); n != cap(fila) {
		t.Fatalf("%d mensagens na fila, queria %d", n, cap(fila))
	}
	medianaNova, medianaExistente := mediana(novos), mediana(existentes)
	diferenca := medianaNova - medianaExistente
	if diferenca < 0 {
		diferenca = -diferenca
	}
	t.Logf("medianas de cadastro: conta nova %v, existente %v, diferença %v", medianaNova, medianaExistente, diferenca)
	if diferenca >= 40*time.Millisecond {
		t.Fatalf("diferença %v entre as medianas, queria menos de 40 ms", diferenca)
	}
}

func TestCadastrarValidaAntesDeTudo(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()

	if err := a.cad.Cadastrar(ctx, "Nome <a@b.com>", senha, legal.VersaoAtual); !errors.Is(err, account.ErrInvalidEmail) {
		t.Fatalf("e-mail inválido: %v; queria ErrInvalidEmail", err)
	}
	err := a.cad.Cadastrar(ctx, "fulano@exemplo.com", strings.Repeat("a", 14), legal.VersaoAtual)
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

// falhasDeLogin lê o contador do bloqueio da conta (o do login).
func falhasDeLogin(t *testing.T, id uuid.UUID) (int, bool) {
	t.Helper()
	var n int
	var travada bool
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT failed_attempts, locked_until IS NOT NULL FROM users WHERE id = $1`, id).Scan(&n, &travada); err != nil {
		t.Fatal(err)
	}
	return n, travada
}

func TestConfirmarEmailMarcaEConsome(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); err != nil {
		t.Fatal(err)
	}
	if !emailConfirmado(t, id) {
		t.Fatal("o e-mail continua não confirmado")
	}
	if n := contar(t, `SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'user.email_verified'`, id); n != 1 {
		t.Fatalf("%d auditorias user.email_verified; queria 1", n)
	}
	if n, _ := falhasDeLogin(t, id); n != 0 {
		t.Errorf("a senha certa ficou contada no bloqueio: %d falhas, queria 0", n)
	}
	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("segundo uso do token: %v; queria ErrTokenInvalido", err)
	}
}

// A senha errada não confirma, não gasta o link e conta no bloqueio do login:
// a confirmação não é um oráculo de senha à parte.
func TestConfirmarEmailSenhaErradaNaoGastaOLink(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	if err := a.cad.ConfirmarEmail(ctx, token, "outra-senha-bem-comprida", nil); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("senha errada: %v; queria ErrInvalidCredentials", err)
	}
	if emailConfirmado(t, id) {
		t.Fatal("a senha errada confirmou o e-mail")
	}
	if n, _ := falhasDeLogin(t, id); n != 1 {
		t.Errorf("%d falhas no bloqueio da conta, queria 1", n)
	}
	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); err != nil {
		t.Fatalf("o link não valeu depois da senha errada: %v", err)
	}
	if !emailConfirmado(t, id) {
		t.Fatal("a senha certa não confirmou o e-mail")
	}
	if n, _ := falhasDeLogin(t, id); n != 1 {
		t.Errorf("a senha certa mexeu no contador: %d falhas, queria 1 (só a errada conta)", n)
	}
}

// A senha vazia é 401 sem Argon2id e sem contar tentativa, e o link continua
// valendo.
func TestConfirmarEmailSenhaVazia(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	antes := auth.Argon2Verifications()
	if err := a.cad.ConfirmarEmail(ctx, token, "", nil); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("senha vazia: %v; queria ErrInvalidCredentials", err)
	}
	if n := auth.Argon2Verifications() - antes; n != 0 {
		t.Errorf("senha vazia rodou %d Argon2id, queria 0", n)
	}
	if n, _ := falhasDeLogin(t, id); n != 0 {
		t.Errorf("senha vazia contou %d falhas, queria 0", n)
	}
	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); err != nil {
		t.Fatalf("o link não valeu depois da senha vazia: %v", err)
	}
}

func TestConfirmarEmailVencido(t *testing.T) {
	a := novoCadastro(t)
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	a.clk.Advance(24*time.Hour + time.Second)

	if err := a.cad.ConfirmarEmail(context.Background(), token, senha, nil); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("token vencido: %v; queria ErrTokenInvalido", err)
	}
	if emailConfirmado(t, id) {
		t.Fatal("o token vencido confirmou o e-mail")
	}
}

// O token inválido vem antes da senha: com a certa, a errada ou nenhuma, é
// ErrTokenInvalido, sem Argon2id e sem contar tentativa.
func TestConfirmarEmailTokenLixo(t *testing.T) {
	a := novoCadastro(t)
	cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	antes := auth.Argon2Verifications()
	for _, token := range []string{"x", ""} {
		for _, s := range []string{senha, "outra-senha-bem-comprida", ""} {
			if err := a.cad.ConfirmarEmail(context.Background(), token, s, nil); !errors.Is(err, account.ErrTokenInvalido) {
				t.Fatalf("token %q, senha %q: %v; queria ErrTokenInvalido", token, s, err)
			}
		}
	}
	if n := auth.Argon2Verifications() - antes; n != 0 {
		t.Errorf("token inválido rodou %d Argon2id, queria 0", n)
	}
	if n, _ := falhasDeLogin(t, id); n != 0 {
		t.Errorf("token inválido contou %d falhas, queria 0", n)
	}
}

// Cinco senhas erradas travam a conta no bloqueio do login, com a auditoria do
// login e o ip_hash de quem deu a quinta; travada, até a senha certa é
// auth.LockedError com o fim da trava, sem contar nada, e o link continua
// valendo para quando a trava vencer.
func TestConfirmarEmailBloqueiaComoOLogin(t *testing.T) {
	a := novoCadastro(t)
	ctx := context.Background()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")
	ipDe := func(i int) []byte { return crypto.HashToken(fmt.Sprintf("origem-%d", i)) }

	for i := range auth.LockThreshold {
		if err := a.cad.ConfirmarEmail(ctx, token, "outra-senha-bem-comprida", ipDe(i)); !errors.Is(err, account.ErrInvalidCredentials) {
			t.Fatalf("senha errada %d: %v; queria ErrInvalidCredentials", i+1, err)
		}
	}
	travouEm := a.clk.Now()
	if n, travada := falhasDeLogin(t, id); n != auth.LockThreshold || !travada {
		t.Fatalf("%d falhas, travada = %v; queria %d e travada", n, travada, auth.LockThreshold)
	}
	var ipDaTrava []byte
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT ip_hash FROM audit_log WHERE user_id = $1 AND action = 'user.locked'`, id).Scan(&ipDaTrava); err != nil {
		t.Fatalf("auditoria user.locked: %v", err)
	}
	if !bytes.Equal(ipDaTrava, ipDe(auth.LockThreshold-1)) {
		t.Errorf("ip_hash da trava = %x, queria o da quinta tentativa", ipDaTrava)
	}

	a.clk.Advance(time.Minute)
	err := a.cad.ConfirmarEmail(ctx, token, senha, nil)
	var travada auth.LockedError
	if !errors.Is(err, auth.ErrLocked) || !errors.As(err, &travada) {
		t.Fatalf("senha certa com a conta travada: %v; queria auth.LockedError", err)
	}
	if want := travouEm.Add(auth.LockDuration); !travada.Until.Equal(want) {
		t.Errorf("Until = %v, queria %v (o fim da trava)", travada.Until, want)
	}
	if emailConfirmado(t, id) {
		t.Fatal("a conta travada confirmou o e-mail")
	}
	if n, _ := falhasDeLogin(t, id); n != auth.LockThreshold {
		t.Errorf("a conta travada contou tentativa: %d falhas, queria %d", n, auth.LockThreshold)
	}

	a.clk.Advance(auth.LockDuration)
	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); err != nil {
		t.Fatalf("depois da trava: %v", err)
	}
	if !emailConfirmado(t, id) {
		t.Fatal("a senha certa depois da trava não confirmou")
	}
}

// A senha trocada entre a verificação e a transação não confirma: a senha
// conferida já não é a da conta. O link continua valendo.
func TestConfirmarEmailSenhaTrocadaNoMeioRecusa(t *testing.T) {
	a := novoCadastro(t)
	ctx := t.Context()
	token := cadastrarEPegarToken(t, a, "fulano@exemplo.com")
	id := idDoEmail(t, "fulano@exemplo.com")

	soltar := segurarTrava(t, a.pool, id)
	fim := make(chan error, 1)
	go func() { fim <- a.cad.ConfirmarEmail(ctx, token, senha, nil) }()
	if esperou, err := esperarNaTravaDoUsuario(t, a.pool, id, fim); !esperou {
		soltar()
		t.Fatalf("a confirmação terminou antes de esperar a trava do usuário: %v", err)
	}
	outro, err := hashArgon2Barato(ctx, "a-senha-trocada-no-meio")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, outro); err != nil {
		t.Fatal(err)
	}
	soltar()
	if err := <-fim; !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("confirmação com a senha antiga: %v; queria ErrInvalidCredentials", err)
	}
	if emailConfirmado(t, id) {
		t.Fatal("a senha antiga confirmou o e-mail")
	}
	if err := a.cad.ConfirmarEmail(ctx, token, "a-senha-trocada-no-meio", nil); err != nil {
		t.Fatalf("o link não valeu com a senha nova: %v", err)
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
			erros[i] = a.cad.ConfirmarEmail(context.Background(), token, senha, nil)
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
	if f, _ := falhasDeLogin(t, id); f != 0 {
		t.Errorf("as senhas certas ficaram contadas: %d falhas, queria 0", f)
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

	if err := a.cad.ConfirmarEmail(ctx, antigo, senha, nil); !errors.Is(err, account.ErrTokenInvalido) {
		t.Fatalf("token antigo: %v; queria ErrTokenInvalido", err)
	}
	if err := a.cad.ConfirmarEmail(ctx, novoToken, senha, nil); err != nil {
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
	if err := a.cad.ConfirmarEmail(ctx, token, senha, nil); err != nil {
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

	if err := cad.Cadastrar(ctx, "fulano@exemplo.com", senha, legal.VersaoAtual); err != nil {
		t.Fatalf("conta nova com o mailer fora do ar: %v; queria nil", err)
	}
	if err := cad.Cadastrar(ctx, "fulano@exemplo.com", senha, legal.VersaoAtual); err != nil {
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

// aceiteDe lê a versão dos termos aceita e o instante do aceite da conta;
// sem aceite, "" e o instante zero.
func aceiteDe(t *testing.T, email string) (string, time.Time) {
	t.Helper()
	var versao string
	var em time.Time
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT coalesce(terms_version, ''), coalesce(terms_accepted_at, 'epoch') FROM users WHERE email = $1`,
		email).Scan(&versao, &em); err != nil {
		t.Fatal(err)
	}
	return versao, em
}

func TestCadastroGravaVersaoAceita(t *testing.T) {
	a := novoCadastro(t)
	const email = "fulano@exemplo.com"
	cadastrarEPegarToken(t, a, email)

	versao, em := aceiteDe(t, email)
	if versao != legal.VersaoAtual {
		t.Fatalf("terms_version = %q; queria %q", versao, legal.VersaoAtual)
	}
	if !em.Equal(a.clk.Now()) {
		t.Fatalf("terms_accepted_at = %v; queria %v", em, a.clk.Now())
	}
}

// O ramo do e-mail já cadastrado não grava nada: nem o aceite da conta antiga
// muda, mesmo com outra versão e outro instante.
func TestCadastroDeEmailExistenteNaoTrocaAceite(t *testing.T) {
	a := novoCadastro(t)
	const email = "fulano@exemplo.com"
	cadastrarEPegarToken(t, a, email)
	antigo := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`UPDATE users SET terms_version = '2020-01-01', terms_accepted_at = $1 WHERE email = $2`, antigo, email); err != nil {
		t.Fatal(err)
	}
	a.clk.Advance(time.Hour)

	if err := a.cad.Cadastrar(context.Background(), email, senha, legal.VersaoAtual); err != nil {
		t.Fatal(err)
	}

	versao, em := aceiteDe(t, email)
	if versao != "2020-01-01" || !em.Equal(antigo) {
		t.Fatalf("aceite da conta antiga = %q, %v; queria 2020-01-01, %v", versao, em, antigo)
	}
}
