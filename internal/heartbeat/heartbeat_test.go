package heartbeat_test

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

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
	return s.Apply(sig, em, hora, tolerancia)
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

// 1h30 acontece duas vezes em 1/11/2026 em Nova York.
func TestCronAcrossTheRepeatedHourMovesForward(t *testing.T) {
	s := cronEm(t, "30 1 * * *", "America/New_York")
	primeiro := s.Next(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !primeiro.Equal(want) {
		t.Fatalf("primeiro %v, queria %v", primeiro, want)
	}
	if s.Next(primeiro).Before(primeiro.Add(time.Minute)) {
		t.Errorf("o próximo depois de %v não avançou", primeiro)
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
	if _, err := heartbeat.ScheduleFromRow(nil, nil, nil); err == nil {
		t.Fatal("linha sem agendamento passou")
	}
	if _, err := heartbeat.ScheduleFromRow(nil, &expr, nil); err == nil {
		t.Fatal("cron sem fuso passou")
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

func TestResolvedAt(t *testing.T) {
	inicio := agora.Add(-time.Hour)
	antes, igual, depois := inicio.Add(-time.Minute), inicio, inicio.Add(time.Minute)
	casos := []struct {
		nome    string
		sucesso *time.Time
		want    time.Time
	}{
		{"sem sucesso", nil, agora},
		{"sucesso antigo", &antes, agora},
		{"sucesso no início", &igual, inicio},
		{"sucesso depois", &depois, depois},
	}
	for _, c := range casos {
		if got := heartbeat.ResolvedAt(inicio, c.sucesso, agora); !got.Equal(c.want) {
			t.Errorf("%s: %v, queria %v", c.nome, got, c.want)
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
