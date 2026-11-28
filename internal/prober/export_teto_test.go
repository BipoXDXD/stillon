package prober

import "math"

// SemTetoPorHost tira o teto por host do agendador. É para os testes que
// disparam dezenas de alvos contra o mesmo servidor de teste (um host só) e
// medem outra coisa: o jitter, a trava entre dois sondadores, as vagas do
// semáforo. O teto tem os testes próprios (teto_host_test.go).
func SemTetoPorHost(s *Scheduler) {
	s.teto = novoTetoPorHost(math.MaxInt32, math.MaxInt32)
}
