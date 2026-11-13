package prober

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// leitorQueFalha devolve o erro na primeira leitura, como uma conexão
// resetada antes de qualquer byte do corpo.
type leitorQueFalha struct{ err error }

func (l leitorQueFalha) Read([]byte) (int, error) { return 0, l.err }

// Erro de leitura antes do primeiro byte não é corpo vazio: sai como erro, e
// a sonda cai em down por erro de rede em vez de julgar a regra.
func TestLerCorpoDaRegraErroNoPrimeiroByte(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(leitorQueFalha{err: io.ErrUnexpectedEOF}),
	}
	corpo, cortado, ilegivel, err := lerCorpoDaRegra(resp)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, queria io.ErrUnexpectedEOF", err)
	}
	if corpo != nil || ilegivel || cortado {
		t.Errorf("corpo %q, cortado %v, ilegivel %v; queria nil, false, false", corpo, cortado, ilegivel)
	}
}

// Um gzip que acaba dentro do cabeçalho é erro de leitura (a rede cortou o
// corpo), e não "declara gzip e não é": o segundo vira content_encoding, o
// primeiro vira down por erro de rede.
func TestLerCorpoDaRegraGzipTruncadoNoCabecalho(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(strings.NewReader("\x1f\x8b\x08")),
	}
	corpo, cortado, ilegivel, err := lerCorpoDaRegra(resp)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, queria io.ErrUnexpectedEOF", err)
	}
	if corpo != nil || ilegivel || cortado {
		t.Errorf("corpo %q, cortado %v, ilegivel %v; queria nil, false, false", corpo, cortado, ilegivel)
	}
}

// O JSON real escapa com hexa maiúsculo (U+00E9 sai como barra, u, 00E9, e o
// emoji sai como um par substituto); a chave escapada tem de casar com o
// ponteiro já decodificado, e só com ele.
func TestAvaliarConteudoChaveEscapadaComHexaMaiusculo(t *testing.T) {
	const u = "\x5cu" // o \u do JSON, sem depender de como o editor trata a sequência
	chave := func(escapes string) string { return `{"` + escapes + `":1}` }
	json := func(ponteiro string) RegraDaSonda {
		return RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("1"), Pointer: []string{ponteiro}}
	}
	casos := []struct {
		nome  string
		regra RegraDaSonda
		corpo string
		quer  ErrorKind
	}{
		{"e agudo com E maiúsculo", json("\u00e9"), chave(u + "00E9"), ""},
		{"E agudo com C maiúsculo", json("\u00c9"), chave(u + "00C9"), ""},
		{"par substituto maiúsculo", json("\U0001F600"), chave(u + "D83D" + u + "DE00"), ""},
		{"par substituto com hexa misturado", json("\U0001F600"), chave(u + "D83d" + u + "DE00"), ""},
		{"sem acento não casa", json("e"), chave(u + "00E9"), KindContentMismatch},
		{"maiúscula não casa com minúscula", json("\u00c9"), chave(u + "00E9"), KindContentMismatch},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := avaliarConteudo(c.regra, []byte(c.corpo), false); got != c.quer {
				t.Errorf("avaliarConteudo = %q, queria %q", got, c.quer)
			}
		})
	}
}
