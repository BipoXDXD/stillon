package account

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode"
)

// ResultadoDoEnvio é a resposta do transporte ou o descarte antes da entrega.
type ResultadoDoEnvio uint8

const (
	EnvioIncerto ResultadoDoEnvio = iota
	AceitoPeloTransporte
	EnvioDescartado
)

// ConclusaoDoEnvio registra o resultado. Pode ser repetida se a gravação
// falhar depois de o trabalho já ter sido feito.
type ConclusaoDoEnvio func(context.Context, ResultadoDoEnvio) error

// EntregaDeEmail descreve o ciclo de um link. O trabalhador chama Preparar
// antes das cotas e da rede. false significa que o link já perdeu a validade.
// Mensagens sem link usam o valor zero.
type EntregaDeEmail struct {
	Preparar func(context.Context) (bool, error)
	Concluir ConclusaoDoEnvio
}

// AccountMailer enfileira e volta sem aguardar a rede. A conclusão pertence
// ao trabalhador, inclusive quando este pode limpar um descarte da fila.
type AccountMailer interface {
	SendAccountEmail(ctx context.Context, to, subject, body string, entrega EntregaDeEmail) error
}

// ErrEmailSuprimido indica que o mailer descartou a mensagem antes de enfileirar.
var ErrEmailSuprimido = errors.New("e-mail de conta suprimido")

// enviarEmailDeConta mantém a resposta independente da fila e do provedor.
// Um erro do mailer pode conter dados da mensagem, então o log é fixo.
func enviarEmailDeConta(ctx context.Context, mailer AccountMailer, to, assunto, corpo string, entrega EntregaDeEmail) {
	if err := mailer.SendAccountEmail(ctx, to, assunto, corpo, entrega); err != nil && !errors.Is(err, ErrEmailSuprimido) {
		slog.ErrorContext(ctx, "e-mail de conta não enfileirado")
	}
}

// Links monta os links dos e-mails de conta a partir da URL pública do painel.
// O token vai no fragmento (#token=), que o navegador não manda ao servidor:
// não aparece em log de acesso nem no Referer.
// Base aceita a barra no fim (a PUBLIC_BASE_URL pode trazê-la): o link
// nunca sai com "//".
type Links struct{ Base string }

func (l Links) base() string { return strings.TrimRight(l.Base, "/") }

// ConfirmarEmail é o link da confirmação de e-mail.
func (l Links) ConfirmarEmail(token string) string {
	return l.base() + "/confirm-email#token=" + token
}

// RedefinirSenha é o link da redefinição de senha.
func (l Links) RedefinirSenha(token string) string {
	return l.base() + "/reset-password#token=" + token
}

// AceitarConvite é o link do convite para um projeto.
func (l Links) AceitarConvite(token string) string {
	return l.base() + "/invite#token=" + token
}

// EsqueciASenha é a página que pede o link de redefinição.
func (l Links) EsqueciASenha() string {
	return l.base() + "/forgot-password"
}

// Os textos aprovados pelo dono (Step 1 da T11 do PR 3). Texto puro, com o
// link sozinho numa linha para o cliente de e-mail torná-lo clicável.

// emailDeConfirmacao é o e-mail do cadastro novo.
func emailDeConfirmacao(link string) (assunto, corpo string) {
	return "Confirme seu e-mail no StillOn",
		"Olá,\n\n" +
			"Recebemos um cadastro no StillOn com este e-mail. Para concluir, abra o link:\n\n" +
			link + "\n\n" +
			"O link vale por 24 horas e funciona uma vez.\n\n" +
			"Se não foi você, ignore esta mensagem. Sem a confirmação, nada fica ativo.\n\n" +
			"StillOn\n"
}

// emailJaTemConta é o e-mail de quem tenta se cadastrar com um e-mail que já
// tem conta: aponta para "esqueci a senha", sem dizer nada a quem cadastrou.
func emailJaTemConta(linkEsqueci string) (assunto, corpo string) {
	return "Você já tem conta no StillOn",
		"Olá,\n\n" +
			"Alguém tentou criar uma conta no StillOn com este e-mail, que já está cadastrado.\n\n" +
			"Se foi você e não lembra a senha, peça um link de redefinição:\n\n" +
			linkEsqueci + "\n\n" +
			"Se não foi você, ignore esta mensagem. Nada mudou na sua conta.\n\n" +
			"StillOn\n"
}

// emailDeRedefinicao é o e-mail do "esqueci a senha".
func emailDeRedefinicao(link string) (assunto, corpo string) {
	return "Redefina sua senha no StillOn",
		"Olá,\n\n" +
			"Recebemos um pedido para redefinir a senha da sua conta no StillOn. Para escolher uma nova, abra o link:\n\n" +
			link + "\n\n" +
			"O link vale por 1 hora e funciona uma vez. Ao salvar a senha nova, todas as sessões abertas são encerradas.\n\n" +
			"Se não foi você, ignore esta mensagem. Sua senha continua a mesma.\n\n" +
			"StillOn\n"
}

// rotulosDosPapeis são os nomes dos papéis no e-mail do convite. Rascunho:
// o texto final é do dono (T12 do PR 4).
var rotulosDosPapeis = map[string]string{
	"owner":   "dono",
	"manager": "gerente",
	"member":  "membro",
	"viewer":  "leitor",
}

// semControle troca quebras de linha e outros caracteres de controle por
// nada: um nome de projeto antigo (anterior a ParseProjectName) não quebra o
// assunto nem o corpo.
func semControle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// EmailDeConvite é o e-mail do convite para um projeto. O texto é o mesmo
// exista ou não conta com o endereço do destinatário: nada nele depende
// disso. Rascunho até a T12 do PR 4.
func EmailDeConvite(projeto, quemConvidou, papel, link string) (assunto, corpo string) {
	projeto, quemConvidou = semControle(projeto), semControle(quemConvidou)
	rotulo, ok := rotulosDosPapeis[papel]
	if !ok {
		rotulo = "integrante"
	}
	return "Convite para o projeto " + projeto + " no StillOn",
		"Olá,\n\n" +
			quemConvidou + " convidou você para o projeto " + projeto + " no StillOn, como " + rotulo + ". Para aceitar, abra o link:\n\n" +
			link + "\n\n" +
			"O convite vale por 7 dias e só pode ser usado uma vez. Para aceitar, entre no StillOn com este e-mail.\n\n" +
			"Se não esperava este convite, ignore esta mensagem.\n\n" +
			"StillOn\n"
}
