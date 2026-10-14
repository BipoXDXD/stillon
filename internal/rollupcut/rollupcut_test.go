package rollupcut_test

import (
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/rollupcut"
)

func TestAt(t *testing.T) {
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	inicio := h.Add(-24 * time.Hour)
	casos := []struct {
		nome   string
		marca  time.Time
		queria time.Time
	}{
		{"marca no meio da janela: a hora seguinte a ela", h.Add(-3 * time.Hour), h.Add(-2 * time.Hour)},
		{"última hora fechada já consolidada: corte na hora corrente", h.Add(-time.Hour), h},
		{"sem rollup (época Unix): início da janela", time.Unix(0, 0), inicio},
		{"marca logo antes da janela: início da janela", inicio.Add(-2 * time.Hour), inicio},
		{"marca na hora anterior ao início: o próprio início", inicio.Add(-time.Hour), inicio},
		{"marca na hora corrente: nunca passa dela", h, h},
		{"marca no futuro: nunca passa da hora corrente", h.Add(5 * time.Hour), h},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := rollupcut.At(c.marca, inicio, h); !got.Equal(c.queria) {
				t.Errorf("At(%v) = %v, queria %v", c.marca, got, c.queria)
			}
		})
	}
}

func TestAtIsUTC(t *testing.T) {
	sp := time.FixedZone("BRT", -3*3600)
	h := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	got := rollupcut.At(h.Add(-3*time.Hour).In(sp), h.Add(-24*time.Hour), h)
	if got.Location() != time.UTC {
		t.Errorf("corte em %v, queria UTC", got.Location())
	}
}
