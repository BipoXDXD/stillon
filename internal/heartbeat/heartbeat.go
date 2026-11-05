// Package heartbeat guarda as regras das tarefas que avisam por ping
// (docs/superpowers/specs/2026-11-05-heartbeats-design.md): agendamento,
// prazo, token. O núcleo é puro: não faz I/O e não lê relógio.
package heartbeat

import (
	"time"
	_ "time/tzdata" // o cron depende do fuso declarado, não do tzdata da imagem (D18)
)

// Faixas que a validação aplica. Cada uma tem par num CHECK da migration
// 00016: a validação devolve o campo culpado, e o CHECK garante que nenhum
// caminho grave lixo.
const (
	MinPeriod = time.Minute
	MaxPeriod = 31 * 24 * time.Hour
	MinGrace  = 30 * time.Second
	MaxGrace  = 7 * 24 * time.Hour
	// DefaultGrace é a tolerância de quem não informa uma.
	DefaultGrace = 5 * time.Minute

	MaxNameLength     = 100
	MaxCronLength     = 100
	MaxTimezoneLength = 64

	// RepeatWindow é por quanto tempo um sinal igual ao último é repetição
	// (D9): o curl --retry de uma tarefa cabe nela, e um período, que é de
	// no mínimo um minuto, não.
	RepeatWindow = 10 * time.Second
	// MaxEventsPerHeartbeat é o teto do log de sinais (D15).
	MaxEventsPerHeartbeat = 1000
	// EventsRetention é a idade máxima de um evento, para o heartbeat que
	// parou de pingar e nunca alcançaria o teto (D15).
	EventsRetention = 30 * 24 * time.Hour
)

// Motivos de ValidationError, no vocabulário fechado da API.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
)

// ValidationError aponta o campo culpado. Field e Reason vêm sempre de
// constantes do código, nunca da entrada.
type ValidationError struct {
	Field, Reason string
}

func (e *ValidationError) Error() string {
	return "heartbeat inválido: " + e.Field + " " + e.Reason
}

// Signal é o que a tarefa avisa. Os valores são os do CHECK de
// heartbeat_events.kind.
type Signal string

const (
	SignalStart   Signal = "start"
	SignalSuccess Signal = "success"
	SignalFail    Signal = "fail"
)

// State é a parte da linha que os sinais mudam. Instante zero é "nunca".
type State struct {
	DeadlineAt    time.Time
	LastPingAt    time.Time
	LastSuccessAt time.Time
	LastStartAt   time.Time
	LastSignal    Signal
	FailingSince  time.Time
}

// Event é a linha que um sinal deixa no log. Runtime só existe num sucesso
// com início aberto.
type Event struct {
	At        time.Time
	Kind      Signal
	RuntimeMS *int64
}

// Apply aplica um sinal ao estado (seção 5 da spec). Devolve o estado novo e
// o evento a gravar; sem evento, o sinal foi repetição e nada muda.
//
// Só o sucesso move o prazo (D10): a tolerância cobre o tempo de execução, e
// um início sem sucesso vira atraso no prazo normal.
func (s State) Apply(sig Signal, now time.Time, sch Schedule, grace time.Duration) (State, *Event) {
	if s.isRepeat(sig, now) {
		return s, nil
	}
	s.LastPingAt, s.LastSignal = now, sig
	ev := &Event{At: now, Kind: sig}
	switch sig {
	case SignalStart:
		s.LastStartAt = now
	case SignalFail:
		if s.FailingSince.IsZero() {
			s.FailingSince = now
		}
	case SignalSuccess:
		if !s.LastStartAt.IsZero() {
			ms := max(now.Sub(s.LastStartAt).Milliseconds(), 0)
			ev.RuntimeMS = &ms
		}
		s.LastStartAt = time.Time{}
		s.FailingSince = time.Time{}
		s.LastSuccessAt = now
		s.DeadlineAt = FirstDeadline(sch, grace, now)
	}
	return s, ev
}

// isRepeat vale para o intervalo negativo também: com relógios de instâncias
// diferentes, now pode cair um pouco antes do último ping.
func (s State) isRepeat(sig Signal, now time.Time) bool {
	return s.LastSignal == sig && now.Sub(s.LastPingAt) < RepeatWindow
}

// FirstDeadline é o prazo a contar de from: o próximo instante esperado mais
// a tolerância. Serve à criação (D12), à retomada e ao sucesso.
func FirstDeadline(sch Schedule, grace time.Duration, from time.Time) time.Time {
	return sch.Next(from).Add(grace)
}

// ValidateGrace confere a tolerância.
func ValidateGrace(grace time.Duration) error {
	if grace < MinGrace || grace > MaxGrace {
		return &ValidationError{Field: "grace_sec", Reason: ReasonOutOfRange}
	}
	return nil
}
