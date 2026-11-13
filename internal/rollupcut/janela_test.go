package rollupcut_test

import (
	"errors"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/rollupcut"
)

func TestNovaJanela(t *testing.T) {
	desde := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)
	agora := time.Date(2026, 9, 22, 12, 20, 0, 0, time.UTC)
	casos := []struct {
		nome              string
		desde, ate, agora time.Time
		valida            bool
	}{
		{"desde antes de ate", desde, desde.Add(72 * time.Hour), agora, true},
		{"ate um nanossegundo depois: o menor intervalo semiaberto", desde, desde.Add(time.Nanosecond), agora, true},
		{"desde igual a ate: intervalo vazio", desde, desde, agora, false},
		{"desde depois de ate: invertida", desde, desde.Add(-time.Hour), agora, false},
		{"desde zero", time.Time{}, desde, agora, false},
		{"ate zero", desde, time.Time{}, agora, false},
		{"agora zero", desde, desde.Add(time.Hour), time.Time{}, false},
		{"tudo zero", time.Time{}, time.Time{}, time.Time{}, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := rollupcut.NovaJanela(c.desde, c.ate, c.agora)
			if c.valida && err != nil {
				t.Fatalf("NovaJanela devolveu %v, queria janela válida", err)
			}
			if !c.valida && !errors.Is(err, rollupcut.ErrJanelaInvalida) {
				t.Fatalf("NovaJanela devolveu %v, queria ErrJanelaInvalida", err)
			}
		})
	}
}

func TestJanelaDerivaInicioHoraECorte(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	desde := time.Date(2026, 9, 19, 7, 30, 0, 0, brt) // 10:30 UTC
	ate := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	agora := time.Date(2026, 9, 22, 9, 20, 0, 0, brt) // 12:20 UTC

	j, err := rollupcut.NovaJanela(desde, ate, agora)
	if err != nil {
		t.Fatal(err)
	}
	if got, queria := j.Desde(), time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC); !got.Equal(queria) || got.Location() != time.UTC {
		t.Errorf("Desde() = %v, queria %v em UTC (a hora que contém o início)", got, queria)
	}
	if got := j.Ate(); !got.Equal(ate) {
		t.Errorf("Ate() = %v, queria %v", got, ate)
	}
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if got := j.Hora(); !got.Equal(h) || got.Location() != time.UTC {
		t.Errorf("Hora() = %v, queria %v em UTC", got, h)
	}
	marca := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	if got, queria := j.Corte(marca), time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC); !got.Equal(queria) {
		t.Errorf("Corte(%v) = %v, queria %v", marca, got, queria)
	}
	// Sem rollup (época Unix) o corte cai no piso: a janela de 24 h antes da hora corrente.
	if got, queria := j.Corte(time.Unix(0, 0)), h.Add(-rollupcut.MaxRawWindow); !got.Equal(queria) {
		t.Errorf("Corte(época) = %v, queria %v", got, queria)
	}
}
