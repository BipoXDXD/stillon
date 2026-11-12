package prober

import (
	"errors"
	"fmt"
	"testing"
)

// fonteSemFim entrega sempre o que lhe pedem e conta o que saiu: a prova do
// teto é pelo que foi lido da fonte, e não só pelo que o leitor devolveu.
type fonteSemFim struct{ lidos int }

func (f *fonteSemFim) Read(p []byte) (int, error) {
	f.lidos += len(p)
	return len(p), nil
}

// O teto de bytes comprimidos vale ao byte, qualquer que seja o tamanho do
// buffer de quem lê: o último Read corta o buffer em vez de passar do teto. Os
// testes de sonda leem em blocos que dividem o teto, e por isso nunca chegam
// ao corte de um Read parcial.
func TestCompressedLimitReaderStopsExactlyAtTheLimit(t *testing.T) {
	for _, tamanho := range []int{1, 3000, 4096, tetoComprimido - 1, tetoComprimido, tetoComprimido + 1, 3 * tetoComprimido} {
		t.Run(fmt.Sprintf("buffer de %d", tamanho), func(t *testing.T) {
			fonte := &fonteSemFim{}
			leitor := comTetoComprimido(fonte)
			buf := make([]byte, tamanho)

			entregues := 0
			var err error
			for err == nil {
				var n int
				n, err = leitor.Read(buf)
				entregues += n
			}

			if !errors.Is(err, errTetoComprimido) {
				t.Fatalf("err = %v, queria errTetoComprimido", err)
			}
			if entregues != tetoComprimido || fonte.lidos != tetoComprimido {
				t.Errorf("entregues %d, lidos da fonte %d; queria %d nos dois", entregues, fonte.lidos, tetoComprimido)
			}
			if n, err := leitor.Read(buf); n != 0 || !errors.Is(err, errTetoComprimido) {
				t.Errorf("Read depois do teto = (%d, %v), queria (0, errTetoComprimido)", n, err)
			}
			if fonte.lidos != tetoComprimido {
				t.Errorf("a fonte foi lida de novo depois do teto: %d bytes", fonte.lidos)
			}
		})
	}
}
