package notify

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// A retomada promete um lembrete, o vencido, sem prazo para voltar
// (docs/superpowers/specs/2026-10-14-lembretes-design.md, "Quem recebe
// lembrete"). A base dele são as linhas da outbox, que a retenção apaga aos 30
// dias quando já foram entregues. Os dois testes são os lados da fronteira: no
// limite a linha fica, um segundo depois ela sai.
func TestResumeAtTheRetentionLimitGetsTheOverdueReminder(t *testing.T) {
	exigirLembreteAoRetomar(t, 30*24*time.Hour, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
}

func TestResumeBeyondTheRetentionLimitGetsTheOverdueReminder(t *testing.T) {
	exigirLembreteAoRetomar(t, 30*24*time.Hour+time.Second, time.Date(2026, 10, 24, 12, 0, 1, 0, time.UTC))
}

func exigirLembreteAoRetomar(t *testing.T, pausa time.Duration, retomadaEm time.Time) {
	t.Helper()
	casos := []struct {
		nome   string
		pausar func(t *testing.T, b *bancada, pausado bool)
	}{
		{"canal pausado", pausarCanalDaBancada},
		{"alvo pausado", func(t *testing.T, b *bancada, pausado bool) {
			t.Helper()
			pausarAlvoDoIncidente(t, b.pool, b.dono, b.incidente, pausado)
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			ctx := context.Background()
			r := NewReminders(b.pool, b.clk, DefaultPolicy())
			entregarAbertura(t, b)

			c.pausar(t, b, true)
			b.clk.Advance(pausa)
			if n, err := r.Tick(ctx); err != nil || n != 0 {
				t.Fatalf("Tick com a pausa valendo = %d, %v; queria 0", n, err)
			}
			if _, err := storage.ApplyRetention(ctx, b.pool, b.clk.Now()); err != nil {
				t.Fatal(err)
			}
			c.pausar(t, b, false)

			if n, err := r.Tick(ctx); err != nil || n != 1 {
				t.Fatalf("Tick ao retomar = %d, %v; queria 1, o lembrete vencido", n, err)
			}
			b.despachar(t, 1)
			if b.canal.quantos() != 2 {
				t.Fatalf("envios = %+v; queria a abertura e um lembrete", b.canal.envios)
			}
			if l := b.canal.envios[1]; l.Kind != "reminder" || !l.QueuedAt.Equal(retomadaEm) {
				t.Errorf("segundo envio = %s, enfileirado em %s; queria reminder em %s", l.Kind, l.QueuedAt, retomadaEm)
			}
		})
	}
}

// entregarAbertura leva o incidente da bancada ao estado de quem já foi avisado:
// a linha de abertura sai pelo dispatcher e fica com status sent.
func entregarAbertura(t *testing.T, b *bancada) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: b.incidente, Now: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	aberta := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if l := b.estado(t, aberta); l.Status != "sent" {
		t.Fatalf("abertura: %+v, queria sent", l)
	}
}

func pausarCanalDaBancada(t *testing.T, b *bancada, pausado bool) {
	t.Helper()
	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.SetChannelEnabled(ctx, db.SetChannelEnabledParams{UserID: b.dono, ID: b.canalID, Enabled: !pausado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A retomada e o tique seguinte não são a mesma transação: entre os dois, até
// 30 s, a manutenção pode rodar e já não ver pausa nenhuma. A base precisa
// sobreviver a essa janela também.
func TestRetentionBetweenTheResumeAndTheNextTickKeepsTheOverdueReminder(t *testing.T) {
	b := novaBancada(t, nil)
	ctx := context.Background()
	r := NewReminders(b.pool, b.clk, DefaultPolicy())
	entregarAbertura(t, b)

	pausarCanalDaBancada(t, b, true)
	b.clk.Advance(31 * 24 * time.Hour)
	pausarCanalDaBancada(t, b, false)
	aplicarRetencao(t, b)

	if n, err := r.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("Tick depois da retomada = %d, %v; queria 1, o lembrete vencido", n, err)
	}
}

// Os 30 dias em número, de propósito: comparar com a constante deixaria passar
// uma troca dela. O incidente fechado tira a isenção da base do caminho, e o
// que sobra é só a idade.
func TestRetentionDeletesANotificationOnlyAfterThirtyDays(t *testing.T) {
	b := novaBancada(t, nil)
	entregarAbertura(t, b)
	b.resolver(t)

	b.clk.Advance(30 * 24 * time.Hour)
	aplicarRetencao(t, b)
	if l := linhasDaOutbox(t, b); !slices.Equal(l, []string{"opened/0"}) {
		t.Fatalf("com 30 dias exatos sobrou %v; queria a abertura", l)
	}

	b.clk.Advance(time.Second)
	aplicarRetencao(t, b)
	if l := linhasDaOutbox(t, b); len(l) != 0 {
		t.Errorf("um segundo depois dos 30 dias sobrou %v; queria nada", l)
	}
}

// Sem lembrete possível não há base a guardar, mesmo com o canal pausado: é
// isso que impede a tabela de crescer sem limite.
func TestRetentionDeletesTheLastRowOfAnIncidentThatCanNoLongerRemind(t *testing.T) {
	casos := []struct {
		nome    string
		encerra func(b *bancada, t *testing.T)
	}{
		{"incidente resolvido", (*bancada).resolver},
		{"incidente reconhecido", (*bancada).reconhecer},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			entregarAbertura(t, b)
			pausarCanalDaBancada(t, b, true)
			c.encerra(b, t)

			b.clk.Advance(31 * 24 * time.Hour)
			aplicarRetencao(t, b)

			if l := linhasDaOutbox(t, b); len(l) != 0 {
				t.Errorf("sobrou %v; queria nada", l)
			}
		})
	}
}

// Só a linha mais nova é base. As anteriores saem pela idade, e o seq do
// próximo lembrete continua de onde parou: um seq repetido cairia no ON
// CONFLICT DO NOTHING e o lembrete sumiria em silêncio.
func TestRetentionKeepsOnlyTheNewestRowOfAPausedPair(t *testing.T) {
	b := novaBancada(t, nil)
	r := NewReminders(b.pool, b.clk, DefaultPolicy())
	entregarAbertura(t, b)
	for seq := int32(1); seq <= 2; seq++ {
		b.clk.Advance(time.Hour)
		b.lembrar(t, seq)
		b.despachar(t, 1)
	}

	pausarCanalDaBancada(t, b, true)
	b.clk.Advance(31 * 24 * time.Hour)
	aplicarRetencao(t, b)
	if l := linhasDaOutbox(t, b); !slices.Equal(l, []string{"reminder/2"}) {
		t.Fatalf("depois da retenção sobrou %v; queria só o lembrete 2", l)
	}

	pausarCanalDaBancada(t, b, false)
	if n, err := r.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick ao retomar = %d, %v; queria 1", n, err)
	}
	if l := linhasDaOutbox(t, b); !slices.Equal(l, []string{"reminder/2", "reminder/3"}) {
		t.Errorf("depois do tique: %v; queria os lembretes 2 e 3", l)
	}
}

// A base é por par (incidente, canal): a linha mais nova de outro canal ou de
// outro incidente não toma o lugar dela.
func TestRetentionKeepsTheBaseOfEachPair(t *testing.T) {
	casos := []struct {
		nome  string
		mudar func(t *testing.T, b *bancada)
	}{
		{"outro canal", func(t *testing.T, b *bancada) { b.canalID = novoCanalDaBancada(t, b) }},
		{"outro incidente", func(t *testing.T, b *bancada) { b.incidente = novoIncidenteDaBancada(t, b) }},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b := novaBancada(t, nil)
			entregarAbertura(t, b)
			pausarCanalDaBancada(t, b, true)

			b.clk.Advance(time.Hour)
			c.mudar(t, b)
			b.enfileirar(t, incident.EventOpened)

			b.clk.Advance(31 * 24 * time.Hour)
			aplicarRetencao(t, b)

			if l := linhasDaOutbox(t, b); !slices.Equal(l, []string{"opened/0", "opened/0"}) {
				t.Errorf("sobrou %v; queria a abertura de cada par", l)
			}
		})
	}
}

func aplicarRetencao(t *testing.T, b *bancada) {
	t.Helper()
	if _, err := storage.ApplyRetention(context.Background(), b.pool, b.clk.Now()); err != nil {
		t.Fatal(err)
	}
}

// linhasDaOutbox devolve evento/seq de cada notificação, da mais velha para a
// mais nova.
func linhasDaOutbox(t *testing.T, b *bancada) []string {
	t.Helper()
	ctx := context.Background()
	var linhas []string
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT event || '/' || seq FROM notifications ORDER BY created_at, seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		linhas = append(linhas, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return linhas
}

func novoCanalDaBancada(t *testing.T, b *bancada) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	agora, id := b.clk.Now(), uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: id, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func novoIncidenteDaBancada(t *testing.T, b *bancada) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	agora, alvo, id := b.clk.Now(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: b.dono, Name: "Outra", URL: "https://exemplo.com/outra", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: "timeout"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
