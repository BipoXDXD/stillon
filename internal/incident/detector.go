// Package incident decide quando uma sequência de sondas vira incidente, e
// quando o incidente acaba (spec §7). É função pura: não faz I/O e não lê
// relógio. Quem chama traz o estado do banco e grava o que voltar.
package incident

import "time"

// State é a histerese de um alvo. O zero de StreakSince e LastCheckedAt
// significa "nunca": o alvo ainda não teve check avaliado.
type State struct {
	ConsecutiveFailures  int32
	ConsecutiveSuccesses int32
	// StreakSince é o instante do PRIMEIRO check da sequência atual, de
	// falhas ou de sucessos. É ele que vira started_at ao abrir e
	// resolved_at ao fechar: o alvo caiu quando a primeira falha aconteceu,
	// não quando a terceira confirmou.
	StreakSince   time.Time
	LastCheckedAt time.Time
	// Open diz se o alvo tem incidente aberto agora. Vem do banco, não da
	// contagem: é a linha em incidents que manda.
	Open bool
}

// ProbeResult é o que o detector precisa saber de um check. Só down conta
// como falha: degraded é estado de exibição (spec §7.2, §11.5).
type ProbeResult struct {
	Down bool
	At   time.Time
}

type Action int

const (
	ActionNone Action = iota
	ActionOpen
	ActionClose
)

func (a Action) String() string {
	switch a {
	case ActionOpen:
		return "open"
	case ActionClose:
		return "close"
	default:
		return "none"
	}
}

// Detector guarda os limiares, que são decisão de produto revisável
// (spec §7.2): mudar um número aqui não mexe em mais nada.
type Detector struct {
	OpenAfter  int32 // falhas consecutivas para abrir
	CloseAfter int32 // sucessos consecutivos para fechar
}

// Default é a histerese da spec: rápido para abrir, lento para fechar.
var Default = Detector{OpenAfter: 3, CloseAfter: 5}

// Evaluate aplica um check ao estado e devolve o estado novo e a transição.
//
// Diverge do contrato da spec §7.3 em dois pontos, registrados no plano: os
// contadores são atualizados AQUI, e não por quem chama (assim a regra
// inteira fica num lugar só e testável sem banco), e não há Window, porque
// a histerese por contagem não precisa de janela.
//
// Um check com At igual ou anterior ao último avaliado é ignorado: o estado
// volta intacto e a ação é ActionNone. Isso acontece quando duas sondas do
// mesmo alvo estão em voo e a mais nova grava primeiro (M4 do Plano 2). Sem
// a guarda, um sucesso velho chegando atrasado zeraria uma sequência de
// falhas mais recente.
func (d Detector) Evaluate(s State, r ProbeResult) (State, Action) {
	if !s.LastCheckedAt.IsZero() && !r.At.After(s.LastCheckedAt) {
		return s, ActionNone
	}
	s.LastCheckedAt = r.At

	if r.Down {
		if s.ConsecutiveFailures == 0 {
			s.StreakSince = r.At
		}
		s.ConsecutiveFailures++
		s.ConsecutiveSuccesses = 0
		if !s.Open && s.ConsecutiveFailures >= d.OpenAfter {
			s.Open = true
			return s, ActionOpen
		}
		return s, ActionNone
	}

	if s.ConsecutiveSuccesses == 0 {
		s.StreakSince = r.At
	}
	s.ConsecutiveSuccesses++
	s.ConsecutiveFailures = 0
	if s.Open && s.ConsecutiveSuccesses >= d.CloseAfter {
		s.Open = false
		return s, ActionClose
	}
	return s, ActionNone
}

// Eventos da outbox de notificações (notifications.event).
const (
	EventOpened   = "opened"
	EventResolved = "resolved"
)
