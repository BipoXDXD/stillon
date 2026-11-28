package statuspage

import "strings"

// slugReservado: os nomes da lista e todo slug que começa com "stillon" ou
// "still-on". A página pública mora em /p/{slug}, então a lista não evita
// colisão de rota, e sim confusão e phishing ("stillon.tech/p/reset-password").
// Vale só para criar e para trocar de slug: a página que já tem um destes
// fica com ele (o Update compara com o slug atual, relido sob a trava).
func slugReservado(slug string) bool {
	if strings.HasPrefix(slug, "stillon") || strings.HasPrefix(slug, "still-on") {
		return true
	}
	switch slug {
	case "admin", "administrador", "api", "app", "assets", "auth", "blog", "cdn",
		"conta", "contas", "account", "dashboard", "docs", "help", "ajuda",
		"login", "entrar", "cadastro", "signup", "logout", "mail", "email",
		"painel", "privacidade", "termos", "root", "static", "status",
		"suporte", "support", "www", "oficial", "official", "security",
		"seguranca", "billing", "pagamento", "healthchecks",
		// Rotas de conta do painel.
		"confirm-email", "forgot-password", "reset-password", "settings",
		"configuracoes", "projects", "projetos", "invite", "convite", "convites",
		"export", "exportar",
		// Marcas.
		"google", "microsoft", "apple", "amazon", "aws", "azure", "cloudflare",
		"github", "gitlab", "paypal", "stripe", "facebook", "meta", "instagram",
		"whatsapp", "netflix", "nubank", "itau", "bradesco", "santander",
		"caixa", "bancodobrasil", "pix", "mercadopago", "mercadolivre",
		"resend", "hostinger":
		return true
	}
	return false
}
