package targets

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// HostDaRegra expõe a chave do host da regra de conteúdo (D-T1).
var HostDaRegra = hostDaRegra
