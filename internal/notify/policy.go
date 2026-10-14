package notify

import (
	"fmt"
	"time"
)

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
	cadences    map[Kind]cadence
}

// DefaultPolicy é a tabela aprovada: Discord a cada 30 min e e-mail a cada
// 2 h nas primeiras 6 h; depois, 6 h e 24 h.
func DefaultPolicy() Policy {
	return Policy{
		longBandAge: reminderLongBandAge,
		cadences: map[Kind]cadence{
			KindDiscord: {short: discordShortInterval, long: discordLongInterval},
			KindEmail:   {short: emailShortInterval, long: emailLongInterval},
		},
	}
}

// NextReminder devolve quando vence o lembrete seguinte ao aviso de base (a
// abertura ou o último lembrete do par incidente-canal), enfileirado em
// baseAt. A faixa vem da idade do incidente na base, então a troca para o
// intervalo longo acontece no lembrete que cai depois das 6 h. Um kind sem
// cadência é erro: o CHECK de notif_channels.kind não deixaria isso chegar
// aqui, e quem chama não deve inventar um intervalo.
func (p Policy) NextReminder(kind Kind, startedAt, baseAt time.Time) (time.Time, error) {
	c, ok := p.cadences[kind]
	if !ok {
		return time.Time{}, fmt.Errorf("política de lembretes: canal de tipo %q sem cadência", kind)
	}
	if baseAt.Sub(startedAt) < p.longBandAge {
		return baseAt.Add(c.short), nil
	}
	return baseAt.Add(c.long), nil
}
