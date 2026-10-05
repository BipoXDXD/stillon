package account

import "context"

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// SetPasswordHasher troca apenas a dependência de derivação, nos testes.
// O teste de saturação continua chamando auth.HashPassword de verdade.
func (s *Service) SetPasswordHasher(fn func(context.Context, string) (string, error)) {
	s.hashPassword = fn
}
