package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
)

// Códigos de saída de `stillon user`. O 3 não muda de
// sentido: um passo idempotente (o seed do compose) o trata como sucesso.
const (
	saidaOK            = 0
	saidaAmbiente      = 1 // banco, papel com rolsuper/rolbypassrls, migration
	saidaUso           = 2 // flag, e-mail, DATABASE_URL, senha fora da política
	saidaJaExiste      = 3 // create: já existe usuário; nada mudou
	saidaNaoCadastrado = 4 // set-password, reset-2fa: e-mail não cadastrado; nada mudou
	// saidaInterrompida é Ctrl-C ou SIGTERM, em qualquer fase: 128 + SIGINT,
	// o que o shell mostra para um programa interrompido por Ctrl-C. Separado
	// do 1 para um script distinguir "o operador desistiu" de "o ambiente está
	// quebrado". Na leitura da senha nada foi escrito ainda; mais adiante, a
	// transação interrompida volta atrás.
	saidaInterrompida = 130
)

// maxLeitura limita o que se lê de um pipe. A política corta em 128 code
// points (512 bytes no pior caso); o teto só impede ler um arquivo enorme por
// engano.
const maxLeitura = 4096

// entradaDaSenha é de onde a senha vem. Senha NUNCA vem de argumento nem de
// variável de ambiente: argumento aparece em `ps` e no
// histórico do shell; variável, em /proc/<pid>/environ e no `docker inspect`.
type entradaDaSenha struct {
	// terminal: pede a senha duas vezes, sem eco, por lerSemEco.
	terminal  bool
	lerSemEco func() ([]byte, error)
	// salvarTerminal guarda o estado do terminal (com eco) antes da leitura e
	// devolve a função que o restaura. Nil nos testes.
	salvarTerminal func() (restaurar func(), err error)
	// pipe: uma linha só, lida daqui.
	pipe io.Reader
	// avisarPipe: o stdin não é terminal, mas o stderr é — uma pessoa digitando
	// num `docker exec -i` sem -t, ou num `docker compose exec -T`. Sem o aviso, o
	// comando parece travado: espera a linha em silêncio, e com eco.
	avisarPipe bool
}

// avisoDoPipe é a linha que avisarPipe escreve no stderr.
const avisoDoPipe = "stillon user: lendo a senha do stdin (uma linha; termine com Ctrl-D). Para digitar sem eco, use um terminal (-t)."

// stdinDoProcesso decide entre terminal e pipe olhando o stdin de verdade.
func stdinDoProcesso() entradaDaSenha {
	fd := int(os.Stdin.Fd()) // #nosec G115 -- descritor de arquivo do stdin (0); cabe em int em toda plataforma suportada.
	if term.IsTerminal(fd) {
		return entradaDaSenha{
			terminal:  true,
			lerSemEco: func() ([]byte, error) { return term.ReadPassword(fd) },
			salvarTerminal: func() (func(), error) {
				estado, err := term.GetState(fd)
				if err != nil {
					return nil, err
				}
				return func() { _ = term.Restore(fd, estado) }, nil
			},
		}
	}
	stderr := int(os.Stderr.Fd()) // #nosec G115 -- descritor de arquivo do stderr (2); cabe em int em toda plataforma suportada.
	return entradaDaSenha{pipe: os.Stdin, avisarPipe: term.IsTerminal(stderr)}
}

// errInterrompida: chegou Ctrl-C ou SIGTERM durante a leitura da senha.
var errInterrompida = errors.New("interrompido")

// lerOuDesistir devolve o que ler ler, ou errInterrompida se ctx acabar antes.
//
// Nem term.ReadPassword nem a leitura do stdin olham o contexto, e o
// signal.NotifyContext do main tira do SIGINT o efeito padrão (sair): sem isto,
// Ctrl-C durante a senha era engolido, e só um SIGKILL tirava o processo dali.
// A goroutine que lê fica bloqueada até o processo sair, logo em seguida.
func lerOuDesistir(ctx context.Context, ler func() ([]byte, error)) ([]byte, error) {
	type lido struct {
		b   []byte
		err error
	}
	fim := make(chan lido, 1)
	go func() {
		b, err := ler()
		fim <- lido{b, err}
	}()
	select {
	case r := <-fim:
		return r.b, r.err
	case <-ctx.Done():
		return nil, errInterrompida
	}
}

const usoUser = `uso:
  stillon user create --email <e-mail>        cria o dono (só se não houver usuário)
  stillon user set-password --email <e-mail>  redefine a senha e encerra todas as sessões
  stillon user reset-2fa --email <e-mail>     desliga o 2FA e encerra todas as sessões

A senha (create, set-password) vem do stdin: num terminal, é pedida duas
vezes, sem eco; num pipe, é a única linha da entrada. O reset-2fa não pede
senha. Só DATABASE_URL é lida do ambiente.`

// cliUser carrega o que todo subcomando de `stillon user` usa: o contexto, a
// origem da senha e os dois fluxos de saída.
type cliUser struct {
	ctx    context.Context
	in     entradaDaSenha
	stdout io.Writer
	stderr io.Writer
}

// falhar escreve a mensagem e devolve o código de saída.
func (c cliUser) falhar(codigo int, formato string, a ...any) int {
	// Com o contexto encerrado (Ctrl-C, SIGTERM), a falha é a interrupção,
	// em qualquer fase: o pgx devolve o erro do contexto, e sair com 1
	// diria "ambiente quebrado" a quem só desistiu.
	if c.ctx.Err() != nil {
		fmt.Fprintln(c.stderr, "stillon user: interrompido")
		return saidaInterrompida
	}
	fmt.Fprintf(c.stderr, "stillon user: "+formato+"\n", a...)
	return codigo
}

// falharNaoCadastrado é a recusa de set-password e reset-2fa para um e-mail sem
// usuário. O e-mail sai na mensagem para quem rodou contra o banco errado
// perceber.
func (c cliUser) falharNaoCadastrado(email string) int {
	return c.falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", email)
}

// falharDoServico classifica o erro de uma escrita de senha: política da senha
// é erro de uso (2); o resto, de ambiente (1).
func (c cliUser) falharDoServico(err error) int {
	if msg, ok := foraDaPolitica(err); ok {
		return c.falhar(saidaUso, "%s", msg)
	}
	return c.falhar(saidaAmbiente, "%v", err)
}

// runUser é `stillon user ...`. Devolve o código de saída; quem chama sai do
// processo com ele.
func runUser(ctx context.Context, args []string, in entradaDaSenha, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || (args[0] != "create" && args[0] != "set-password" && args[0] != "reset-2fa") {
		fmt.Fprintln(stderr, usoUser)
		return saidaUso
	}
	sub := args[0]
	c := cliUser{ctx: ctx, in: in, stdout: stdout, stderr: stderr}
	e, dsn, codigo := c.lerArgumentos(sub, args[1:], getenv)
	if codigo != saidaOK {
		return codigo
	}
	svc, fechar, err := abrirServico(ctx, dsn)
	if err != nil {
		return c.falhar(saidaAmbiente, "%v", err)
	}
	defer fechar()

	switch sub {
	case "create":
		return c.runCreate(svc, e)
	case "set-password":
		return c.runSetPassword(svc, e)
	case "reset-2fa":
		return c.runReset2FA(svc, e)
	default:
		// Inalcançável: o subcomando foi validado no começo. Um subcomando
		// novo que esqueça este switch falha em vez de cair no reset-2fa.
		return c.falhar(saidaAmbiente, "subcomando sem despacho: %q", sub)
	}
}

// lerArgumentos valida flag, e-mail e DATABASE_URL, nessa ordem. Com codigo
// diferente de saidaOK, a mensagem já saiu, codigo é o código de saída e email
// e dsn vêm vazios.
func (c cliUser) lerArgumentos(sub string, args []string, getenv func(string) string) (email, dsn string, codigo int) {
	fs := flag.NewFlagSet("stillon user "+sub, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	emailFlag := fs.String("email", "", "e-mail do usuário")
	if err := fs.Parse(args); err != nil {
		return "", "", saidaUso
	}
	if fs.NArg() > 0 {
		if sub == "reset-2fa" {
			return "", "", c.falhar(saidaUso, "argumento inesperado: %q", fs.Arg(0))
		}
		return "", "", c.falhar(saidaUso, "argumento inesperado: %q (a senha vem do stdin)", fs.Arg(0))
	}
	email, err := account.ValidateEmail(*emailFlag)
	if err != nil {
		return "", "", c.falhar(saidaUso, "--email precisa ser um endereço, como dono@exemplo.com")
	}
	dsn = strings.TrimSpace(getenv("DATABASE_URL"))
	if dsn == "" {
		return "", "", c.falhar(saidaUso, "DATABASE_URL não definida")
	}
	return email, dsn, saidaOK
}

// abrirServico abre o pool e monta o serviço de contas; fechar libera o pool.
func abrirServico(ctx context.Context, dsn string) (svc *account.Service, fechar func(), err error) {
	// A mesma ordem do boot: o papel antes da migration. Num
	// banco virgem, migrar como superusuário criaria as tabelas com o dono
	// errado, e o papel comum depois não conseguiria usá-las.
	pool, err := storage.NewPoolNamed(ctx, dsn, "stillon-user")
	if err != nil {
		return nil, nil, err
	}
	if err := storage.CheckRole(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := storage.Migrate(ctx, dsn); err != nil {
		pool.Close()
		return nil, nil, err
	}
	// Sem segundo fator e sem chave de IP: o CLI não troca senha pela API.
	return account.NewService(pool, nil, clock.Real{}, nil), pool.Close, nil
}

// runReset2FA desliga o 2FA. Não lê senha: quem tem a DATABASE_URL já pode
// tudo no banco.
func (c cliUser) runReset2FA(svc *account.Service, e string) int {
	ligado, n, err := svc.ResetTwoFactor(c.ctx, e)
	if errors.Is(err, account.ErrUserNotFound) {
		return c.falharNaoCadastrado(e)
	}
	if err != nil {
		return c.falhar(saidaAmbiente, "%v", err)
	}
	if !ligado {
		fmt.Fprintf(c.stdout, "o 2FA já estava desativado para %s; nada mudou\n", e)
		return saidaOK
	}
	fmt.Fprintf(c.stdout, "2FA desativado para %s; %s. Novos acessos dessas sessões estão bloqueados; ações já em andamento podem ser concluídas. Entre com a senha e ative de novo pelo painel.\n", e, sessoesEncerradas(n))
	return saidaOK
}

// runCreate cria o dono. A pré-condição vem ANTES da senha: ninguém digita
// duas vezes à toa.
func (c cliUser) runCreate(svc *account.Service, e string) int {
	existe, err := svc.HasUsers(c.ctx)
	if err != nil {
		return c.falhar(saidaAmbiente, "%v", err)
	}
	if existe {
		return c.falhar(saidaJaExiste, "já existe um usuário; nada foi alterado (use `stillon user set-password` para trocar a senha)")
	}
	senha, err := lerSenha(c.ctx, c.in, c.stderr)
	if err != nil {
		return c.falhar(saidaUso, "%v", err)
	}
	// A política da senha é a do serviço, e só dele: uma segunda checagem aqui
	// poderia divergir, e o PolicyError sairia como erro de ambiente (1).
	id, err := svc.CreateUser(c.ctx, e, senha)
	if errors.Is(err, account.ErrUserExists) {
		return c.falhar(saidaJaExiste, "já existe um usuário; nada foi alterado")
	}
	if err != nil {
		return c.falharDoServico(err)
	}
	fmt.Fprintf(c.stdout, "usuário criado: %s (id %s)\n", e, id)
	return saidaOK
}

// runSetPassword redefine a senha. A pré-condição (o e-mail existe) vem ANTES
// da senha, como no create.
func (c cliUser) runSetPassword(svc *account.Service, e string) int {
	if _, err := svc.UserIDByEmail(c.ctx, e); errors.Is(err, account.ErrUserNotFound) {
		return c.falharNaoCadastrado(e)
	} else if err != nil {
		return c.falhar(saidaAmbiente, "%v", err)
	}
	senha, err := lerSenha(c.ctx, c.in, c.stderr)
	if err != nil {
		return c.falhar(saidaUso, "%v", err)
	}
	n, err := svc.ResetPassword(c.ctx, e, senha)
	if errors.Is(err, account.ErrUserNotFound) {
		return c.falharNaoCadastrado(e)
	}
	if err != nil {
		return c.falharDoServico(err)
	}
	fmt.Fprintf(c.stdout, "senha redefinida; %s. Novos acessos dessas sessões estão bloqueados; ações já em andamento podem ser concluídas.\n", sessoesEncerradas(n))
	return saidaOK
}

// foraDaPolitica devolve a mensagem do PolicyError do serviço, que é erro de
// uso (2), e nunca de ambiente. A mensagem diz o limite, nunca a senha.
func foraDaPolitica(err error) (string, bool) {
	var pe account.PolicyError
	if !errors.As(err, &pe) {
		return "", false
	}
	switch pe.Reason {
	case auth.ReasonTooShort:
		return fmt.Sprintf("senha muito curta (mínimo de %d caracteres)", auth.MinPasswordLen), true
	case auth.ReasonTooLong:
		return fmt.Sprintf("senha muito longa (máximo de %d caracteres)", auth.MaxPasswordLen), true
	}
	return "senha fora da política", true
}

// lerSenha nunca escreve a senha em lugar nenhum; o erro diz o que houve, não
// o que foi lido.
//
// A senha vive como string depois disto: Go não tem como zerar uma string, e
// este código não finge que tem.
func lerSenha(ctx context.Context, in entradaDaSenha, stderr io.Writer) (string, error) {
	if in.terminal {
		// O term.ReadPassword desliga o eco e só o religa quando a leitura
		// termina; interrompida, ela não termina. O estado de antes é o que
		// devolve o eco ao terminal do operador.
		restaurar := func() {}
		if in.salvarTerminal != nil {
			r, err := in.salvarTerminal()
			if err != nil {
				return "", fmt.Errorf("ler o estado do terminal: %w", err)
			}
			restaurar = r
		}
		ler := func(rotulo string) ([]byte, error) {
			fmt.Fprint(stderr, rotulo)
			b, err := lerOuDesistir(ctx, in.lerSemEco)
			if errors.Is(err, errInterrompida) {
				restaurar()
			}
			fmt.Fprintln(stderr) // o terminal não ecoa o Enter
			return b, err
		}
		a, err := ler("Senha: ")
		if err != nil {
			return "", fmt.Errorf("ler a senha: %w", err)
		}
		b, err := ler("Confirme a senha: ")
		if err != nil {
			return "", fmt.Errorf("ler a confirmação: %w", err)
		}
		if !bytes.Equal(a, b) {
			return "", errors.New("as senhas não conferem")
		}
		return validarTexto(a)
	}

	if in.avisarPipe {
		fmt.Fprintln(stderr, avisoDoPipe)
	}
	b, err := lerOuDesistir(ctx, func() ([]byte, error) {
		return io.ReadAll(io.LimitReader(in.pipe, maxLeitura+1))
	})
	if err != nil {
		return "", fmt.Errorf("ler a senha do stdin: %w", err)
	}
	if len(b) > maxLeitura {
		return "", fmt.Errorf("a entrada tem mais de %d bytes", maxLeitura)
	}
	// Um \n (ou \r\n) final é o que `echo` e um arquivo de texto põem; sai um,
	// e só um. Sobrando quebra de linha, a entrada tem mais de uma linha, e
	// pegar só a primeira criaria um usuário com uma senha que ninguém sabe.
	if bytes.HasSuffix(b, []byte("\r\n")) {
		b = b[:len(b)-2]
	} else if bytes.HasSuffix(b, []byte("\n")) {
		b = b[:len(b)-1]
	}
	if bytes.ContainsAny(b, "\r\n") {
		return "", errors.New("a senha tem mais de uma linha")
	}
	return validarTexto(b)
}

func validarTexto(b []byte) (string, error) {
	if !utf8.Valid(b) {
		return "", errors.New("a senha não é UTF-8 válido")
	}
	return string(b), nil
}

// sessoesEncerradas concorda o número: "1 sessão encerrada", e "N sessões
// encerradas" para os outros (0 inclusive).
func sessoesEncerradas(n int64) string {
	if n == 1 {
		return "1 sessão encerrada"
	}
	return fmt.Sprintf("%d sessões encerradas", n)
}
