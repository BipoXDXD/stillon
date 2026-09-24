package incident

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// rodar aplica uma sequência de checks, um por minuto a partir de t0, e
// devolve o estado final e a ação de cada passo. "d" é down, "u" é up.
func rodar(d Detector, s State, seq string) (State, []Action) {
	acoes := make([]Action, 0, len(seq))
	for i, c := range seq {
		var a Action
		s, a = d.Evaluate(s, ProbeResult{Down: c == 'd', At: t0.Add(time.Duration(i) * time.Minute)})
		acoes = append(acoes, a)
	}
	return s, acoes
}

// minuto devolve o instante do i-ésimo check de rodar.
func minuto(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }

func TestEvaluateHysteresis(t *testing.T) {
	cases := []struct {
		nome     string
		seq      string
		abreEm   int // índice do check que abre; -1 se nenhum
		fechaEm  int // índice do check que fecha; -1 se nenhum
		aberto   bool
		desdeIdx int // índice cujo instante deve estar em StreakSince
	}{
		{"duas falhas não abrem", "dd", -1, -1, false, 0},
		{"a terceira falha abre", "ddd", 2, -1, true, 0},
		{"um sucesso no meio recomeça a contagem", "ddudd", -1, -1, false, 3},
		{"falhas além da terceira não reabrem", "dddddd", 2, -1, true, 0},
		{"quatro sucessos não fecham", "ddduuuu", 2, -1, true, 3},
		{"o quinto sucesso fecha", "ddduuuuu", 2, 7, false, 3},
		{"uma falha no meio recomeça os sucessos", "ddduuuuduuuuu", 2, 12, false, 8},
		{"sucessos sem incidente não fecham nada", "uuuuuu", -1, -1, false, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s, acoes := rodar(Default, State{}, c.seq)
			for i, a := range acoes {
				quer := ActionNone
				switch i {
				case c.abreEm:
					quer = ActionOpen
				case c.fechaEm:
					quer = ActionClose
				}
				if a != quer {
					t.Errorf("check %d (%c): ação %v, queria %v", i, c.seq[i], a, quer)
				}
			}
			if s.Open != c.aberto {
				t.Errorf("Open = %v, queria %v", s.Open, c.aberto)
			}
			if !s.StreakSince.Equal(minuto(c.desdeIdx)) {
				t.Errorf("StreakSince = %v, queria o check %d (%v)", s.StreakSince, c.desdeIdx, minuto(c.desdeIdx))
			}
			if !s.LastCheckedAt.Equal(minuto(len(c.seq) - 1)) {
				t.Errorf("LastCheckedAt = %v, queria o último check", s.LastCheckedAt)
			}
		})
	}
}

// started_at e resolved_at saem de StreakSince no instante da transição: o
// alvo caiu na PRIMEIRA falha e voltou no PRIMEIRO sucesso.
func TestTransitionCarriesTheStartOfTheStreak(t *testing.T) {
	s, _ := rodar(Default, State{}, "uddd")
	if !s.Open || !s.StreakSince.Equal(minuto(1)) {
		t.Errorf("ao abrir, StreakSince = %v, queria a primeira falha %v", s.StreakSince, minuto(1))
	}
	s, _ = rodar(Default, State{}, "ddduuduuuuu")
	if s.Open || !s.StreakSince.Equal(minuto(6)) {
		t.Errorf("ao fechar, StreakSince = %v, queria o primeiro sucesso da sequência final %v", s.StreakSince, minuto(6))
	}
}

// M4 do Plano 2: duas sondas do mesmo alvo em voo, gravando fora de ordem.
// Um sucesso VELHO não pode zerar uma sequência de falhas mais nova.
func TestOutOfOrderCheckIsIgnored(t *testing.T) {
	s, _ := rodar(Default, State{}, "dd") // falhas em t0 e t0+1min

	velho := ProbeResult{Down: false, At: minuto(1).Add(-30 * time.Second)}
	depois, a := Default.Evaluate(s, velho)
	if a != ActionNone || depois != s {
		t.Fatalf("check velho mudou o estado: %+v -> %+v (ação %v)", s, depois, a)
	}
	igual := ProbeResult{Down: false, At: minuto(1)}
	if depois, a = Default.Evaluate(s, igual); a != ActionNone || depois != s {
		t.Fatalf("check com o MESMO instante do último mudou o estado: %+v", depois)
	}

	// A terceira falha, mais nova, abre: a sequência sobreviveu ao atraso.
	if _, a = Default.Evaluate(s, ProbeResult{Down: true, At: minuto(2)}); a != ActionOpen {
		t.Errorf("a terceira falha depois do check velho deu %v, queria open", a)
	}
}

// Os limiares são campos, não constantes (spec §7.2).
func TestThresholdsAreConfigurable(t *testing.T) {
	d := Detector{OpenAfter: 1, CloseAfter: 2}
	_, acoes := rodar(d, State{}, "duu")
	if acoes[0] != ActionOpen || acoes[1] != ActionNone || acoes[2] != ActionClose {
		t.Errorf("com 1/2, ações = %v", acoes)
	}
}

// Quem chama traz Open do banco. Um estado que já chega aberto não reabre.
func TestOpenComesFromTheCaller(t *testing.T) {
	s := State{Open: true}
	_, acoes := rodar(Default, s, "ddd")
	for i, a := range acoes {
		if a != ActionNone {
			t.Errorf("check %d reabriu um incidente já aberto: %v", i, a)
		}
	}
}
