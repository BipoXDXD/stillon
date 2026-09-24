package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// senhaMarcada é distinta o bastante para ser procurada na saída: ela nunca
// pode aparecer no stdout nem no stderr.
const senhaMarcada = "marca-d-agua-7f3a9c-senha-do-teste"

type execucao struct {
	codigo         int
	stdout, stderr string
}

func (e execucao) saida() string { return e.stdout + e.stderr }

// rodarUser chama runUser com o DSN do papel da aplicação e a senha num pipe.
func rodarUser(t *testing.T, dsn string, stdin string, args ...string) execucao {
	t.Helper()
	return rodarUserCom(t, dsn, entradaDaSenha{pipe: strings.NewReader(stdin)}, nil, args...)
}

func rodarUserCom(t *testing.T, dsn string, in entradaDaSenha, env map[string]string, args ...string) execucao {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return dsn
		}
		return env[k]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := runUser(ctx, args, in, &out, &errb, getenv)
	return execucao{codigo: c, stdout: out.String(), stderr: errb.String()}
}

func usuarios(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// senhaConfere lê o hash gravado e o confere com a senha dada.
func senhaGravadaConfere(t *testing.T, pool *pgxpool.Pool, email, senha string) bool {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE email = $1`, email).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	ok, _, err := auth.VerifyPassword(hash, senha)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func auditorias(t *testing.T, pool *pgxpool.Pool, acao string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1`, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUserCreateFromPipe(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	r := rodarUser(t, dsn, senhaMarcada+"\n", "create", "--email", "  Dono@Exemplo.com ")
	if r.codigo != saidaOK {
		t.Fatalf("código %d, queria 0; stderr: %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stdout, "Dono@Exemplo.com") {
		t.Errorf("stdout não diz quem foi criado: %q", r.stdout)
	}
	// O e-mail sai normalizado como no login (espaço em volta) e com a caixa
	// preservada; o citext compara sem ela.
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("o hash gravado não confere com a senha do stdin")
	}
	if n := auditorias(t, pool, "user.created"); n != 1 {
		t.Errorf("%d registros user.created, queria 1", n)
	}
}

// Um \n ou \r\n final sai; mais de uma linha é recusado. Provado pelo
// set-password, que troca a senha do mesmo usuário a cada rodada.
func TestUserPasswordLineHandling(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("create: código %d; %s", r.codigo, r.stderr)
	}

	for fim, nome := range map[string]string{"\n": "lf", "\r\n": "crlf", "": "nada"} {
		nova := "outra-senha-comprida-" + nome
		r := rodarUser(t, dsn, nova+fim, "set-password", "--email", "dono@exemplo.com")
		if r.codigo != saidaOK {
			t.Fatalf("fim %q: código %d; %s", fim, r.codigo, r.stderr)
		}
		if !senhaGravadaConfere(t, pool, "dono@exemplo.com", nova) {
			t.Errorf("fim %q: a senha gravada não é a linha sem a quebra", fim)
		}
	}

	for _, entrada := range []string{"primeira-linha-longa\nsegunda-linha-longa", "senha-comprida-demais\r", "a\n\n"} {
		r := rodarUser(t, dsn, entrada, "set-password", "--email", "dono@exemplo.com")
		if r.codigo != saidaUso {
			t.Errorf("entrada %q: código %d, queria 2", entrada, r.codigo)
		}
	}
	// A última senha boa continua valendo: as recusas não mexeram em nada.
	senhas := 0
	for _, nome := range []string{"lf", "crlf", "nada"} {
		if senhaGravadaConfere(t, pool, "dono@exemplo.com", "outra-senha-comprida-"+nome) {
			senhas++
		}
	}
	if senhas != 1 {
		t.Error("uma entrada recusada mudou a senha")
	}
}

// Usuário único (spec §2.1): com qualquer usuário no banco, create sai com 3
// e não muda nada — nem cria outro, nem mexe na senha do existente.
func TestUserCreateRefusesWhenAUserExists(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("primeiro create: código %d; %s", r.codigo, r.stderr)
	}

	for _, email := range []string{"dono@exemplo.com", "outra@exemplo.com"} {
		r := rodarUser(t, dsn, "uma-senha-nova-bem-longa", "create", "--email", email)
		if r.codigo != saidaJaExiste {
			t.Errorf("%s: código %d, queria 3", email, r.codigo)
		}
	}
	if n := usuarios(t, pool); n != 1 {
		t.Errorf("%d usuários depois das recusas, queria 1", n)
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("a recusa trocou a senha do usuário existente")
	}
}

// A senha nunca sai: nem no sucesso, nem num erro de política, nem na
// confirmação que não confere.
func TestUserPasswordNeverLeaves(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	longa := senhaMarcada + strings.Repeat("x", auth.MaxPasswordLen)
	execucoes := map[string]execucao{
		"muito longa": rodarUser(t, dsn, longa, "create", "--email", "dono@exemplo.com"),
		"confirmação diferente": rodarUserCom(t, dsn, entradaDaSenha{terminal: true, lerSemEco: sequencia(senhaMarcada, senhaMarcada+"!")},
			nil, "create", "--email", "dono@exemplo.com"),
		"sucesso": rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"),
	}
	for nome, r := range execucoes {
		if strings.Contains(r.saida(), "marca-d-agua") {
			t.Errorf("%s: a senha apareceu na saída: %q", nome, r.saida())
		}
	}
	if execucoes["muito longa"].codigo != saidaUso || execucoes["confirmação diferente"].codigo != saidaUso {
		t.Error("as recusas não saíram com 2")
	}
	if n := usuarios(t, pool); n != 1 {
		t.Errorf("%d usuários, queria 1 (só o do sucesso)", n)
	}
}

// sequencia devolve cada senha numa chamada, como o terminal faria.
func sequencia(senhas ...string) func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i >= len(senhas) {
			return nil, errors.New("leitura a mais")
		}
		i++
		return []byte(senhas[i-1]), nil
	}
}

func TestUserTerminalAsksTwice(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	r := rodarUserCom(t, dsn, entradaDaSenha{terminal: true, lerSemEco: sequencia(senhaMarcada, senhaMarcada)},
		nil, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("código %d; %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stderr, "Confirme a senha") {
		t.Error("o terminal não pediu a confirmação")
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", senhaMarcada) {
		t.Error("a senha digitada não foi a gravada")
	}
}

// Não existe flag nem variável de ambiente de senha (D6 do Plano 5). Com o
// stdin vazio e uma senha válida no ambiente, o resultado tem de ser "muito
// curta" — a variável não foi lida.
func TestUserHasNoPasswordFlagOrEnv(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)

	if r := rodarUser(t, dsn, "", "create", "--email", "dono@exemplo.com", "--password", senhaMarcada); r.codigo != saidaUso {
		t.Errorf("--password: código %d, queria 2", r.codigo)
	}
	if r := rodarUser(t, dsn, "", "create", "--email", "dono@exemplo.com", senhaMarcada); r.codigo != saidaUso {
		t.Errorf("senha posicional: código %d, queria 2", r.codigo)
	}
	env := map[string]string{"VIGILUME_PASSWORD": senhaMarcada, "PASSWORD": senhaMarcada, "SENHA": senhaMarcada}
	r := rodarUserCom(t, dsn, entradaDaSenha{pipe: strings.NewReader("")}, env, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaUso || !strings.Contains(r.stderr, "muito curta") {
		t.Errorf("senha no ambiente: código %d, stderr %q; queria 2 e \"muito curta\"", r.codigo, r.stderr)
	}
	if n := usuarios(t, pool); n != 0 {
		t.Errorf("%d usuários criados sem senha no stdin", n)
	}
}

func TestUserRejectsBadInvocations(t *testing.T) {
	testdb.New(t)
	dsn := testdb.DSN(t)
	casos := map[string][]string{
		"sem subcomando":       {},
		"subcomando estranho":  {"delete", "--email", "dono@exemplo.com"},
		"sem e-mail":           {"create"},
		"e-mail sem arroba":    {"create", "--email", "dono"},
		"com nome de exibição": {"create", "--email", "Dono <dono@exemplo.com>"},
	}
	for nome, args := range casos {
		if r := rodarUser(t, dsn, senhaMarcada, args...); r.codigo != saidaUso {
			t.Errorf("%s: código %d, queria 2", nome, r.codigo)
		}
	}
	if r := rodarUser(t, "", senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaUso {
		t.Errorf("sem DATABASE_URL: código %d, queria 2", r.codigo)
	}
}

// O CLI recusa o superusuário ANTES de migrar, como o boot. Só o código não
// basta, porque ele sai igual nas duas ordens: o que trava a ordem é o banco
// continuar vazio.
func TestUserRefusesSuperuserBeforeMigrating(t *testing.T) {
	vazio, err := url.Parse(testdb.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := url.Parse(testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	vazio.User = admin.User

	r := rodarUser(t, vazio.String(), senhaMarcada, "create", "--email", "dono@exemplo.com")
	if r.codigo != saidaAmbiente {
		t.Errorf("superusuário: código %d, queria 1; stderr %q", r.codigo, r.stderr)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, vazio.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var tabela *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('users')::text`).Scan(&tabela); err != nil {
		t.Fatal(err)
	}
	if tabela != nil {
		t.Error("o CLI migrou como superusuário antes de recusar: as tabelas nasceram com o dono errado")
	}
}

// set-password é o caminho de recuperação: senha nova, bloqueio desfeito, TODAS
// as sessões do usuário encerradas, e só as dele. O segundo fator fica.
func TestSetPasswordRevokesEverySessionAndClearsTheLock(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if r := rodarUser(t, dsn, senhaMarcada, "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Fatalf("create: código %d; %s", r.codigo, r.stderr)
	}
	var dono uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users`).Scan(&dono); err != nil {
		t.Fatal(err)
	}
	// Outro usuário, inserido por fora (o CLI só cria o primeiro).
	outro := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'outro@exemplo.com', 'x')`, outro); err != nil {
		t.Fatal(err)
	}

	mgr := auth.NewManager(auth.NewPGStore(pool), clock.Real{}, []byte("chave-de-teste-com-32-bytes-ok!!"))
	meta := auth.Meta{IP: "203.0.113.9", UserAgent: "teste"}
	for range 2 {
		if _, _, err := mgr.Issue(ctx, dono, meta); err != nil {
			t.Fatal(err)
		}
	}
	doOutro, _, err := mgr.Issue(ctx, outro, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET failed_attempts = 5, locked_until = now() + interval '1 hour', totp_confirmed_at = now() WHERE id = $1`,
		dono); err != nil {
		t.Fatal(err)
	}

	r := rodarUser(t, dsn, "a-senha-nova-do-dono", "set-password", "--email", "dono@exemplo.com")
	if r.codigo != saidaOK {
		t.Fatalf("set-password: código %d; %s", r.codigo, r.stderr)
	}
	if !strings.Contains(r.stdout, "2 sessões encerradas") {
		t.Errorf("stdout = %q, queria a contagem de sessões", r.stdout)
	}

	var vivas, falhas int
	var travada, totp bool
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, dono).Scan(&vivas); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT failed_attempts, locked_until IS NOT NULL, totp_confirmed_at IS NOT NULL FROM users WHERE id = $1`,
		dono).Scan(&falhas, &travada, &totp); err != nil {
		t.Fatal(err)
	}
	if vivas != 0 {
		t.Errorf("%d sessões do dono continuaram vivas", vivas)
	}
	if falhas != 0 || travada {
		t.Errorf("o bloqueio ficou: %d falhas, travada = %v", falhas, travada)
	}
	if !totp {
		t.Error("set-password desligou o segundo fator")
	}
	if !senhaGravadaConfere(t, pool, "dono@exemplo.com", "a-senha-nova-do-dono") {
		t.Error("a senha nova não foi gravada")
	}
	if n := auditorias(t, pool, "user.password_reset"); n != 1 {
		t.Errorf("%d registros user.password_reset, queria 1", n)
	}
	// A sessão do outro usuário continua viva: prova direta, pelo refresh.
	if _, _, err := mgr.Rotate(ctx, doOutro, meta); err != nil {
		t.Errorf("a sessão de outro usuário caiu junto: %v", err)
	}
}

func TestSetPasswordUnknownEmail(t *testing.T) {
	pool := testdb.New(t)
	dsn := testdb.DSN(t)
	r := rodarUser(t, dsn, senhaMarcada, "set-password", "--email", "ninguem@exemplo.com")
	if r.codigo != saidaNaoCadastrado {
		t.Errorf("código %d, queria 4", r.codigo)
	}
	if n := usuarios(t, pool); n != 0 {
		t.Errorf("%d usuários criados por um set-password", n)
	}
}

func TestUserPolicyBoundaries(t *testing.T) {
	testdb.New(t)
	dsn := testdb.DSN(t)
	if r := rodarUser(t, dsn, strings.Repeat("a", auth.MinPasswordLen-1), "create", "--email", "dono@exemplo.com"); r.codigo != saidaUso {
		t.Errorf("14 caracteres: código %d, queria 2", r.codigo)
	}
	if r := rodarUser(t, dsn, strings.Repeat("é", auth.MinPasswordLen), "create", "--email", "dono@exemplo.com"); r.codigo != saidaOK {
		t.Errorf("15 caracteres de dois bytes: código %d, queria 0; %s", r.codigo, r.stderr)
	}
}
