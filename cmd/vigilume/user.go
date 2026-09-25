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

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
)

// Códigos de saída de `vigilume user` (spec do Plano 5, §4.1). O 3 não muda de
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
// variável de ambiente (D6 do Plano 5): argumento aparece em `ps` e no
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
	// num `docker exec -i` sem -t, ou num `fly ssh console -C`. Sem o aviso, o
	// comando parece travado: espera a linha em silêncio, e com eco.
	avisarPipe bool
}

// avisoDoPipe é a linha que avisarPipe escreve no stderr.
const avisoDoPipe = "vigilume user: lendo a senha do stdin (uma linha; termine com Ctrl-D). Para digitar sem eco, use um terminal (-t)."

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
  vigilume user create --email <e-mail>        cria o dono (só se não houver usuário)
  vigilume user set-password --email <e-mail>  redefine a senha e encerra todas as sessões
  vigilume user reset-2fa --email <e-mail>     desliga o 2FA e encerra todas as sessões

A senha (create, set-password) vem do stdin: num terminal, é pedida duas
vezes, sem eco; num pipe, é a única linha da entrada. O reset-2fa não pede
senha. Só DATABASE_URL é lida do ambiente.`

// runUser é `vigilume user ...`. Devolve o código de saída; quem chama sai do
// processo com ele.
func runUser(ctx context.Context, args []string, in entradaDaSenha, stdout, stderr io.Writer, getenv func(string) string) int {
	falhar := func(codigo int, formato string, a ...any) int {
		// Com o contexto encerrado (Ctrl-C, SIGTERM), a falha é a interrupção,
		// em qualquer fase: o pgx devolve o erro do contexto, e sair com 1
		// diria "ambiente quebrado" a quem só desistiu.
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "vigilume user: interrompido")
			return saidaInterrompida
		}
		fmt.Fprintf(stderr, "vigilume user: "+formato+"\n", a...)
		return codigo
	}
	if len(args) == 0 || (args[0] != "create" && args[0] != "set-password" && args[0] != "reset-2fa") {
		fmt.Fprintln(stderr, usoUser)
		return saidaUso
	}
	sub := args[0]
	fs := flag.NewFlagSet("vigilume user "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "e-mail do usuário")
	if err := fs.Parse(args[1:]); err != nil {
		return saidaUso
	}
	if fs.NArg() > 0 {
		if sub == "reset-2fa" {
			return falhar(saidaUso, "argumento inesperado: %q", fs.Arg(0))
		}
		return falhar(saidaUso, "argumento inesperado: %q (a senha vem do stdin)", fs.Arg(0))
	}
	e, err := account.ValidateEmail(*email)
	if err != nil {
		return falhar(saidaUso, "--email precisa ser um endereço, como dono@exemplo.com")
	}
	dsn := strings.TrimSpace(getenv("DATABASE_URL"))
	if dsn == "" {
		return falhar(saidaUso, "DATABASE_URL não definida")
	}

	// A mesma ordem do boot (D7 do Plano 5): o papel antes da migration. Num
	// banco virgem, migrar como superusuário criaria as tabelas com o dono
	// errado, e o papel comum depois não conseguiria usá-las.
	pool, err := storage.NewPool(ctx, dsn)
	if err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	defer pool.Close()
	if err := storage.CheckRole(ctx, pool); err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	if err := storage.Migrate(ctx, dsn); err != nil {
		return falhar(saidaAmbiente, "%v", err)
	}
	// Sem segundo fator e sem chave de IP: o CLI não troca senha pela API.
	svc := account.NewService(pool, nil, clock.Real{}, nil)

	// reset-2fa não lê senha (spec do Plano 9, §3.1): quem tem a DATABASE_URL
	// já pode tudo no banco. O e-mail sai na mensagem para quem rodou contra o
	// banco errado perceber (R5 da spec).
	if sub == "reset-2fa" {
		ligado, n, err := svc.ResetTwoFactor(ctx, e)
		if errors.Is(err, account.ErrUserNotFound) {
			return falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", e)
		}
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		if !ligado {
			fmt.Fprintf(stdout, "o 2FA já estava desativado para %s; nada mudou\n", e)
			return saidaOK
		}
		fmt.Fprintf(stdout, "2FA desativado para %s; %s. Acessos já abertos expiram em até 15 minutos. Entre com a senha e ative de novo pelo painel.\n", e, sessoesEncerradas(n))
		return saidaOK
	}

	// A pré-condição vem ANTES da senha: ninguém digita duas vezes à toa.
	switch sub {
	case "create":
		existe, err := svc.HasUsers(ctx)
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		if existe {
			return falhar(saidaJaExiste, "já existe um usuário; nada foi alterado (use `vigilume user set-password` para trocar a senha)")
		}
	case "set-password":
		if _, err := svc.UserIDByEmail(ctx, e); errors.Is(err, account.ErrUserNotFound) {
			return falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", e)
		} else if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
	}

	senha, err := lerSenha(ctx, in, stderr)
	if err != nil {
		return falhar(saidaUso, "%v", err)
	}

	// A política da senha é a do serviço, e só dele: uma segunda checagem aqui
	// poderia divergir, e o PolicyError sairia como erro de ambiente (1).
	switch sub {
	case "create":
		id, err := svc.CreateUser(ctx, e, senha)
		if errors.Is(err, account.ErrUserExists) {
			return falhar(saidaJaExiste, "já existe um usuário; nada foi alterado")
		}
		if msg, ok := foraDaPolitica(err); ok {
			return falhar(saidaUso, "%s", msg)
		}
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		fmt.Fprintf(stdout, "usuário criado: %s (id %s)\n", e, id)
	case "set-password":
		n, err := svc.ResetPassword(ctx, e, senha)
		if errors.Is(err, account.ErrUserNotFound) {
			return falhar(saidaNaoCadastrado, "nenhum usuário com o e-mail %s; nada foi alterado", e)
		}
		if msg, ok := foraDaPolitica(err); ok {
			return falhar(saidaUso, "%s", msg)
		}
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		fmt.Fprintf(stdout, "senha redefinida; %s. Acessos já abertos expiram em até 15 minutos.\n", sessoesEncerradas(n))
	}
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
