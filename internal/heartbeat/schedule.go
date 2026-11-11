package heartbeat

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser aceita só os 5 campos clássicos: sem segundos, sem descritores
// como @daily e sem CRON_TZ, que esconderia o fuso dentro da expressão.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// cronReference é de onde sai a checagem de que a expressão dispara. A resposta
// não depende do instante, e um instante fixo deixa o pacote sem relógio; a
// janela de cinco anos da biblioteca cobre o 29 de fevereiro a partir daqui.
var cronReference = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// Schedule diz quando a tarefa deve dar sinal. O valor zero não é válido:
// nasce de NewPeriod, NewCron ou ScheduleFromRow.
type Schedule struct {
	period   time.Duration
	cron     cron.Schedule
	location *time.Location
	expr     string
	timezone string
	// unknown marca a linha gravada por uma versão futura numa forma que esta
	// não sabe calcular. É campo próprio, e não o valor zero, para que um
	// Schedule{} esquecido continue sendo bug e não vire "desconhecido".
	unknown bool
}

// NewPeriod é o agendamento por período fixo, contado a partir do sinal.
func NewPeriod(period time.Duration) (Schedule, error) {
	if period < MinPeriod || period > MaxPeriod {
		return Schedule{}, &ValidationError{Field: "period_sec", Reason: ReasonOutOfRange}
	}
	return Schedule{period: period}, nil
}

// NewCron é o agendamento por expressão de 5 campos, num fuso IANA
// obrigatório: sem ele, o horário dependeria da máquina (D18).
func NewCron(expr, timezone string) (Schedule, error) {
	loc, err := parseTimezone(timezone)
	if err != nil {
		return Schedule{}, err
	}
	if expr == "" {
		return Schedule{}, &ValidationError{Field: "expr", Reason: ReasonRequired}
	}
	if len(expr) > MaxCronLength || strings.Contains(expr, "TZ=") {
		return Schedule{}, &ValidationError{Field: "expr", Reason: ReasonInvalid}
	}
	sch, err := cronParser.Parse(expr)
	if err != nil {
		return Schedule{}, &ValidationError{Field: "expr", Reason: ReasonInvalid}
	}
	// 0 0 30 2 * passa no parser e nunca dispara: Next devolve o instante
	// zero depois de procurar cinco anos, e o prazo nasceria no passado.
	if sch.Next(cronReference.In(loc)).IsZero() {
		return Schedule{}, &ValidationError{Field: "expr", Reason: ReasonInvalid}
	}
	return Schedule{cron: sch, location: loc, expr: expr, timezone: timezone}, nil
}

// ScheduleFromRow remonta o agendamento das colunas de heartbeats. É o piso de
// rollback: lê o que uma versão futura gravou sem reaplicar as faixas de
// cadastro, que continuam em NewPeriod. Período acima de MaxPeriod é lido como
// está; período e cron nulos viram a forma desconhecida (IsKnown falso). Cron
// sem fuso segue erro, porque o CHECK de hoje já o impede.
func ScheduleFromRow(periodSec *int32, cronExpr, timezone *string) (Schedule, error) {
	if periodSec != nil {
		return periodFromRow(*periodSec)
	}
	if cronExpr == nil {
		return Schedule{unknown: true}, nil
	}
	if timezone == nil {
		return Schedule{}, &ValidationError{Field: "schedule", Reason: ReasonRequired}
	}
	return NewCron(*cronExpr, *timezone)
}

// periodFromRow só recusa o que nenhuma versão gravaria: sem período positivo,
// Next devolveria o próprio instante e o prazo não andaria.
func periodFromRow(sec int32) (Schedule, error) {
	if sec <= 0 {
		return Schedule{}, &ValidationError{Field: "period_sec", Reason: ReasonOutOfRange}
	}
	return Schedule{period: time.Duration(sec) * time.Second}, nil
}

func parseTimezone(timezone string) (*time.Location, error) {
	if timezone == "" {
		return nil, &ValidationError{Field: "timezone", Reason: ReasonRequired}
	}
	// "Local" é aceito por LoadLocation e é justamente o fuso da máquina.
	if len(timezone) > MaxTimezoneLength || timezone == "Local" {
		return nil, &ValidationError{Field: "timezone", Reason: ReasonInvalid}
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, &ValidationError{Field: "timezone", Reason: ReasonInvalid}
	}
	return loc, nil
}

// Next é o próximo instante em que a tarefa deve ter dado sinal depois de t.
// Num agendamento desconhecido não há próximo instante: chamar Next é bug de
// quem chama, que devia ter checado IsKnown, e inventar um prazo seria pior.
func (s Schedule) Next(t time.Time) time.Time {
	if s.unknown {
		panic("heartbeat: Next em agendamento desconhecido; confira IsKnown antes")
	}
	if s.cron == nil {
		return t.Add(s.period)
	}
	return s.cron.Next(t.In(s.location)).UTC()
}

// NextRuns são até n disparos depois de t. Menos que n só quando o próximo fica
// além dos cinco anos que a biblioteca procura: Next devolve o instante zero, e
// mostrá-lo seria inventar uma data.
func (s Schedule) NextRuns(t time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	for len(out) < n {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}

// Equal diz se os dois agendamentos são o mesmo: reenviar o que já está gravado
// não é mudança.
func (s Schedule) Equal(o Schedule) bool {
	return s.unknown == o.unknown && s.period == o.period && s.expr == o.expr && s.timezone == o.timezone
}

// IsKnown diz se esta versão sabe calcular o agendamento. Falso só numa linha
// gravada por uma versão futura; nada que nasce aqui é desconhecido.
func (s Schedule) IsKnown() bool { return !s.unknown }

// IsCron diz qual das duas formas o agendamento tem.
func (s Schedule) IsCron() bool { return s.cron != nil }

// Period é o período; zero num agendamento por cron.
func (s Schedule) Period() time.Duration { return s.period }

// CronExpr é a expressão como foi informada; vazia num agendamento por período.
func (s Schedule) CronExpr() string { return s.expr }

// Timezone é o fuso como foi informado; vazio num agendamento por período.
func (s Schedule) Timezone() string { return s.timezone }
