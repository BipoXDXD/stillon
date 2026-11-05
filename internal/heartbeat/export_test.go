package heartbeat

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// SetDepoisDeGravar expõe o gancho dos testes da resposta.
func (s *Service) SetDepoisDeGravar(fn func()) { s.depoisDeGravar = fn }
