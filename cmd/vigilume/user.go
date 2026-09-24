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
	saidaNaoCadastrado = 4 // set-password: e-mail não cadastrado; nada mudou
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
	// pipe: uma linha só, lida daqui.
	pipe io.Reader
}

// stdinDoProcesso decide entre terminal e pipe olhando o stdin de verdade.
func stdinDoProcesso() entradaDaSenha {
	fd := int(os.Stdin.Fd()) // #nosec G115 -- descritor de arquivo do stdin (0); cabe em int em toda plataforma suportada.
	if term.IsTerminal(fd) {
		return entradaDaSenha{terminal: true, lerSemEco: func() ([]byte, error) { return term.ReadPassword(fd) }}
	}
	return entradaDaSenha{pipe: os.Stdin}
}

const usoUser = `uso:
  vigilume user create --email <e-mail>        cria o dono (só se não houver usuário)
  vigilume user set-password --email <e-mail>  redefine a senha e encerra todas as sessões

A senha vem do stdin: num terminal, é pedida duas vezes, sem eco; num pipe,
é a única linha da entrada. Só DATABASE_URL é lida do ambiente.`

// runUser é `vigilume user ...`. Devolve o código de saída; quem chama sai do
// processo com ele.
func runUser(ctx context.Context, args []string, in entradaDaSenha, stdout, stderr io.Writer, getenv func(string) string) int {
	falhar := func(codigo int, formato string, a ...any) int {
		fmt.Fprintf(stderr, "vigilume user: "+formato+"\n", a...)
		return codigo
	}
	if len(args) == 0 || (args[0] != "create" && args[0] != "set-password") {
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
	svc := account.NewService(pool, clock.Real{})

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

	senha, err := lerSenha(in, stderr)
	if err != nil {
		return falhar(saidaUso, "%v", err)
	}
	switch auth.ValidateNewPassword(senha) {
	case auth.ReasonTooShort:
		return falhar(saidaUso, "senha muito curta (mínimo de %d caracteres)", auth.MinPasswordLen)
	case auth.ReasonTooLong:
		return falhar(saidaUso, "senha muito longa (máximo de %d caracteres)", auth.MaxPasswordLen)
	}

	switch sub {
	case "create":
		id, err := svc.CreateUser(ctx, e, senha)
		if errors.Is(err, account.ErrUserExists) {
			return falhar(saidaJaExiste, "já existe um usuário; nada foi alterado")
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
		if err != nil {
			return falhar(saidaAmbiente, "%v", err)
		}
		fmt.Fprintf(stdout, "senha redefinida; %d sessões encerradas. Acessos já abertos expiram em até 15 minutos.\n", n)
	}
	return saidaOK
}

// lerSenha nunca escreve a senha em lugar nenhum; o erro diz o que houve, não
// o que foi lido.
//
// A senha vive como string depois disto: Go não tem como zerar uma string, e
// este código não finge que tem.
func lerSenha(in entradaDaSenha, stderr io.Writer) (string, error) {
	if in.terminal {
		fmt.Fprint(stderr, "Senha: ")
		a, err := in.lerSemEco()
		fmt.Fprintln(stderr) // o terminal não ecoa o Enter
		if err != nil {
			return "", fmt.Errorf("ler a senha: %w", err)
		}
		fmt.Fprint(stderr, "Confirme a senha: ")
		b, err := in.lerSemEco()
		fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("ler a confirmação: %w", err)
		}
		if !bytes.Equal(a, b) {
			return "", errors.New("as senhas não conferem")
		}
		return validarTexto(a)
	}

	b, err := io.ReadAll(io.LimitReader(in.pipe, maxLeitura+1))
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
