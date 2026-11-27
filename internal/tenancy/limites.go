package tenancy

// Limites são as cotas do titular de um projeto (spec §Cotas e abuso). Valem
// para o titular, nunca para quem age: o membro que cria um alvo usa os
// limites de quem é dono do projeto.
type Limites struct {
	Projetos           int
	MembrosPorProjeto  int   // além do titular
	Alvos              int64 // somando os projetos do titular
	Heartbeats         int64 // idem
	CanaisPorProjeto   int64
	IntervaloMinimoSec int32
}

// LimitesDe devolve as cotas do plano grátis ou as do operador, que fica sem
// as cotas do plano e com os tetos de administração. Devolve um literal a
// cada chamada: não há estado mutável a proteger.
func LimitesDe(isOperator bool) Limites {
	switch {
	case isOperator:
		return Limites{
			Projetos: 100, MembrosPorProjeto: 100, Alvos: 100, Heartbeats: 100,
			CanaisPorProjeto: 10, IntervaloMinimoSec: 30,
		}
	default:
		return Limites{
			Projetos: 3, MembrosPorProjeto: 3, Alvos: 20, Heartbeats: 20,
			CanaisPorProjeto: 5, IntervaloMinimoSec: 300,
		}
	}
}
