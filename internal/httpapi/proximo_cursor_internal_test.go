package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

type itemComID struct{ ID uuid.UUID }

func idDoItem(i itemComID) uuid.UUID { return i.ID }

// proximoCursor só emite cursor com a página cheia: o id do último item.
func TestProximoCursor(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	b := uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	cheia := []itemComID{{a}, {b}}

	casos := []struct {
		nome   string
		items  []itemComID
		limite int32
		quer   string
	}{
		{"nil", nil, 2, ""},
		{"vazia", []itemComID{}, 2, ""},
		{"incompleta", cheia[:1], 2, ""},
		{"exatamente o limite", cheia, 2, b.String()},
		{"limite 1 com um item", cheia[:1], 1, a.String()},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := proximoCursor(c.items, c.limite, idDoItem); got != c.quer {
				t.Errorf("proximoCursor() = %q, queria %q", got, c.quer)
			}
		})
	}
}
