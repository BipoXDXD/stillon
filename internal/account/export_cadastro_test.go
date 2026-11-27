package account

import "context"

// SetHash troca a derivação da senha do cadastro, nos testes: um hasher falso
// conta as chamadas e simula a demora do Argon2id sem disputar o semáforo.
func (c *Cadastro) SetHash(fn func(context.Context, string) (string, error)) {
	c.hash = fn
}
