package notify

import "time"

// ritmo cria o tique periódico de um laço Run e devolve o canal dos tiques e
// a função que o para. É campo do Dispatcher e dos Reminders para que os
// testes entreguem os tiques à mão, sem esperar o período real; a produção
// usa ritmoReal.
type ritmo func(periodo time.Duration) (tiques <-chan time.Time, parar func())

func ritmoReal(periodo time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(periodo)
	return t.C, t.Stop
}
