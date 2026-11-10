package heartbeat_test

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

var (
	agora = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	hora  = mustPeriod(time.Hour)
)

const tolerancia = 5 * time.Minute

func mustPeriod(d time.Duration) heartbeat.Schedule {
	s, err := heartbeat.NewPeriod(d)
	if err != nil {
		panic(err)
	}
	return s
}

func aplicar(s heartbeat.State, sig heartbeat.Signal, em time.Time) (heartbeat.State, *heartbeat.Event) {
	return s.Apply(sig, em, heartbeat.Run{}, hora, tolerancia)
}

func TestSuccessMovesTheDeadlineByPeriodPlusGrace(t *testing.T) {
	s, ev := aplicar(heartbeat.State{DeadlineAt: agora.Add(-time.Hour)}, heartbeat.SignalSuccess, agora)
	if want := time.Date(2026, 11, 5, 13, 5, 0, 0, time.UTC); !s.DeadlineAt.Equal(want) {
		t.Errorf("prazo %v, queria %v", s.DeadlineAt, want)
	}
	if !s.LastSuccessAt.Equal(agora) || !s.LastPingAt.Equal(agora) || s.LastSignal != heartbeat.SignalSuccess {
		t.Errorf("estado depois do sucesso: %+v", s)
	}
	if ev == nil || ev.Kind != heartbeat.SignalSuccess || !ev.At.Equal(agora) || ev.RuntimeMS != nil {
		t.Errorf("evento %+v, queria sucesso sem tempo de execução", ev)
	}
}

// D10: início e falha não mexem no prazo.
func TestStartAndFailDoNotMoveTheDeadline(t *testing.T) {
	prazo := agora.Add(time.Hour)
	for _, sig := range []heartbeat.Signal{heartbeat.SignalStart, heartbeat.SignalFail} {
		s, ev := aplicar(heartbeat.State{DeadlineAt: prazo}, sig, agora)
		if !s.DeadlineAt.Equal(prazo) {
			t.Errorf("%s moveu o prazo para %v", sig, s.DeadlineAt)
		}
		if ev == nil || ev.Kind != sig {
			t.Errorf("%s: evento %+v", sig, ev)
		}
	}
}

func TestFailKeepsTheFirstFailingInstantAndSuccessClearsIt(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalFail, agora)
	s, _ = aplicar(s, heartbeat.SignalFail, agora.Add(time.Minute))
	if !s.FailingSince.Equal(agora) {
		t.Errorf("failing_since %v, queria a primeira falha", s.FailingSince)
	}
	s, _ = aplicar(s, heartbeat.SignalSuccess, agora.Add(2*time.Minute))
	if !s.FailingSince.IsZero() {
		t.Errorf("sucesso deixou failing_since em %v", s.FailingSince)
	}
}

func TestRuntimeComesFromTheOpenStartAndConsumesIt(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalStart, agora)
	s, ev := aplicar(s, heartbeat.SignalSuccess, agora.Add(90*time.Second))
	if ev.RuntimeMS == nil || *ev.RuntimeMS != 90000 {
		t.Fatalf("tempo de execução %v, queria 90000", ev.RuntimeMS)
	}
	if !s.LastStartAt.IsZero() {
		t.Errorf("o início não foi consumido: %v", s.LastStartAt)
	}
	_, ev = aplicar(s, heartbeat.SignalSuccess, agora.Add(time.Hour))
	if ev.RuntimeMS != nil {
		t.Errorf("segundo sucesso herdou tempo de execução: %d", *ev.RuntimeMS)
	}
}

// O sucesso manual (o sinalizar agora do painel) move o prazo, mas não encerra
// a execução aberta: o início e a última duração ficam para a tarefa.
func TestManualSuccessKeepsTheOpenRun(t *testing.T) {
	anterior := int64(5000)
	s := heartbeat.State{LastStartAt: agora, LastRuntimeMS: &anterior}
	s, ev := s.Apply(heartbeat.SignalSuccess, agora.Add(time.Minute), heartbeat.Run{Manual: true}, hora, tolerancia)
	if ev.RuntimeMS != nil {
		t.Errorf("o sucesso manual mediu %d ms, queria nil", *ev.RuntimeMS)
	}
	if !s.LastStartAt.Equal(agora) {
		t.Errorf("last_start_at %v, queria %v", s.LastStartAt, agora)
	}
	if s.LastRuntimeMS == nil || *s.LastRuntimeMS != anterior {
		t.Errorf("last_runtime_ms %v, queria %d", s.LastRuntimeMS, anterior)
	}
	if !s.LastSuccessAt.Equal(agora.Add(time.Minute)) {
		t.Errorf("last_success_at %v, queria %v", s.LastSuccessAt, agora.Add(time.Minute))
	}
}

// A execução que falha também é medida, e o início dela não fica aberto para o
// próximo sucesso medir duas execuções como uma.
func TestFailClosesTheRunToo(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalStart, agora)
	s, ev := aplicar(s, heartbeat.SignalFail, agora.Add(30*time.Second))
	if ev.RuntimeMS == nil || *ev.RuntimeMS != 30000 {
		t.Fatalf("tempo de execução %v, queria 30000", ev.RuntimeMS)
	}
	if !s.LastStartAt.IsZero() {
		t.Errorf("o início não foi consumido: %v", s.LastStartAt)
	}
	if s.LastRuntimeMS == nil || *s.LastRuntimeMS != 30000 {
		t.Errorf("última duração %v, queria 30000", s.LastRuntimeMS)
	}
	_, ev = aplicar(s, heartbeat.SignalSuccess, agora.Add(time.Hour))
	if ev.RuntimeMS != nil {
		t.Errorf("sucesso sem início herdou %d ms", *ev.RuntimeMS)
	}
}

func TestLastRuntimeFollowsTheLastClosedRun(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalStart, agora)
	s, _ = aplicar(s, heartbeat.SignalSuccess, agora.Add(90*time.Second))
	if s.LastRuntimeMS == nil || *s.LastRuntimeMS != 90000 {
		t.Fatalf("última duração %v, queria 90000", s.LastRuntimeMS)
	}
	// O início sozinho não apaga a medida: a execução anterior segue a última
	// encerrada.
	s, _ = aplicar(s, heartbeat.SignalStart, agora.Add(time.Hour))
	if s.LastRuntimeMS == nil {
		t.Fatal("o início apagou a última duração")
	}
	s, _ = aplicar(s, heartbeat.SignalSuccess, agora.Add(time.Hour+5*time.Second))
	if s.LastRuntimeMS == nil || *s.LastRuntimeMS != 5000 {
		t.Fatalf("última duração %v, queria 5000", s.LastRuntimeMS)
	}
	s, _ = aplicar(s, heartbeat.SignalSuccess, agora.Add(2*time.Hour))
	if s.LastRuntimeMS != nil {
		t.Errorf("execução sem início ficou com %d ms", *s.LastRuntimeMS)
	}
}

// Relógio de outra instância atrás do início não vira duração negativa, que o
// CHECK de runtime_ms recusaria e perderia o ping.
func TestRuntimeIsNeverNegative(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalStart, agora)
	_, ev := aplicar(s, heartbeat.SignalSuccess, agora.Add(-time.Second))
	if ev.RuntimeMS == nil || *ev.RuntimeMS != 0 {
		t.Fatalf("tempo de execução %v, queria 0", ev.RuntimeMS)
	}
}

// D9, nas duas bordas da janela.
func TestRepeatWindowBoundaries(t *testing.T) {
	base, _ := aplicar(heartbeat.State{}, heartbeat.SignalSuccess, agora)
	casos := []struct {
		nome      string
		depois    time.Duration
		repeticao bool
	}{
		{"mesmo instante", 0, true},
		{"relógio atrás", -time.Second, true},
		{"logo antes da janela", heartbeat.RepeatWindow - time.Nanosecond, true},
		{"na janela", heartbeat.RepeatWindow, false},
		{"depois da janela", heartbeat.RepeatWindow + time.Second, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, ev := aplicar(base, heartbeat.SignalSuccess, agora.Add(c.depois))
			if (ev == nil) != c.repeticao {
				t.Fatalf("evento %+v, repetição esperada=%v", ev, c.repeticao)
			}
			if c.repeticao && s != base {
				t.Errorf("repetição mudou o estado: %+v", s)
			}
		})
	}
}

// Um sinal diferente nunca é repetição: tarefa de 1 s manda início e sucesso.
func TestDifferentSignalInsideTheWindowCounts(t *testing.T) {
	s, _ := aplicar(heartbeat.State{}, heartbeat.SignalStart, agora)
	if _, ev := aplicar(s, heartbeat.SignalSuccess, agora.Add(time.Second)); ev == nil {
		t.Fatal("sucesso logo depois do início foi tratado como repetição")
	}
}

func TestPeriodRange(t *testing.T) {
	casos := []struct {
		d  time.Duration
		ok bool
	}{
		{0, false}, {-time.Minute, false},
		{heartbeat.MinPeriod - time.Second, false}, {heartbeat.MinPeriod, true},
		{heartbeat.MaxPeriod, true}, {heartbeat.MaxPeriod + time.Second, false},
		{365 * 24 * time.Hour, true}, {365*24*time.Hour + time.Second, false},
	}
	for _, c := range casos {
		_, err := heartbeat.NewPeriod(c.d)
		if (err == nil) != c.ok {
			t.Errorf("período %v: %v, ok esperado=%v", c.d, err, c.ok)
		}
		var ve *heartbeat.ValidationError
		if err != nil && (!errors.As(err, &ve) || ve.Field != "period_sec") {
			t.Errorf("período %v: erro %v sem o campo", c.d, err)
		}
	}
}

func TestGraceRange(t *testing.T) {
	casos := []struct {
		d  time.Duration
		ok bool
	}{
		{0, false}, {heartbeat.MinGrace - time.Second, false}, {heartbeat.MinGrace, true},
		{heartbeat.MaxGrace, true}, {heartbeat.MaxGrace + time.Second, false},
		{365 * 24 * time.Hour, true}, {365*24*time.Hour + time.Second, false},
	}
	for _, c := range casos {
		if err := heartbeat.ValidateGrace(c.d); (err == nil) != c.ok {
			t.Errorf("tolerância %v: %v, ok esperado=%v", c.d, err, c.ok)
		}
	}
}

func TestNewCronRejections(t *testing.T) {
	casos := []struct {
		nome, expr, tz, campo string
	}{
		{"expressão vazia", "", "UTC", "expr"},
		{"quatro campos", "0 3 * *", "UTC", "expr"},
		{"seis campos", "0 0 3 * * *", "UTC", "expr"},
		{"descritor", "@daily", "UTC", "expr"},
		{"fuso escondido", "CRON_TZ=Asia/Tokyo 0 3 * * *", "UTC", "expr"},
		{"fuso escondido curto", "TZ=Asia/Tokyo 0 3 * * *", "UTC", "expr"},
		{"nunca dispara", "0 0 30 2 *", "UTC", "expr"},
		{"longa demais", "0 3 * * *" + string(make([]byte, heartbeat.MaxCronLength)), "UTC", "expr"},
		{"sem fuso", "0 3 * * *", "", "timezone"},
		{"fuso da máquina", "0 3 * * *", "Local", "timezone"},
		{"fuso inexistente", "0 3 * * *", "Marte/Olympus", "timezone"},
		{"fuso com travessia", "0 3 * * *", "../../etc/passwd", "timezone"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := heartbeat.NewCron(c.expr, c.tz)
			var ve *heartbeat.ValidationError
			if !errors.As(err, &ve) || ve.Field != c.campo {
				t.Fatalf("erro %v, queria validação em %s", err, c.campo)
			}
		})
	}
}

// O formulário promete exatamente isto (mensagens.ts, FormularioHeartbeat.tsx):
// o domingo é 0 ou SUN, e faixa que começa depois dele não pode terminar nele,
// nem com o nome. A faixa de um dia só, que começa no domingo, passa.
func TestCronSundaySpellings(t *testing.T) {
	casos := []struct {
		diaDaSemana string
		aceita      bool
	}{
		{"0", true},
		{"SUN", true},
		{"sun", true},
		{"MON-FRI", true},
		{"5,6,0", true},
		{"FRI,SAT,SUN", true},
		{"1-6,0", true},
		{"0-0", true},
		{"SUN-SUN", true},
		{"SUN-MON", true},
		{"7", false},
		{"5-7", false},
		{"1-7", false},
		{"0,7", false},
		{"5-0", false},
		{"FRI-SUN", false},
		{"MON-SUN", false},
	}
	for _, c := range casos {
		t.Run(c.diaDaSemana, func(t *testing.T) {
			_, err := heartbeat.NewCron("0 3 * * "+c.diaDaSemana, "UTC")
			var ve *heartbeat.ValidationError
			switch {
			case c.aceita && err != nil:
				t.Errorf("recusou: %v", err)
			case !c.aceita && (!errors.As(err, &ve) || ve.Field != "expr" || ve.Reason != heartbeat.ReasonInvalid):
				t.Errorf("erro %v, queria expr invalid", err)
			}
		})
	}
}

func cronEm(t *testing.T, expr, tz string) heartbeat.Schedule {
	t.Helper()
	s, err := heartbeat.NewCron(expr, tz)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// O mesmo "às 3h" é um instante diferente em cada fuso.
func TestCronUsesTheDeclaredTimezone(t *testing.T) {
	casos := []struct {
		tz   string
		want time.Time
	}{
		{"UTC", time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)},
		{"America/Sao_Paulo", time.Date(2026, 11, 6, 6, 0, 0, 0, time.UTC)},
		{"Asia/Tokyo", time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)},
	}
	for _, c := range casos {
		if got := cronEm(t, "0 3 * * *", c.tz).Next(agora); !got.Equal(c.want) {
			t.Errorf("%s: %v, queria %v", c.tz, got, c.want)
		}
	}
}

// 2h30 não existe em 8/3/2026 em Nova York: o disparo daquele dia não acontece,
// e o prazo vai para o dia seguinte. Quem agenda nessa hora ganha uma tolerância
// de um dia uma vez por ano; o contrário, um alarme falso.
func TestCronSkipsTheHourThatDoesNotExist(t *testing.T) {
	s := cronEm(t, "30 2 * * *", "America/New_York")
	got := s.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("%v, queria %v", got, want)
	}
}

// 1h30 acontece duas vezes em 1/11/2026 em Nova York, e a biblioteca dispara
// nas duas: 05:30 e 06:30 UTC. Quem roda uma vez só nessa hora pode ter um
// alarme falso por ano; é limite conhecido, na seção 5 da spec.
func TestCronAcrossTheRepeatedHourMovesForward(t *testing.T) {
	s := cronEm(t, "30 1 * * *", "America/New_York")
	primeiro := s.Next(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !primeiro.Equal(want) {
		t.Fatalf("primeiro %v, queria %v", primeiro, want)
	}
	if got, want := s.Next(primeiro), time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("próximo %v, queria %v", got, want)
	}
}

// 29 de fevereiro dispara de quatro em quatro anos, e é válido.
func TestCronOnLeapDayIsAccepted(t *testing.T) {
	if _, err := heartbeat.NewCron("0 0 29 2 *", "UTC"); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleFromRow(t *testing.T) {
	periodo, expr, fuso := int32(3600), "0 3 * * *", "UTC"
	s, err := heartbeat.ScheduleFromRow(&periodo, nil, nil)
	if err != nil || s.IsCron() || s.Period() != time.Hour {
		t.Fatalf("período: %+v, %v", s, err)
	}
	s, err = heartbeat.ScheduleFromRow(nil, &expr, &fuso)
	if err != nil || !s.IsCron() || s.CronExpr() != expr || s.Timezone() != fuso {
		t.Fatalf("cron: %+v, %v", s, err)
	}
	if _, err := heartbeat.ScheduleFromRow(nil, &expr, nil); err == nil {
		t.Fatal("cron sem fuso passou")
	}
}

// Piso de rollback: uma versão futura pode gravar período acima da faixa de
// cadastro, e esta versão precisa ler a linha mesmo assim.
func TestScheduleFromRowAcceptsPeriodAboveRegistrationRange(t *testing.T) {
	periodo := int32(31536000)
	s, err := heartbeat.ScheduleFromRow(&periodo, nil, nil)
	if err != nil {
		t.Fatalf("365 dias: %v", err)
	}
	if !s.IsKnown() || s.Period() != 365*24*time.Hour {
		t.Fatalf("365 dias: conhecido=%v período=%v", s.IsKnown(), s.Period())
	}
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if got, want := s.Next(t0), time.Date(2027, 11, 10, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Next %v, queria %v", got, want)
	}
}

func TestScheduleFromRowRejectsNonPositivePeriod(t *testing.T) {
	for _, p := range []int32{0, -60} {
		_, err := heartbeat.ScheduleFromRow(&p, nil, nil)
		var ve *heartbeat.ValidationError
		if !errors.As(err, &ve) || ve.Field != "period_sec" || ve.Reason != heartbeat.ReasonOutOfRange {
			t.Errorf("período %d: erro %v, queria period_sec/out_of_range", p, err)
		}
	}
}

// Período e cron nulos é a forma que esta versão não conhece: lê sem erro e
// marca como desconhecida, com ou sem fuso gravado.
func TestScheduleFromRowWithoutPeriodOrCronIsUnknown(t *testing.T) {
	fuso := "America/Sao_Paulo"
	for _, tz := range []*string{nil, &fuso} {
		s, err := heartbeat.ScheduleFromRow(nil, nil, tz)
		if err != nil {
			t.Fatalf("fuso %v: %v", tz, err)
		}
		if s.IsKnown() || s.IsCron() {
			t.Errorf("fuso %v: conhecido=%v cron=%v, queria desconhecido", tz, s.IsKnown(), s.IsCron())
		}
	}
}

func TestScheduleFromRowCronWithoutTimezoneStillFails(t *testing.T) {
	expr := "0 3 * * *"
	if _, err := heartbeat.ScheduleFromRow(nil, &expr, nil); err == nil {
		t.Fatal("cron sem fuso passou")
	}
}

// A leitura ficou sem teto, mas o cadastro não: a faixa continua no NewPeriod,
// e um segundo acima dos 365 dias é recusado.
func TestNewPeriodStillEnforcesRegistrationRange(t *testing.T) {
	_, err := heartbeat.NewPeriod(365*24*time.Hour + time.Second)
	var ve *heartbeat.ValidationError
	if !errors.As(err, &ve) || ve.Field != "period_sec" || ve.Reason != heartbeat.ReasonOutOfRange {
		t.Fatalf("erro %v, queria period_sec/out_of_range", err)
	}
}

// Next num agendamento desconhecido é bug de quem chama: não há próximo
// instante a inventar, e um prazo errado gravado seria pior que a queda.
func TestNextOnUnknownSchedulePanics(t *testing.T) {
	s, err := heartbeat.ScheduleFromRow(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("Next em agendamento desconhecido não entrou em pânico")
		}
	}()
	s.Next(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
}

// Piso de rollback: numa linha que esta versão não sabe agendar, o sinal fica
// registrado como em qualquer outra, mas o prazo gravado não se move.
func TestApplyOnUnknownScheduleKeepsDeadline(t *testing.T) {
	desconhecido, err := heartbeat.ScheduleFromRow(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prazo := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	em := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	inicioAnterior := time.Date(2026, 11, 10, 9, 59, 0, 0, time.UTC)
	falhandoDesde := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	casos := []struct {
		nome          string
		antes         heartbeat.State
		sig           heartbeat.Signal
		sucesso       time.Time
		falhandoDesde time.Time
	}{
		{nome: "início", antes: heartbeat.State{DeadlineAt: prazo}, sig: heartbeat.SignalStart},
		{nome: "falha", antes: heartbeat.State{DeadlineAt: prazo}, sig: heartbeat.SignalFail, falhandoDesde: em},
		{
			nome: "sucesso limpa a falha", sig: heartbeat.SignalSuccess, sucesso: em,
			antes: heartbeat.State{DeadlineAt: prazo, FailingSince: falhandoDesde},
		},
		{
			nome: "sucesso depois de início", sig: heartbeat.SignalSuccess, sucesso: em,
			antes: heartbeat.State{
				DeadlineAt: prazo, LastStartAt: inicioAnterior, LastPingAt: inicioAnterior,
				LastSignal: heartbeat.SignalStart,
			},
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, ev := c.antes.Apply(c.sig, em, heartbeat.Run{}, desconhecido, tolerancia)
			if !s.DeadlineAt.Equal(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)) {
				t.Errorf("prazo %v, queria 2026-11-10T12:00:00Z", s.DeadlineAt)
			}
			if !s.LastPingAt.Equal(em) || s.LastSignal != c.sig {
				t.Errorf("último ping %v/%s, queria %v/%s", s.LastPingAt, s.LastSignal, em, c.sig)
			}
			if !s.LastSuccessAt.Equal(c.sucesso) {
				t.Errorf("último sucesso %v, queria %v", s.LastSuccessAt, c.sucesso)
			}
			if !s.FailingSince.Equal(c.falhandoDesde) {
				t.Errorf("falhando desde %v, queria %v", s.FailingSince, c.falhandoDesde)
			}
			if ev == nil || ev.Kind != c.sig || !ev.At.Equal(em) {
				t.Errorf("evento %+v, queria %s em %v", ev, c.sig, em)
			}
		})
	}
	// O log não mexe em nada nem com o agendamento desconhecido: só o evento.
	t.Run("log", func(t *testing.T) {
		antes := heartbeat.State{
			DeadlineAt:   time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
			LastPingAt:   time.Date(2026, 11, 10, 9, 59, 0, 0, time.UTC),
			LastSignal:   heartbeat.SignalStart,
			FailingSince: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
			LastStartAt:  time.Date(2026, 11, 10, 9, 59, 0, 0, time.UTC),
		}
		s, ev := antes.Apply(heartbeat.SignalLog, em, heartbeat.Run{}, desconhecido, tolerancia)
		if s != antes {
			t.Errorf("log mudou o estado: %+v", s)
		}
		if ev == nil || ev.Kind != heartbeat.SignalLog || !ev.At.Equal(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("evento %+v, queria log em 2026-11-10T10:00:00Z", ev)
		}
	})
}

func TestEqualTellsUnknownApart(t *testing.T) {
	a, _ := heartbeat.ScheduleFromRow(nil, nil, nil)
	b, _ := heartbeat.ScheduleFromRow(nil, nil, nil)
	if !a.Equal(b) {
		t.Error("dois desconhecidos deram diferentes")
	}
	if a.Equal(heartbeat.Schedule{}) || (heartbeat.Schedule{}).Equal(a) {
		t.Error("desconhecido deu igual ao valor zero")
	}
}

// Host adiantado: a tarefa de 3h avisa antes das 3h. Dentro da folga, o sinal é
// desse disparo, e o prazo vai para o do dia seguinte. As bordas são exatas.
func TestSuccessJustBeforeTheFireCountsForThatFire(t *testing.T) {
	sch := cronEm(t, "0 3 * * *", "UTC")
	casos := []struct {
		nome string
		de   time.Time
		want time.Time
	}{
		{"um segundo antes", time.Date(2026, 11, 6, 2, 59, 59, 0, time.UTC), time.Date(2026, 11, 7, 3, 5, 0, 0, time.UTC)},
		{"na borda da folga", time.Date(2026, 11, 6, 2, 59, 30, 0, time.UTC), time.Date(2026, 11, 7, 3, 5, 0, 0, time.UTC)},
		{"logo antes da folga", time.Date(2026, 11, 6, 2, 59, 29, 0, time.UTC), time.Date(2026, 11, 6, 3, 5, 0, 0, time.UTC)},
		{"depois do disparo", time.Date(2026, 11, 6, 3, 0, 1, 0, time.UTC), time.Date(2026, 11, 7, 3, 5, 0, 0, time.UTC)},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := heartbeat.FirstDeadline(sch, tolerancia, c.de); !got.Equal(c.want) {
				t.Errorf("%v, queria %v", got, c.want)
			}
		})
	}
}

// O período conta do próprio sinal: a folga do cron não entra.
func TestPeriodDeadlineHasNoEarlyMargin(t *testing.T) {
	got := heartbeat.FirstDeadline(hora, tolerancia, agora)
	if want := time.Date(2026, 11, 5, 13, 5, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("%v, queria %v", got, want)
	}
}

func TestFirstDeadlineAddsGraceToTheNextFire(t *testing.T) {
	got := heartbeat.FirstDeadline(cronEm(t, "0 3 * * *", "UTC"), tolerancia, agora)
	if want := time.Date(2026, 11, 6, 3, 5, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("%v, queria %v", got, want)
	}
}

func TestOpeningPrefersTheAnnouncedFailure(t *testing.T) {
	prazo := agora.Add(-time.Hour)
	causa, desde := heartbeat.Opening(prazo, nil)
	if causa != heartbeat.CauseLate || !desde.Equal(prazo) {
		t.Errorf("atraso: %s desde %v", causa, desde)
	}
	falha := agora.Add(-2 * time.Hour)
	causa, desde = heartbeat.Opening(prazo, &falha)
	if causa != heartbeat.CauseFailed || !desde.Equal(falha) {
		t.Errorf("falha: %s desde %v", causa, desde)
	}
}

func TestClosing(t *testing.T) {
	inicio := agora.Add(-time.Hour)
	antes, igual, depois := inicio.Add(-time.Minute), inicio, inicio.Add(time.Minute)
	casos := []struct {
		nome     string
		sucesso  *time.Time
		want     time.Time
		retomada bool
	}{
		{"sem sucesso", nil, agora, true},
		{"sucesso antigo", &antes, agora, true},
		{"sucesso no início", &igual, inicio, false},
		{"sucesso depois", &depois, depois, false},
	}
	for _, c := range casos {
		got, retomada := heartbeat.Closing(inicio, c.sucesso, agora)
		if !got.Equal(c.want) || retomada != c.retomada {
			t.Errorf("%s: %v retomada=%v, queria %v retomada=%v", c.nome, got, retomada, c.want, c.retomada)
		}
	}
}

func TestNewPingTokenIsRandomAnd256Bits(t *testing.T) {
	vistos := map[string]bool{}
	for range 100 {
		token, hash := heartbeat.NewPingToken()
		cru, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(cru) != 32 {
			t.Fatalf("token %d bytes, erro %v, queria 32", len(cru), err)
		}
		if vistos[token] {
			t.Fatal("token repetido")
		}
		vistos[token] = true
		if string(hash) != string(crypto.HashToken(token)) {
			t.Fatal("o hash devolvido não é o do token")
		}
	}
}

func TestHashPingToken(t *testing.T) {
	token, hash := heartbeat.NewPingToken()
	got, ok := heartbeat.HashPingToken(token)
	if !ok || string(got) != string(hash) {
		t.Fatalf("token válido: ok=%v", ok)
	}
	for _, malformado := range []string{"", "curto", token + "a", token[:42] + "!", token[:42] + "="} {
		if _, ok := heartbeat.HashPingToken(malformado); ok {
			t.Errorf("%q passou como token", malformado)
		}
	}
}

func TestNextRunsFollowTheScheduleInItsTimezone(t *testing.T) {
	sch, err := heartbeat.NewCron("30 3 * * 1", "America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	// Quinta, 24/9/2026, 12:00 UTC. Às 3h30 de segunda em São Paulo são 6h30 UTC.
	got := sch.NextRuns(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC), 3)
	want := []time.Time{
		time.Date(2026, 9, 28, 6, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 5, 6, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 12, 6, 30, 0, 0, time.UTC),
	}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("disparos %v, queria %v", got, want)
	}
}

// 29/2 de 2096 é o último antes de 2104: a próxima procura esgota os cinco anos.
func TestNextRunsStopWhenTheNextIsOutOfReach(t *testing.T) {
	sch, err := heartbeat.NewCron("0 0 29 2 *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if got := sch.NextRuns(time.Date(2097, 1, 1, 0, 0, 0, 0, time.UTC), 3); len(got) != 0 {
		t.Errorf("disparos %v, queria nenhum: instante zero seria data inventada", got)
	}
}

func TestParseExitCode(t *testing.T) {
	casos := []struct {
		cru   string
		ok    bool
		sinal heartbeat.Signal
		valor int16
	}{
		{"0", true, heartbeat.SignalSuccess, 0},
		{"1", true, heartbeat.SignalFail, 1},
		{"9", true, heartbeat.SignalFail, 9},
		{"10", true, heartbeat.SignalFail, 10},
		{"254", true, heartbeat.SignalFail, 254},
		{"255", true, heartbeat.SignalFail, 255},
		{"256", false, "", 0},
		{"300", false, "", 0},
		{"1000", false, "", 0},
		{"", false, "", 0},
		{"-1", false, "", 0},
		{"+1", false, "", 0},
		{"00", false, "", 0},
		{"01", false, "", 0},
		{"007", false, "", 0},
		{"1.5", false, "", 0},
		{"0x1", false, "", 0},
		{"1e2", false, "", 0},
		{"abc", false, "", 0},
		{" 1", false, "", 0},
		{"1\n", false, "", 0},
		{"\u0661", false, "", 0}, // dígito não ASCII
		{"99999999999999999999", false, "", 0},
	}
	for _, c := range casos {
		t.Run(c.cru, func(t *testing.T) {
			got, ok := heartbeat.ParseExitCode(c.cru)
			if ok != c.ok {
				t.Fatalf("ok = %v, queria %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got.Signal() != c.sinal || got.Value() != c.valor {
				t.Errorf("deu %s/%d, queria %s/%d", got.Signal(), got.Value(), c.sinal, c.valor)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	teto := strings.Repeat("é", heartbeat.MaxDescriptionLength)
	casos := []struct {
		nome, entrada, quer, motivo, campo string
	}{
		{nome: "vazia é sem descrição", entrada: "", quer: ""},
		{nome: "só espaço vira vazia", entrada: " \n\t ", quer: ""},
		{nome: "limpa as pontas", entrada: "  roda no nas\n", quer: "roda no nas"},
		{nome: "mantém quebra de linha e tab", entrada: "linha 1\n\tlinha 2", quer: "linha 1\n\tlinha 2"},
		{nome: "emoji composto passa", entrada: "dono \U0001F469\u200D\U0001F4BB", quer: "dono \U0001F469\u200D\U0001F4BB"},
		{nome: "no teto, em caracteres", entrada: teto, quer: teto},
		{nome: "acima do teto", entrada: teto + "é", motivo: heartbeat.ReasonOutOfRange, campo: "description"},
		{nome: "10000 caracteres de dois bytes", entrada: strings.Repeat("é", 10000), quer: strings.Repeat("é", 10000)},
		{nome: "10000 emojis de quatro bytes", entrada: strings.Repeat("😀", 10000), quer: strings.Repeat("😀", 10000)},
		{nome: "10001 caracteres", entrada: strings.Repeat("é", 10001), motivo: heartbeat.ReasonOutOfRange, campo: "description"},
		{nome: "NUL", entrada: "a\x00b", motivo: heartbeat.ReasonInvalid, campo: "description[0]"},
		{nome: "retorno de carro", entrada: "a\r\nb", motivo: heartbeat.ReasonInvalid, campo: "description[0]"},
		{nome: "inversão bidi na segunda linha", entrada: "a\n\u202Eb", motivo: heartbeat.ReasonInvalid, campo: "description[1]"},
		{nome: "linha em branco conta", entrada: "a\n\n\u202Eb", motivo: heartbeat.ReasonInvalid, campo: "description[2]"},
		{nome: "aponta a primeira de duas", entrada: "a\n\u202Eb\n\u202Ec", motivo: heartbeat.ReasonInvalid, campo: "description[1]"},
		{nome: "conta as linhas do texto recebido", entrada: "\n \n\u202Eb", motivo: heartbeat.ReasonInvalid, campo: "description[2]"},
		{nome: "última linha, sem quebra no fim", entrada: "a\nb\n\u202Ec", motivo: heartbeat.ReasonInvalid, campo: "description[2]"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := heartbeat.ValidateDescription(c.entrada)
			var verr *heartbeat.ValidationError
			switch {
			case c.motivo == "" && (err != nil || got != c.quer):
				t.Errorf("devolveu %.40q (%d bytes), %v; queria %.40q (%d bytes)", got, len(got), err, c.quer, len(c.quer))
			case c.motivo != "" && (!errors.As(err, &verr) || verr.Field != c.campo || verr.Reason != c.motivo):
				t.Errorf("erro %v; queria %s %s", err, c.campo, c.motivo)
			}
		})
	}
}

// O nome da cópia cabe nos 100 caracteres do cadastro, contados em caracteres:
// cortar por byte deixaria um nome multibyte curto demais (ou partiria um
// caractere ao meio).
func TestCloneName(t *testing.T) {
	casos := []struct{ nome, entrada, quer string }{
		{"curto ganha o sufixo", "backup", "backup (cópia)"},
		{"no teto corta para caber", strings.Repeat("a", 100), strings.Repeat("a", 92) + " (cópia)"},
		{"92 caracteres cabem sem corte", strings.Repeat("a", 92), strings.Repeat("a", 92) + " (cópia)"},
		{"93 caracteres perdem um", strings.Repeat("a", 93), strings.Repeat("a", 92) + " (cópia)"},
		{"multibyte corta por caractere", strings.Repeat("é", 99), strings.Repeat("é", 92) + " (cópia)"},
		{"espaço deixado pelo corte sai", strings.Repeat("a", 91) + " b...", strings.Repeat("a", 91) + " (cópia)"},
		{"combinante no fim sem corte fica", "Cafe\u0301", "Cafe\u0301 (cópia)"},
		{"seletor de variação sem corte fica", "Backup \u2764\uFE0F", "Backup \u2764\uFE0F (cópia)"},
		{"ZWJ deixado pelo corte sai", strings.Repeat("a", 90) + "x\u200Dyz", strings.Repeat("a", 90) + "x (cópia)"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := heartbeat.CloneName(c.entrada)
			if got != c.quer {
				t.Errorf("CloneName(%.20q…) = %q, queria %q", c.entrada, got, c.quer)
			}
			if n := utf8.RuneCountInString(got); n > heartbeat.MaxNameLength {
				t.Errorf("%d caracteres, acima de %d", n, heartbeat.MaxNameLength)
			}
			if _, err := heartbeat.ValidateName(got); err != nil {
				t.Errorf("o nome da cópia %q não passa no cadastro: %v", got, err)
			}
		})
	}
}

var (
	t0   = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ridA = uuid.MustParse("0193a1b2-0000-7000-8000-00000000000a")
	ridB = uuid.MustParse("0193a1b2-0000-7000-8000-00000000000b")
)

func comRid(s heartbeat.State, sig heartbeat.Signal, em time.Time, run heartbeat.Run) (heartbeat.State, *heartbeat.Event) {
	return s.Apply(sig, em, run, hora, tolerancia)
}

func ptr[T any](v T) *T { return &v }

// /log registra o evento e não mexe em último sinal, prazo nem execução.
func TestLogChangesNothingButTheEvent(t *testing.T) {
	antes := heartbeat.State{
		DeadlineAt:    time.Date(2026, 11, 10, 13, 5, 0, 0, time.UTC),
		LastPingAt:    time.Date(2026, 11, 10, 11, 59, 0, 0, time.UTC),
		LastSuccessAt: time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC),
		LastStartAt:   time.Date(2026, 11, 10, 11, 58, 0, 0, time.UTC),
		LastSignal:    heartbeat.SignalSuccess,
		FailingSince:  time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC),
		LastRuntimeMS: ptr(int64(1234)),
		LastRunID:     ptr(ridB),
	}
	s, ev := comRid(antes, heartbeat.SignalLog, t0, heartbeat.Run{ID: ptr(ridA)})
	if s != antes {
		t.Errorf("log mudou o estado: %+v", s)
	}
	if ev == nil || !ev.At.Equal(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)) || ev.Kind != heartbeat.SignalLog ||
		ev.RuntimeMS != nil || ev.RunID == nil || *ev.RunID != ridA {
		t.Fatalf("evento %+v, queria log em t0 com ridA", ev)
	}
	s, ev = comRid(s, heartbeat.SignalLog, t0.Add(time.Second), heartbeat.Run{})
	if ev == nil || ev.Kind != heartbeat.SignalLog || ev.RunID != nil {
		t.Errorf("segundo log em 1 s: evento %+v, queria outro log sem rid", ev)
	}
	if s != antes {
		t.Errorf("segundo log mudou o estado: %+v", s)
	}
}

func TestRepeatComparesTheRunID(t *testing.T) {
	casos := []struct {
		nome            string
		primeiro, outro *uuid.UUID
		repeticao       bool
	}{
		{"mesmo rid", ptr(ridA), ptr(ridA), true},
		{"rids distintos", ptr(ridA), ptr(ridB), false},
		{"sem rid e com rid", nil, ptr(ridA), false},
		{"com rid e sem rid", ptr(ridA), nil, false},
		{"sem rid nos dois", nil, nil, true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, _ := comRid(heartbeat.State{}, heartbeat.SignalSuccess, t0, heartbeat.Run{ID: c.primeiro})
			_, ev := comRid(s, heartbeat.SignalSuccess, t0.Add(5*time.Second), heartbeat.Run{ID: c.outro})
			if (ev == nil) != c.repeticao {
				t.Fatalf("evento %+v, repetição esperada=%v", ev, c.repeticao)
			}
		})
	}
}

func TestOverlappingRunsMeasureTheirOwnStart(t *testing.T) {
	s, _ := comRid(heartbeat.State{}, heartbeat.SignalStart, t0, heartbeat.Run{ID: ptr(ridA)})
	s, _ = comRid(s, heartbeat.SignalStart, t0.Add(10*time.Minute), heartbeat.Run{ID: ptr(ridB)})
	s, ev := comRid(s, heartbeat.SignalSuccess, t0.Add(30*time.Minute), heartbeat.Run{ID: ptr(ridA), StartAt: ptr(t0)})
	if ev == nil || ev.RuntimeMS == nil || *ev.RuntimeMS != 1800000 {
		t.Fatalf("duração de A %+v, queria 1800000", ev)
	}
	if !s.LastStartAt.Equal(time.Date(2026, 11, 10, 12, 10, 0, 0, time.UTC)) {
		t.Errorf("início aberto %v, queria o de B (12:10)", s.LastStartAt)
	}
	s, ev = comRid(s, heartbeat.SignalSuccess, t0.Add(40*time.Minute),
		heartbeat.Run{ID: ptr(ridB), StartAt: ptr(t0.Add(10 * time.Minute))})
	if ev == nil || ev.RuntimeMS == nil || *ev.RuntimeMS != 1800000 {
		t.Fatalf("duração de B %+v, queria 1800000", ev)
	}
	if !s.LastStartAt.IsZero() {
		t.Errorf("o início de B não foi consumido: %v", s.LastStartAt)
	}
	if s.LastRuntimeMS == nil || *s.LastRuntimeMS != 1800000 {
		t.Errorf("última duração %v, queria 1800000", s.LastRuntimeMS)
	}
}

func TestRunWithoutStartInTheLogHasNoRuntime(t *testing.T) {
	aberto := time.Date(2026, 11, 10, 11, 50, 0, 0, time.UTC)
	antes := heartbeat.State{
		DeadlineAt: time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC), LastStartAt: aberto,
		LastPingAt: aberto, LastSignal: heartbeat.SignalStart, LastRuntimeMS: ptr(int64(5000)),
	}
	s, ev := comRid(antes, heartbeat.SignalSuccess, t0, heartbeat.Run{ID: ptr(ridA)})
	if ev == nil || ev.RuntimeMS != nil {
		t.Fatalf("evento %+v, queria sucesso sem duração", ev)
	}
	if s.LastRuntimeMS != nil {
		t.Errorf("última duração %d, queria nil", *s.LastRuntimeMS)
	}
	if !s.LastStartAt.Equal(time.Date(2026, 11, 10, 11, 50, 0, 0, time.UTC)) {
		t.Errorf("início aberto %v, queria 11:50 intacto", s.LastStartAt)
	}
	if !s.DeadlineAt.Equal(time.Date(2026, 11, 10, 13, 5, 0, 0, time.UTC)) {
		t.Errorf("prazo %v, queria 13:05", s.DeadlineAt)
	}
}

func TestLastRunIDFollowsTheLastStateChangingSignal(t *testing.T) {
	s, _ := comRid(heartbeat.State{}, heartbeat.SignalStart, t0, heartbeat.Run{ID: ptr(ridA)})
	if s.LastRunID == nil || *s.LastRunID != ridA {
		t.Fatalf("último rid %v, queria A", s.LastRunID)
	}
	s, _ = comRid(s, heartbeat.SignalLog, t0.Add(time.Minute), heartbeat.Run{ID: ptr(ridB)})
	if s.LastRunID == nil || *s.LastRunID != ridA {
		t.Fatalf("log mudou o último rid para %v", s.LastRunID)
	}
	s, _ = comRid(s, heartbeat.SignalFail, t0.Add(2*time.Minute), heartbeat.Run{})
	if s.LastRunID != nil {
		t.Errorf("falha sem rid deixou o último rid em %v", *s.LastRunID)
	}
}

// Só o sucesso de um heartbeat pausado com retomada por ping o retoma.
func TestResumedByPing(t *testing.T) {
	const (
		start   = heartbeat.SignalStart
		success = heartbeat.SignalSuccess
		fail    = heartbeat.SignalFail
		log     = heartbeat.SignalLog
	)
	casos := []struct {
		sig                  heartbeat.Signal
		enabled, pingResumes bool
		want                 bool
	}{
		{success, false, true, true},
		{success, false, false, false},
		{success, true, true, false},
		{success, true, false, false},
		{start, false, true, false},
		{start, false, false, false},
		{start, true, true, false},
		{fail, false, true, false},
		{fail, false, false, false},
		{fail, true, true, false},
		{log, false, true, false},
		{log, false, false, false},
		{log, true, true, false},
	}
	for _, c := range casos {
		if got := heartbeat.ResumedByPing(c.sig, c.enabled, c.pingResumes); got != c.want {
			t.Errorf("ResumedByPing(%s, enabled=%v, pingResumes=%v) = %v, queria %v",
				c.sig, c.enabled, c.pingResumes, got, c.want)
		}
	}
}

// A janela é [prazo - tolerância, prazo) por si só, sem depender de o caso
// late vir antes no switch de statusOf.
func TestGraceWindowEndsAtTheDeadline(t *testing.T) {
	prazo := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	f := heartbeat.StatusFacts{Enabled: true, KnownSchedule: true, FinishedARun: true, Deadline: prazo, Grace: 5 * time.Minute}
	if !f.InGrace(prazo.Add(-time.Nanosecond)) {
		t.Error("um instante antes do prazo ficou fora da tolerância")
	}
	if f.InGrace(prazo) {
		t.Error("o prazo ficou dentro da tolerância")
	}
}

// "Em tolerância" é a janela [prazo - tolerância, prazo): começa no instante
// exato e termina quando o atraso começa (A9).
func TestGraceStateBoundaries(t *testing.T) {
	emDia := func() heartbeat.StatusFacts {
		return heartbeat.StatusFacts{
			Enabled: true, KnownSchedule: true, FinishedARun: true,
			Deadline: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), Grace: 5 * time.Minute,
		}
	}
	dentro := time.Date(2026, 11, 10, 11, 57, 0, 0, time.UTC)
	casos := []struct {
		nome   string
		fatos  func(*heartbeat.StatusFacts)
		agora  time.Time
		estado heartbeat.Status
	}{
		{"início exato da tolerância", nil, time.Date(2026, 11, 10, 11, 55, 0, 0, time.UTC), heartbeat.StatusGrace},
		{"um instante antes", nil, time.Date(2026, 11, 10, 11, 54, 59, 999999999, time.UTC), heartbeat.StatusUp},
		{"no prazo", nil, time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), heartbeat.StatusLate},
		{"execução aberta", func(f *heartbeat.StatusFacts) { f.RunOpen = true }, dentro, heartbeat.StatusUp},
		{"nunca terminou", func(f *heartbeat.StatusFacts) { f.FinishedARun = false }, dentro, heartbeat.StatusWaiting},
		{"agendamento desconhecido", func(f *heartbeat.StatusFacts) { f.KnownSchedule = false }, dentro, heartbeat.StatusUp},
		{"pausado", func(f *heartbeat.StatusFacts) { f.Enabled = false }, dentro, heartbeat.StatusPaused},
		{"falhando", func(f *heartbeat.StatusFacts) { f.Failing = true }, dentro, heartbeat.StatusFailing},
		{"incidente aberto", func(f *heartbeat.StatusFacts) { f.OpenIncident = true }, dentro, heartbeat.StatusLate},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			f := emDia()
			if c.fatos != nil {
				c.fatos(&f)
			}
			if got := heartbeat.StatusOf(f, c.agora); got != c.estado {
				t.Errorf("estado %q, queria %q", got, c.estado)
			}
		})
	}
}
