// Package heartbeat guarda as regras das tarefas que avisam por ping
// (docs/superpowers/specs/2026-11-05-heartbeats-design.md). As regras de
// agendamento, prazo e token (heartbeat.go, schedule.go, token.go) são puras:
// não fazem I/O e não leem relógio. Os serviços (ping.go, service.go) aplicam
// essas regras no banco.
package heartbeat

import (
	"strconv"
	"time"
	_ "time/tzdata" // o cron depende do fuso declarado, não do tzdata da imagem

	"github.com/google/uuid"
)

// Faixas que a validação aplica. Cada uma tem par num CHECK das migrations
// (a 00016, e a migration heartbeat_limits para período, tolerância e
// descrição): a validação devolve o campo culpado, e o CHECK garante que
// nenhum caminho grave lixo.
// MinPeriod e MinGrace não mudam: MinGrace sustenta EarlySignalMargin;
// MinPeriod sustenta RepeatWindow.
const (
	MinPeriod = time.Minute
	MaxPeriod = 365 * 24 * time.Hour
	MinGrace  = 30 * time.Second
	MaxGrace  = 365 * 24 * time.Hour
	// DefaultGrace é a tolerância de quem não informa uma.
	DefaultGrace = 5 * time.Minute

	MaxNameLength     = 100
	MaxCronLength     = 100
	MaxTimezoneLength = 64
	// MaxDescriptionLength é em caracteres, como o CHECK da migration
	// heartbeat_limits.
	MaxDescriptionLength = 10000

	// RepeatWindow é por quanto tempo um sinal igual ao último é repetição:
	// o curl --retry de uma tarefa cabe nela, e um período, que é de
	// no mínimo um minuto, não.
	RepeatWindow = 10 * time.Second
	// MaxEventsPerHeartbeat é o teto do log de sinais.
	MaxEventsPerHeartbeat = 1000
	// EarlySignalMargin é quanto antes de um disparo do cron um sinal ainda
	// conta como daquele disparo. Um host com o relógio adiantado termina a
	// tarefa de 3h às 02:59:59; sem a folga, o próximo disparo seria o das 3h
	// que acabou de rodar, e o incidente falso se repetiria todo dia. Fica em
	// MinGrace: acima disso, a folga passaria da menor tolerância aceita.
	EarlySignalMargin = MinGrace
	// ReopenCooldown é o intervalo mínimo entre aberturas de incidente do
	// mesmo heartbeat: um token vazado alternando falha e sucesso
	// dispararia um aviso a cada avaliação. Atrasa a abertura, nunca a perde.
	ReopenCooldown = 5 * time.Minute
)

// Motivos de ValidationError, no vocabulário fechado da API.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
)

// ValidationError aponta o campo culpado. Field e Reason vêm sempre de
// constantes do código, nunca da entrada; o que se calcula é só o índice da
// linha em description[i].
type ValidationError struct {
	Field, Reason string
}

func (e *ValidationError) Error() string {
	return "heartbeat inválido: " + e.Field + " " + e.Reason
}

// Causas de incidente de heartbeat, no enum de incidents.cause.
const (
	CauseLate   = "heartbeat_late"
	CauseFailed = "heartbeat_failed"
)

// Opening diz por que o incidente abre e desde quando: a falha avisada ganha
// do atraso, porque é o que a tarefa disse de si mesma.
func Opening(deadlineAt time.Time, failingSince *time.Time) (cause string, startedAt time.Time) {
	if failingSince != nil {
		return CauseFailed, *failingSince
	}
	return CauseLate, deadlineAt
}

// Closing diz quando o incidente acabou e se foi pela retomada: o sucesso que o
// resolveu, ou now quando o dono retomou um heartbeat pausado sem sucesso
// nenhum depois do início.
func Closing(startedAt time.Time, lastSuccessAt *time.Time, now time.Time) (resolvedAt time.Time, byResume bool) {
	if lastSuccessAt != nil && !lastSuccessAt.Before(startedAt) {
		return *lastSuccessAt, false
	}
	return now, true
}

// Signal é o que a tarefa avisa. Os valores são os do CHECK de
// heartbeat_events.kind.
type Signal string

const (
	SignalStart   Signal = "start"
	SignalSuccess Signal = "success"
	SignalFail    Signal = "fail"
	// SignalLog só registra: não mexe em último sinal, prazo nem execução.
	SignalLog Signal = "log"
)

// Source diz de onde veio o sinal, nos valores do CHECK de
// heartbeat_events.source.
type Source string

const (
	SourcePing  Source = "ping"
	SourcePanel Source = "panel"
)

// Run identifica a execução que mandou o sinal. Sem ID, o sinal vale como
// antes do rid. StartAt é o início de mesmo rid lido do log, ou nil; só faz
// sentido com ID. Manual é o sucesso do sinalizar agora: ele não é a tarefa,
// então não encerra a execução aberta nem leva a duração dela.
type Run struct {
	ID      *uuid.UUID
	StartAt *time.Time
	Manual  bool
}

// ExitCode é o código de saída que a tarefa mandou no ping. O valor zero é o
// sucesso; os outros nascem de ParseExitCode.
type ExitCode struct {
	value int16
}

// bitsDoExitCode é o tamanho do código de saída de um processo Unix: 0..255.
const bitsDoExitCode = 8

// ParseExitCode lê o sufixo da rota. Só a forma que o $? do shell produz é
// aceita: zero à esquerda é erro de quem chama, e não um número a interpretar.
// ParseUint já recusa sinal, espaço e o que não for dígito.
func ParseExitCode(s string) (ExitCode, bool) {
	if len(s) > 1 && s[0] == '0' {
		return ExitCode{}, false
	}
	n, err := strconv.ParseUint(s, 10, bitsDoExitCode)
	if err != nil {
		return ExitCode{}, false
	}
	return ExitCode{value: int16(n)}, true
}

// Signal é o sinal que o código representa: 0 é sucesso, o resto é falha.
func (c ExitCode) Signal() Signal {
	if c.value == 0 {
		return SignalSuccess
	}
	return SignalFail
}

// Value é o número, em 0..255.
func (c ExitCode) Value() int16 { return c.value }

// State é a parte da linha que os sinais mudam. Instante zero é "nunca".
type State struct {
	DeadlineAt    time.Time
	LastPingAt    time.Time
	LastSuccessAt time.Time
	LastStartAt   time.Time
	LastSignal    Signal
	FailingSince  time.Time
	// LastRuntimeMS é a duração da última execução encerrada; nil se ela não
	// foi medida.
	LastRuntimeMS *int64
	// LastRunID é o rid do último sinal que mudou o estado; nil sem rid.
	LastRunID *uuid.UUID
}

// Event é a linha que um sinal deixa no log. Runtime só existe num sucesso ou
// numa falha com início aberto.
type Event struct {
	At        time.Time
	Kind      Signal
	RuntimeMS *int64
	RunID     *uuid.UUID
}

// Apply aplica um sinal ao estado (seção 5 da spec). Devolve o estado novo e
// o evento a gravar; sem evento, o sinal foi repetição e nada muda.
//
// Só o sucesso move o prazo: a tolerância cobre o tempo de execução, e
// um início sem sucesso vira atraso no prazo normal. O log nunca é repetição:
// cada linha que a tarefa manda é uma linha que ela quer ver.
func (s State) Apply(sig Signal, now time.Time, run Run, sch Schedule, grace time.Duration) (State, *Event) {
	ev := &Event{At: now, Kind: sig, RunID: run.ID}
	if sig == SignalLog {
		return s, ev
	}
	if s.isRepeat(sig, now, run.ID) {
		return s, nil
	}
	s.LastPingAt, s.LastSignal, s.LastRunID = now, sig, run.ID
	switch sig {
	case SignalStart:
		s.LastStartAt = now
	case SignalFail:
		ev.RuntimeMS = s.closeRun(now, run)
		if s.FailingSince.IsZero() {
			s.FailingSince = now
		}
	case SignalSuccess:
		if !run.Manual {
			ev.RuntimeMS = s.closeRun(now, run)
		}
		s.FailingSince = time.Time{}
		s.LastSuccessAt = now
		// Num agendamento que esta versão não conhece não há próximo instante
		// a calcular: o prazo gravado fica como está até a versão que o conhece.
		if sch.IsKnown() {
			s.DeadlineAt = FirstDeadline(sch, grace, now)
		}
	}
	return s, ev
}

// closeRun encerra a execução e devolve a duração dela. Sem início, a última
// execução fica sem medida: repetir a duração da anterior diria algo sobre uma
// execução que ninguém mediu.
//
// Sem rid, mede do início aberto e o consome. Com rid, mede do início de mesmo
// rid e só consome o aberto quando ele é esse início: o aberto pode ser de
// outra execução sobreposta, que ainda vai terminar.
func (s *State) closeRun(now time.Time, run Run) *int64 {
	if run.ID == nil {
		started := s.LastStartAt
		s.LastStartAt = time.Time{}
		return s.measure(started, now)
	}
	if run.StartAt == nil {
		return s.measure(time.Time{}, now)
	}
	if run.StartAt.Equal(s.LastStartAt) {
		s.LastStartAt = time.Time{}
	}
	return s.measure(*run.StartAt, now)
}

// measure grava como última duração a de started até now, ou nil se started é
// "nunca".
func (s *State) measure(started, now time.Time) *int64 {
	s.LastRuntimeMS = nil
	if !started.IsZero() {
		ms := max(now.Sub(started).Milliseconds(), 0)
		s.LastRuntimeMS = &ms
	}
	return s.LastRuntimeMS
}

// isRepeat vale para o intervalo negativo também: com relógios de instâncias
// diferentes, now pode cair um pouco antes do último ping. Sinais de rid
// diferentes são execuções diferentes, nunca repetição.
func (s State) isRepeat(sig Signal, now time.Time, runID *uuid.UUID) bool {
	return s.LastSignal == sig && sameRun(s.LastRunID, runID) && now.Sub(s.LastPingAt) < RepeatWindow
}

func sameRun(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ResumedByPing diz se o sinal retoma um heartbeat pausado: só o sucesso, pela
// URL de ping ou pelo sinalizar agora, e só quando o dono deixou a retomada
// por ping ligada.
func ResumedByPing(sig Signal, enabled, pingResumes bool) bool {
	return sig == SignalSuccess && !enabled && pingResumes
}

// FirstDeadline é o prazo a contar de from: o próximo instante esperado mais
// a tolerância. Serve à criação, à retomada e ao sucesso. Só o cron tem
// a folga EarlySignalMargin: no período, o próximo instante conta do próprio
// sinal, e não há disparo marcado para confundir.
func FirstDeadline(sch Schedule, grace time.Duration, from time.Time) time.Time {
	if sch.IsCron() {
		from = from.Add(EarlySignalMargin)
	}
	return sch.Next(from).Add(grace)
}

// ValidateGrace confere a tolerância.
func ValidateGrace(grace time.Duration) error {
	if grace < MinGrace || grace > MaxGrace {
		return &ValidationError{Field: "grace_sec", Reason: ReasonOutOfRange}
	}
	return nil
}
