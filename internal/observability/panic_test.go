package observability

import (
	"errors"
	"strings"
	"testing"
)

func TestDescribePanicKeepsOnlyRuntimeMessages(t *testing.T) {
	casos := []struct {
		nome  string
		valor any
		quer  string
	}{
		{"string", "CANARY-9d2e token", "valor string omitido"},
		{"erro comum", errors.New("CANARY-9d2e token"), "valor *errors.errorString omitido"},
		{"runtime", erroDeRuntime(), "index out of range"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := DescreverPanic(c.valor)
			if !strings.Contains(got, c.quer) || strings.Contains(got, "CANARY-9d2e") {
				t.Errorf("DescreverPanic = %q; queria %q e sem o canário", got, c.quer)
			}
		})
	}
}

func erroDeRuntime() (r any) {
	defer func() { r = recover() }()
	var vazio []int
	indice := 1
	_ = vazio[indice]
	return nil
}
