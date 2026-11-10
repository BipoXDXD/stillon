package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

// lerRunID é pura: recebe a query crua e diz o rid, ou que ele é inválido. Só a
// forma canônica de 36 caracteres passa, sem distinção de caixa. Os demais
// parâmetros seguem ignorados, mesmo malformados.
func TestLerRunID(t *testing.T) {
	const valido = "0195f3c2-8a4b-7c1d-9e2f-3a4b5c6d7e8f"
	id := uuid.MustParse(valido)
	for _, c := range []struct {
		nome   string
		query  string
		quer   *uuid.UUID
		aceito bool
	}{
		{"query vazia", "", nil, true},
		{"sem rid", "a=1", nil, true},
		{"rid canônico", "rid=" + valido, &id, true},
		{"rid em maiúsculas", "rid=0195F3C2-8A4B-7C1D-9E2F-3A4B5C6D7E8F", &id, true},
		{"par inválido alheio é ignorado", "x=%zz&rid=" + valido, &id, true},
		{"outro parâmetro antes", "foo=1&rid=" + valido, &id, true},
		{"rid vazio", "rid=", nil, false},
		{"rid sem igual", "rid", nil, false},
		{"rid com chaves", "rid=%7B" + valido + "%7D", nil, false},
		{"rid com chaves literais", "rid={" + valido + "}", nil, false},
		{"rid urn", "rid=urn:uuid:" + valido, nil, false},
		{"rid nulo", "rid=00000000-0000-0000-0000-000000000000", nil, false},
		{"rid de 32 hex", "rid=0195f3c28a4b7c1d9e2f3a4b5c6d7e8f", nil, false},
		{"rid repetido diferente", "rid=a&rid=b", nil, false},
		{"rid válido repetido", "rid=" + valido + "&rid=" + valido, nil, false},
		{"rid com escape inválido", "rid=%zz", nil, false},
		{"rid com NUL", "rid=%00", nil, false},
		{"rid com injeção", "rid=%27%3B%20DROP%20TABLE", nil, false},
		{"rid com chave escapada", "r%69d=x", nil, false},
	} {
		t.Run(c.nome, func(t *testing.T) {
			got, ok := lerRunID(c.query)
			if ok != c.aceito {
				t.Fatalf("lerRunID(%q) aceito = %v, queria %v", c.query, ok, c.aceito)
			}
			switch {
			case c.quer == nil && got != nil:
				t.Errorf("lerRunID(%q) = %v, queria nil", c.query, *got)
			case c.quer != nil && (got == nil || *got != *c.quer):
				t.Errorf("lerRunID(%q) = %v, queria %v", c.query, got, *c.quer)
			}
		})
	}
}
