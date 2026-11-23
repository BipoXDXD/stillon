package account

import "context"

// SetHash troca a derivação da senha nova da Recuperacao, nos testes: o par
// barato, ou um hasher que conta as chamadas.
func (r *Recuperacao) SetHash(fn func(context.Context, string) (string, error)) {
	r.hashPassword = fn
}
