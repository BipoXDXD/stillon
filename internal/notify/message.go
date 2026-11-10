package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// causas traduz o error_kind para quem lê o alerta. É o enum, e nunca a
// mensagem crua do erro, que não é persistida (spec §4.2).
var causas = map[string]string{
	"timeout":          "tempo esgotado",
	"dns":              "falha de DNS",
	"tls":              "erro de TLS",
	"conn_refused":     "conexão recusada",
	"http_status":      "status HTTP diferente do esperado",
	"content_mismatch": "conteúdo da resposta diferente do esperado",
	"body_limit":       "resposta grande demais para conferir o conteúdo",
	"blocked":          "destino bloqueado pela proteção de rede",
	"unknown":          "erro de rede",
	// Tarefas: o sinal é dela, e o texto não fala em erro de rede.
	"heartbeat_late":   "o sinal não chegou no prazo",
	"heartbeat_failed": "a tarefa avisou que falhou",
}

const formatoData = "02/01/2006 15:04 UTC"

// mensagem monta assunto e corpo do alerta. Só usa campos de ev, que vêm do
// banco: a mesma notificação produz sempre o mesmo texto.
func mensagem(ev Event) (assunto, corpo string) {
	desde := ev.StartedAt.UTC().Format(formatoData)
	if strings.HasPrefix(ev.Cause, prefixoDeTarefa) {
		return mensagemDeTarefa(ev, desde)
	}
	switch ev.Kind {
	case incident.EventResolved:
		return fmt.Sprintf("[StillOn] %s voltou", ev.TargetName),
			fmt.Sprintf("%s voltou em %s, depois de %s fora do ar.\n",
				ev.TargetName, ev.ResolvedAt.UTC().Format(formatoData), duracao(ev.ResolvedAt.Sub(ev.StartedAt)))
	case incident.EventReminder:
		return fmt.Sprintf("[StillOn] %s continua fora do ar", ev.TargetName),
			fmt.Sprintf("%s continua fora do ar desde %s (há %s).\nCausa: %s.\n"+
				"Reconheça o incidente no painel para parar os lembretes.\n",
				ev.TargetName, desde, duracao(ev.QueuedAt.Sub(ev.StartedAt)), causa(ev.Cause))
	}
	return fmt.Sprintf("[StillOn] %s está fora do ar", ev.TargetName),
		fmt.Sprintf("%s está fora do ar desde %s.\nCausa: %s.\n", ev.TargetName, desde, causa(ev.Cause))
}

// prefixoDeTarefa marca as causas de heartbeat. "Fora do ar" descreve um
// serviço, não um backup que não rodou (D20 da spec de heartbeats).
const prefixoDeTarefa = "heartbeat_"

// causaFalhaDeTarefa é a falha avisada: a tarefa deu sinal, e o sinal foi ruim.
const causaFalhaDeTarefa = "heartbeat_failed"

func mensagemDeTarefa(ev Event, desde string) (assunto, corpo string) {
	situacao, volta := "sem sinal", "voltou a dar sinal"
	if ev.Cause == causaFalhaDeTarefa {
		situacao, volta = "em falha", "voltou a funcionar"
	}
	switch ev.Kind {
	case incident.EventResolved:
		if ev.ClosedByResume {
			return fmt.Sprintf("[StillOn] %s foi retomado", ev.TargetName),
				fmt.Sprintf("%s foi retomado em %s, sem sinal de sucesso. O incidente foi encerrado, e o prazo recomeça a contar.\n",
					ev.TargetName, ev.ResolvedAt.UTC().Format(formatoData))
		}
		return fmt.Sprintf("[StillOn] %s %s", ev.TargetName, volta),
			fmt.Sprintf("%s %s em %s, depois de %s %s.\n", ev.TargetName, volta,
				ev.ResolvedAt.UTC().Format(formatoData), duracao(ev.ResolvedAt.Sub(ev.StartedAt)), situacao)
	case incident.EventReminder:
		return fmt.Sprintf("[StillOn] %s continua %s", ev.TargetName, situacao),
			fmt.Sprintf("%s continua %s desde %s (há %s).\nCausa: %s.\n"+
				"Reconheça o incidente para parar os lembretes.\n",
				ev.TargetName, situacao, desde, duracao(ev.QueuedAt.Sub(ev.StartedAt)), causa(ev.Cause))
	}
	return fmt.Sprintf("[StillOn] %s está %s", ev.TargetName, situacao),
		fmt.Sprintf("%s está %s desde %s.\nCausa: %s.\n", ev.TargetName, situacao, desde, causa(ev.Cause))
}

// Text é o assunto e o corpo do aviso, como os canais os enviam.
func (ev Event) Text() (assunto, corpo string) { return mensagem(ev) }

// causa traduz o error_kind; o que estiver fora do enum vira o residual.
func causa(errorKind string) string {
	if c, ok := causas[errorKind]; ok {
		return c
	}
	return causas["unknown"]
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
