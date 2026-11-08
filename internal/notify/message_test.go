package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
)

func TestMessageForOpenedAndResolved(t *testing.T) {
	assunto, corpo := mensagem(evAberto)
	if assunto != "[StillOn] API de pagamentos está fora do ar" ||
		corpo != "API de pagamentos está fora do ar desde 24/09/2026 12:00 UTC.\nCausa: tempo esgotado.\n" {
		t.Errorf("abertura: %q / %q", assunto, corpo)
	}

	fechou := evAberto
	fechou.Kind = incident.EventResolved
	fechou.ResolvedAt = fechou.StartedAt.Add(2*time.Hour + 5*time.Minute + 40*time.Second)
	assunto, corpo = mensagem(fechou)
	if assunto != "[StillOn] API de pagamentos voltou" ||
		corpo != "API de pagamentos voltou em 24/09/2026 14:05 UTC, depois de 2h 05min fora do ar.\n" {
		t.Errorf("fechamento: %q / %q", assunto, corpo)
	}
}

// Tarefa não fica "fora do ar": os três avisos falam em sinal, com o motivo.
func TestMessageForHeartbeat(t *testing.T) {
	ev := evAberto
	ev.TargetName = "Backup"
	ev.Cause = "heartbeat_late"
	assunto, corpo := mensagem(ev)
	if assunto != "[StillOn] Backup está sem sinal" ||
		corpo != "Backup está sem sinal desde 24/09/2026 12:00 UTC.\nCausa: o sinal não chegou no prazo.\n" {
		t.Errorf("abertura: %q / %q", assunto, corpo)
	}

	ev.Kind = incident.EventReminder
	ev.QueuedAt = ev.StartedAt.Add(45 * time.Minute)
	assunto, corpo = mensagem(ev)
	if assunto != "[StillOn] Backup continua sem sinal" || !strings.Contains(corpo, "(há 45 min)") {
		t.Errorf("lembrete: %q / %q", assunto, corpo)
	}

	ev.Kind = incident.EventResolved
	ev.ResolvedAt = ev.StartedAt.Add(2 * time.Hour)
	assunto, corpo = mensagem(ev)
	if assunto != "[StillOn] Backup voltou a dar sinal" ||
		corpo != "Backup voltou a dar sinal em 24/09/2026 14:00 UTC, depois de 2h 00min sem sinal.\n" {
		t.Errorf("fechamento: %q / %q", assunto, corpo)
	}
}

// Falha avisada não é "sem sinal": a tarefa deu sinal, e ele foi ruim.
func TestMessageForAnnouncedTaskFailure(t *testing.T) {
	ev := evAberto
	ev.TargetName = "Backup"
	ev.Cause = "heartbeat_failed"
	assunto, corpo := mensagem(ev)
	if assunto != "[StillOn] Backup está em falha" ||
		corpo != "Backup está em falha desde 24/09/2026 12:00 UTC.\nCausa: a tarefa avisou que falhou.\n" {
		t.Errorf("abertura: %q / %q", assunto, corpo)
	}

	ev.Kind = incident.EventReminder
	ev.QueuedAt = ev.StartedAt.Add(45 * time.Minute)
	assunto, corpo = mensagem(ev)
	if assunto != "[StillOn] Backup continua em falha" ||
		corpo != "Backup continua em falha desde 24/09/2026 12:00 UTC (há 45 min).\nCausa: a tarefa avisou que falhou.\n"+
			"Reconheça o incidente para parar os lembretes.\n" {
		t.Errorf("lembrete: %q / %q", assunto, corpo)
	}

	ev.Kind = incident.EventResolved
	ev.ResolvedAt = ev.StartedAt.Add(2 * time.Hour)
	assunto, corpo = mensagem(ev)
	if assunto != "[StillOn] Backup voltou a funcionar" ||
		corpo != "Backup voltou a funcionar em 24/09/2026 14:00 UTC, depois de 2h 00min em falha.\n" {
		t.Errorf("fechamento: %q / %q", assunto, corpo)
	}
}

// Retomar sem sucesso encerra o incidente, mas a tarefa não voltou: o aviso não
// pode dizer que voltou, seja qual for a causa.
func TestMessageForResumeWithoutSuccess(t *testing.T) {
	for _, c := range []string{"heartbeat_late", "heartbeat_failed"} {
		ev := evAberto
		ev.TargetName = "Backup"
		ev.Cause = c
		ev.Kind = incident.EventResolved
		ev.ClosedByResume = true
		ev.ResolvedAt = ev.StartedAt.Add(2 * time.Hour)
		assunto, corpo := mensagem(ev)
		if assunto != "[StillOn] Backup foi retomado" ||
			corpo != "Backup foi retomado em 24/09/2026 14:00 UTC, sem sinal de sucesso. O incidente foi encerrado, e o prazo recomeça a contar.\n" {
			t.Errorf("%s: %q / %q", c, assunto, corpo)
		}
	}
}

// Causa fora do enum não vaza para a mensagem: vira o residual.
func TestMessageNeverEchoesAnUnknownCause(t *testing.T) {
	ev := evAberto
	ev.Cause = "dial tcp 10.0.3.42:5432: connect: connection refused"
	if _, corpo := mensagem(ev); strings.Contains(corpo, "10.0.3.42") || !strings.Contains(corpo, "erro de rede") {
		t.Errorf("corpo = %q", corpo)
	}
}

func TestDuration(t *testing.T) {
	for d, quer := range map[time.Duration]string{
		30 * time.Second:               "menos de um minuto",
		7*time.Minute + 59*time.Second: "7 min",
		time.Hour:                      "1h 00min",
		26*time.Hour + 30*time.Minute:  "1d 2h",
	} {
		if got := duracao(d); got != quer {
			t.Errorf("duracao(%v) = %q, queria %q", d, got, quer)
		}
	}
}

func TestConfigIsBoundToTheRow(t *testing.T) {
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	id, outro := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(box, id, "https://discord.com/api/webhooks/1/tok")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "tok") {
		t.Error("config_enc contém o destino em claro")
	}
	if dest, err := openConfig(box, id, sealed); err != nil || string(dest.Reveal()) != "https://discord.com/api/webhooks/1/tok" {
		t.Errorf("abrir na própria linha: %v", err)
	}
	if _, err := openConfig(box, outro, sealed); err == nil {
		t.Error("o ciphertext abriu em outra linha: o AAD não amarra o id")
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, quer := range map[string]string{
		"pessoa@Exemplo.COM":                "pessoa@exemplo.com",
		"  pessoa@exemplo.com ":             "pessoa@exemplo.com",
		"Pessoa@exemplo.com":                "Pessoa@exemplo.com", // a parte local pode diferenciar caixa
		"Fulano <p@x.com>":                  "",
		"p@x.com (comentário)":              "",
		"p@localhost":                       "",
		"sem-arroba":                        "",
		"a@b.com, c@d.com":                  "",
		"":                                  "",
		strings.Repeat("a", 250) + "@x.com": "",
		// A fronteira de maxEmail (254): on point aceito, off point recusado.
		strings.Repeat("a", 248) + "@x.com": strings.Repeat("a", 248) + "@x.com",
		strings.Repeat("a", 249) + "@x.com": "",
	} {
		got, ok := NormalizeEmail(in)
		if (quer == "") == ok || got != quer {
			t.Errorf("NormalizeEmail(%q) = %q, %v; queria %q", in, got, ok, quer)
		}
	}
}

func TestHintNeverShowsTheSecret(t *testing.T) {
	if h := hint(KindDiscord, crypto.Secret("https://discord.com/api/webhooks/123456789012/token-secreto")); h != "webhook …9012" {
		t.Errorf("hint do Discord = %q", h)
	}
	if h := hint(KindEmail, crypto.Secret("pessoa@exemplo.com")); h != "p***@exemplo.com" {
		t.Errorf("hint do e-mail = %q", h)
	}
}

// O lembrete conta a duração até o enfileiramento, e não até o envio: a
// retentativa repete o texto (Idempotency-Key do Resend).
func TestMessageForReminder(t *testing.T) {
	ev := evAberto
	ev.Kind = incident.EventReminder
	ev.QueuedAt = ev.StartedAt.Add(6*time.Hour + 30*time.Minute + 15*time.Second)
	assunto, corpo := mensagem(ev)
	if assunto != "[StillOn] API de pagamentos continua fora do ar" ||
		corpo != "API de pagamentos continua fora do ar desde 24/09/2026 12:00 UTC (há 6h 30min).\n"+
			"Causa: tempo esgotado.\nReconheça o incidente no painel para parar os lembretes.\n" {
		t.Errorf("lembrete: %q / %q", assunto, corpo)
	}
}
