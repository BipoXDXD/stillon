// Package crypto reúne as primitivas criptográficas do Sentinela: o tipo
// Secret, o envelope AES-GCM e os hashes de token e de IP.
package crypto

import (
	"fmt"
	"io"
	"log/slog"
)

const redacted = "[REDACTED]"

// Secret embrulha bytes sensíveis e fecha os quatro caminhos pelos quais um
// segredo vaza na prática: fmt (qualquer verbo), encoding/json, log/slog e
// interpolação de um valor formatado.
//
// O que o tipo garante: nenhuma função que formate, serialize ou registre o
// valor consegue ver o conteúdo. Um slog.Info descuidado às três da manhã
// imprime [REDACTED], não porque o programador lembrou, mas porque o tipo não
// deixa.
//
// O que o tipo NÃO garante: o tipo-base continua sendo []byte, então
// string(s), s[0] e append(nil, s...) chegam aos bytes sem passar por Reveal.
// A conversão explícita é visível na revisão; o print acidental não era. É
// esse o risco que o tipo remove.
type Secret []byte

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }

// Format cobre a classe inteira de verbos do fmt, e não só os que consultam
// String e GoString. Sem ele, %d, %c e %08b imprimem os bytes do segredo um a
// um — redação por verbo seria uma lista que alguém esqueceria de completar.
func (Secret) Format(f fmt.State, verb rune) { io.WriteString(f, redacted) }

// Reveal devolve os bytes em claro. O nome é deliberadamente chamativo: uma
// busca por "Reveal(" enumera todos os pontos de exposição do repositório.
func (s Secret) Reveal() []byte { return []byte(s) }
