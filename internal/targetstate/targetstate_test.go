package targetstate_test

import (
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/targetstate"
)

func TestCurrent(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	minuto := time.Minute
	casos := []struct {
		nome    string
		enabled bool
		status  string
		checkEm time.Time
		queria  targetstate.State
	}{
		{"no ar", true, "up", agora.Add(-minuto), targetstate.Up},
		{"lento", true, "degraded", agora.Add(-minuto), targetstate.Degraded},
		{"fora", true, "down", agora.Add(-minuto), targetstate.Down},
		{"desativado", false, "up", agora.Add(-minuto), targetstate.Unknown},
		{"nunca sondado", true, "", time.Unix(0, 0), targetstate.Unknown},
		{"exatamente 3 intervalos", true, "up", agora.Add(-3 * minuto), targetstate.Up},
		{"3 intervalos e 1 s", true, "up", agora.Add(-3*minuto - time.Second), targetstate.Unknown},
		{"status desconhecido", true, "talvez", agora, targetstate.Unknown},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := targetstate.Current(c.enabled, minuto, c.status, c.checkEm, agora); got != c.queria {
				t.Errorf("Current = %q, queria %q", got, c.queria)
			}
		})
	}
}
