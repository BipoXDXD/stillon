package notify

import (
	"testing"
	"time"
)

var inicioDoIncidente = time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)

// A tabela aprovada pelo dono em 2026-10-05: Discord a cada 30 min e e-mail a
// cada 2 h enquanto a idade na base é menor que 6 h; depois, 6 h e 24 h.
func TestNextReminderIntervalByKindAndAge(t *testing.T) {
	casos := []struct {
		nome  string
		kind  Kind
		idade time.Duration // idade do incidente no aviso que serve de base
		quer  time.Duration // do aviso de base até o próximo lembrete
	}{
		{"discord na abertura", KindDiscord, 0, 30 * time.Minute},
		{"discord logo abaixo de 6 h", KindDiscord, 6*time.Hour - time.Second, 30 * time.Minute},
		{"discord em exatamente 6 h", KindDiscord, 6 * time.Hour, 6 * time.Hour},
		{"discord acima de 6 h", KindDiscord, 30 * time.Hour, 6 * time.Hour},
		{"e-mail na abertura", KindEmail, 0, 2 * time.Hour},
		{"e-mail logo abaixo de 6 h", KindEmail, 6*time.Hour - time.Second, 2 * time.Hour},
		{"e-mail em exatamente 6 h", KindEmail, 6 * time.Hour, 24 * time.Hour},
		{"e-mail acima de 6 h", KindEmail, 30 * time.Hour, 24 * time.Hour},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			base := inicioDoIncidente.Add(c.idade)
			got, ok := DefaultPolicy().NextReminder(c.kind, inicioDoIncidente, base)
			if !ok {
				t.Fatalf("NextReminder(%q) sem lembrete; queria um", c.kind)
			}
			if want := base.Add(c.quer); !got.Equal(want) {
				t.Errorf("NextReminder = %s; queria %s", got, want)
			}
		})
	}
}

// O exemplo do design: abertura em 0 h e lembretes em 2 h, 4 h, 6 h e 30 h.
func TestNextReminderEmailSequence(t *testing.T) {
	quer := []time.Duration{2 * time.Hour, 4 * time.Hour, 6 * time.Hour, 30 * time.Hour}
	base := inicioDoIncidente
	for i, q := range quer {
		prox, ok := DefaultPolicy().NextReminder(KindEmail, inicioDoIncidente, base)
		if !ok {
			t.Fatalf("lembrete %d sem vencimento", i+1)
		}
		if got := prox.Sub(inicioDoIncidente); got != q {
			t.Fatalf("lembrete %d em %s; queria %s", i+1, got, q)
		}
		base = prox
	}
}

// Um tipo que esta versão não conhece (gravado por uma versão mais nova,
// antes de um rollback) não tem lembrete; não é erro.
func TestNextReminderTipoDesconhecidoNaoTemLembrete(t *testing.T) {
	if _, ok := DefaultPolicy().NextReminder(Kind("futuro"), inicioDoIncidente, inicioDoIncidente); ok {
		t.Error("tipo desconhecido não deveria ter lembrete")
	}
}

// Cadência nil é um tipo conhecido que não lembra (o PagerDuty, adiante).
func TestNextReminderCadenciaNilNaoTemLembrete(t *testing.T) {
	p := Policy{longBandAge: reminderLongBandAge, cadences: map[Kind]*cadence{"x": nil}}
	if _, ok := p.NextReminder(Kind("x"), inicioDoIncidente, inicioDoIncidente); ok {
		t.Error("tipo com cadência nil não deveria ter lembrete")
	}
}
