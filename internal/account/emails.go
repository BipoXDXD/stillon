package account

import (
	"context"
	"strings"
)

// AccountMailer é a porta dos e-mails de conta (confirmação, "já tem conta",
// redefinição). SendAccountEmail enfileira e volta; não espera a rede. Se a
// latência do envio aparecesse só num dos ramos (conta existente ou não), o
// tempo de resposta denunciaria a conta.
type AccountMailer interface {
	SendAccountEmail(ctx context.Context, to, subject, body string) error
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
