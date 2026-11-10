package notify

import "time"

// A política de lembretes aprovada pelo dono em 2026-10-05
// (docs/superpowers/specs/2026-10-14-lembretes-design.md).
const (
	// reminderLongBandAge é a idade do incidente a partir da qual os
	// lembretes ficam espaçados.
	reminderLongBandAge = 6 * time.Hour

	discordShortInterval = 30 * time.Minute
	discordLongInterval  = 6 * time.Hour
	emailShortInterval   = 2 * time.Hour
	emailLongInterval    = 24 * time.Hour
)

// cadence é o intervalo entre lembretes de um tipo de canal, antes e depois
// de o incidente completar a idade da faixa longa.
type cadence struct {
	short, long time.Duration
}

// Policy decide quando sai o próximo lembrete de um incidente aberto. É um
// valor, sem relógio nem banco: a regra de negócio fica num lugar só.
type Policy struct {
	longBandAge time.Duration
	// cadences tem uma chave por tipo de canal conhecido; valor nil é um tipo
	// que não recebe lembrete.
	cadences map[Kind]*cadence
}

// DefaultPolicy é a tabela aprovada: Discord a cada 30 min e e-mail a cada
// 2 h nas primeiras 6 h; depois, 6 h e 24 h.
func DefaultPolicy() Policy {
	return Policy{
		longBandAge: reminderLongBandAge,
		cadences: map[Kind]*cadence{
			KindDiscord: {short: discordShortInterval, long: discordLongInterval},
			KindEmail:   {short: emailShortInterval, long: emailLongInterval},
		},
	}
}

// NextReminder devolve quando vence o lembrete seguinte ao aviso de base (a
// abertura ou o último lembrete do par incidente-canal), enfileirado em
// baseAt. A faixa vem da idade do incidente na base, então a troca para o
// intervalo longo acontece no lembrete que cai depois das 6 h. ok é false
// quando o tipo não tem lembrete: tipo conhecido sem cadência ou tipo que esta
// versão não conhece (gravado por uma versão mais nova antes de um rollback).
// Nenhum dos dois é erro: quem chama pula o par e segue com os outros.
func (p Policy) NextReminder(kind Kind, startedAt, baseAt time.Time) (vence time.Time, ok bool) {
	c := p.cadences[kind]
	if c == nil {
		return time.Time{}, false
	}
	if baseAt.Sub(startedAt) < p.longBandAge {
		return baseAt.Add(c.short), true
	}
	return baseAt.Add(c.long), true
}

// knows diz se o tipo de canal é desta versão, com ou sem lembrete.
func (p Policy) knows(kind Kind) bool {
	_, ok := p.cadences[kind]
	return ok
}
