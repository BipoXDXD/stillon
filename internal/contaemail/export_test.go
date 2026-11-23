package contaemail

import "time"

// EncurtarPrazoDreno deixa o teste do encerramento rápido.
func EncurtarPrazoDreno(e *Enviador, d time.Duration) { e.prazoDreno = d }
