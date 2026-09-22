// Package crypto reúne as primitivas criptográficas do Sentinela: o tipo
// Secret, o envelope AES-GCM e os hashes de token e de IP.
package crypto

import "log/slog"

const redacted = "[REDACTED]"

// Secret embrulha bytes sensíveis e se recusa a aparecer em qualquer saída
// textual. Cobre os quatro caminhos pelos quais um segredo vaza na prática:
// fmt, encoding/json, log/slog e interpolação em string.
//
// A proteção é do sistema de tipos. Um slog.Info descuidado às três da manhã
// não consegue imprimir o conteúdo — não porque o programador lembrou, mas
// porque o tipo não deixa.
type Secret []byte

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }

// Reveal devolve os bytes em claro. O nome é deliberadamente chamativo: uma
// busca por "Reveal(" enumera todos os pontos de exposição do repositório.
func (s Secret) Reveal() []byte { return []byte(s) }
