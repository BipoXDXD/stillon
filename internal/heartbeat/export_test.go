package heartbeat

import "time"

// SetDepoisDeContar expõe o gancho do teste de criação concorrente.
func (s *Service) SetDepoisDeContar(fn func()) { s.depoisDeContar = fn }

// CloneName expõe o nome da cópia ao teste puro.
var CloneName = cloneName

// SetDepoisDeGravar expõe o gancho dos testes da resposta.
func (s *Service) SetDepoisDeGravar(fn func()) { s.depoisDeGravar = fn }

// StatusOf e StatusFacts expõem o estado derivado aos testes de fronteira.
var StatusOf = statusOf

type StatusFacts = statusFacts

// InGrace expõe a janela de tolerância sozinha, sem a ordem do switch de
// statusOf.
func (f statusFacts) InGrace(now time.Time) bool { return f.inGrace(now) }
