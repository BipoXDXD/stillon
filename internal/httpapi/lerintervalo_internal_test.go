package httpapi

import (
	"net/url"
	"testing"
	"time"
)

// lerIntervalo é pura: relógio fixo e valores esperados escritos à mão. A
// ordem das validações (to, window, from) é contrato: com dois campos
// inválidos, o erro aponta o primeiro dessa ordem.
func TestLerIntervalo(t *testing.T) {
	agora := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		nome     string
		query    string
		from, to time.Time
		invalido string
	}{
		{
			nome:  "sem from nem window: as 24 h antes do agora",
			query: "",
			from:  time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
			to:    agora,
		},
		{
			nome:  "window com to explícito: from = to - janela",
			query: "window=6h&to=2026-09-20T10:00:00Z",
			from:  time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC),
			to:    time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		},
		{
			nome:  "from explícito, to no agora",
			query: "from=2026-09-22T09:00:00Z",
			from:  time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC),
			to:    agora,
		},
		{nome: "to e window inválidos: acusa to", query: "to=ruim&window=2h", invalido: "to"},
		{nome: "to e from inválidos: acusa to", query: "to=ruim&from=ruim", invalido: "to"},
		{nome: "window e from inválidos: acusa window", query: "window=2h&from=ruim", invalido: "window"},
		{nome: "window junto com from válido", query: "window=1h&from=2026-09-22T09:00:00Z", invalido: "window"},
		{nome: "só from inválido", query: "from=ruim", invalido: "from"},
	} {
		t.Run(c.nome, func(t *testing.T) {
			q, err := url.ParseQuery(c.query)
			if err != nil {
				t.Fatal(err)
			}
			from, to, invalido := lerIntervalo(q, agora)
			if invalido != c.invalido || !from.Equal(c.from) || !to.Equal(c.to) {
				t.Errorf("lerIntervalo(%q) = %v, %v, %q; queria %v, %v, %q",
					c.query, from, to, invalido, c.from, c.to, c.invalido)
			}
		})
	}
}
