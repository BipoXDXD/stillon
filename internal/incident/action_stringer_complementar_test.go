package incident_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/incident"
)

// Stringer representa a ação para diagnóstico textual. Os tokens aqui não
// são os eventos da outbox (opened/resolved), e o teste não altera transições.
func TestActionsFromDetectorHaveStableDiagnosticTextComplementar(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		name string
		down bool
		text string
	}{
		{"primeira_falha", true, "none"},
		{"segunda_falha", true, "none"},
		{"terceira_falha_abre", true, "open"},
		{"primeiro_sucesso", false, "none"},
		{"segundo_sucesso", false, "none"},
		{"terceiro_sucesso", false, "none"},
		{"quarto_sucesso", false, "none"},
		{"quinto_sucesso_fecha", false, "close"},
	}
	state := incident.State{}
	for index, step := range steps {
		var action incident.Action
		state, action = incident.Default.Evaluate(state, incident.ProbeResult{Down: step.down, At: start.Add(time.Duration(index) * time.Minute)})
		t.Run(step.name, func(t *testing.T) {
			if got := fmt.Sprint(action); got != step.text {
				t.Fatalf("representação diagnóstica = %q, queria %q", got, step.text)
			}
		})
	}
	t.Run("acao_zero", func(t *testing.T) {
		var action incident.Action
		if got := fmt.Sprint(action); got != "none" {
			t.Fatalf("ação zero = %q, queria none", got)
		}
	})
}
