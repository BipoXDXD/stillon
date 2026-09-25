package prober

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Um Spec no log mostra os nomes dos headers e nunca os valores (Plano 8),
// nos dois formatos que o projeto usa (JSON em produção, texto em
// desenvolvimento).
func TestSpecLogValueHidesHeaderValues(t *testing.T) {
	const canario = "Bearer canario-que-nao-pode-vazar"
	spec := Spec{
		URL: "https://alvo.example/saude", Method: "GET", Timeout: 5 * time.Second, ExpectedStatus: 200,
		Headers: map[string]string{"Authorization": canario, "X-Api-Key": "outro-" + canario},
	}
	for nome, novo := range map[string]func(*bytes.Buffer) slog.Handler{
		"json":  func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		"texto": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(novo(&buf)).Info("sonda", "spec", spec)
		saida := buf.String()
		if strings.Contains(saida, "canario") {
			t.Errorf("%s: o valor de um header foi para o log: %s", nome, saida)
		}
		// A perna positiva: o log continua útil, com a URL e os nomes.
		for _, quer := range []string{"https://alvo.example/saude", "Authorization", "X-Api-Key"} {
			if !strings.Contains(saida, quer) {
				t.Errorf("%s: o log não traz %q: %s", nome, quer, saida)
			}
		}
	}
}
