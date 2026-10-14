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
			got, err := DefaultPolicy().NextReminder(c.kind, inicioDoIncidente, base)
			if err != nil {
				t.Fatal(err)
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
		prox, err := DefaultPolicy().NextReminder(KindEmail, inicioDoIncidente, base)
		if err != nil {
			t.Fatal(err)
		}
		if got := prox.Sub(inicioDoIncidente); got != q {
			t.Fatalf("lembrete %d em %s; queria %s", i+1, got, q)
		}
		base = prox
	}
}

func TestNextReminderUnknownKindFails(t *testing.T) {
	if _, err := DefaultPolicy().NextReminder(Kind("sms"), inicioDoIncidente, inicioDoIncidente); err == nil {
		t.Error("kind desconhecido deveria falhar")
	}
}
