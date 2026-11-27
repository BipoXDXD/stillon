package tenancy

import "testing"

func TestFreePlanLimits(t *testing.T) {
	want := Limites{
		Projetos: 3, MembrosPorProjeto: 3, Alvos: 20, Heartbeats: 20,
		CanaisPorProjeto: 5, IntervaloMinimoSec: 300,
	}
	if got := LimitesDe(false); got != want {
		t.Errorf("LimitesDe(false) = %+v; quer %+v", got, want)
	}
}

func TestOperatorLimits(t *testing.T) {
	want := Limites{
		Projetos: 100, MembrosPorProjeto: 100, Alvos: 100, Heartbeats: 100,
		CanaisPorProjeto: 10, IntervaloMinimoSec: 30,
	}
	if got := LimitesDe(true); got != want {
		t.Errorf("LimitesDe(true) = %+v; quer %+v", got, want)
	}
}
