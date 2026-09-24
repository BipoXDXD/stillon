package account

import "github.com/BipoXDXD/vigilume/internal/auth"

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// SetUsers troca o UserStore, para o teste que precisa de uma falha de banco
// no meio da troca de senha.
func (s *Service) SetUsers(u auth.UserStore) { s.users = u }
