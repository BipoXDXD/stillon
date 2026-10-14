package account

import "context"

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// SetPasswordHasher troca apenas a dependência de derivação, nos testes.
// O teste de saturação continua chamando auth.HashPassword de verdade.
func (s *Service) SetPasswordHasher(fn func(context.Context, string) (string, error)) {
	s.hashPassword = fn
}

// SetPasswordVerifier troca a verificação da senha atual, nos testes. Anda
// junto com SetPasswordHasher: o verificador precisa entender o hash que o
// hasher grava.
func (s *Service) SetPasswordVerifier(fn func(ctx context.Context, encoded, plain string) (ok, needsRehash bool, err error)) {
	s.verifyPassword = fn
}
