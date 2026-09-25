package notify

import (
	"fmt"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// causas traduz o error_kind para quem lê o alerta. É o enum, e nunca a
// mensagem crua do erro, que não é persistida (spec §4.2).
var causas = map[string]string{
	"timeout":      "tempo esgotado",
	"dns":          "falha de DNS",
	"tls":          "erro de TLS",
	"conn_refused": "conexão recusada",
	"http_status":  "status HTTP diferente do esperado",
	"body_limit":   "corpo da resposta acima do limite",
	"blocked":      "destino bloqueado pela proteção de rede",
	"unknown":      "erro de rede",
}

const formatoData = "02/01/2006 15:04 UTC"

// mensagem monta assunto e corpo do alerta. Só usa campos de ev, que vêm do
// banco: a mesma notificação produz sempre o mesmo texto.
func mensagem(ev Event) (assunto, corpo string) {
	if ev.Kind == incident.EventResolved {
		return fmt.Sprintf("[Vigilume] %s voltou", ev.TargetName),
			fmt.Sprintf("%s voltou em %s, depois de %s fora do ar.\n",
				ev.TargetName, ev.ResolvedAt.UTC().Format(formatoData), duracao(ev.ResolvedAt.Sub(ev.StartedAt)))
	}
	causa, ok := causas[ev.Cause]
	if !ok {
		causa = causas["unknown"]
	}
	return fmt.Sprintf("[Vigilume] %s está fora do ar", ev.TargetName),
		fmt.Sprintf("%s está fora do ar desde %s.\nCausa: %s.\n",
			ev.TargetName, ev.StartedAt.UTC().Format(formatoData), causa)
}

// duracao escreve d em minutos, horas ou dias, arredondando para baixo.
func duracao(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de um minuto"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %02dmin", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}
