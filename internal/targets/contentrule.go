package targets

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ContentMode é como a regra de conteúdo julga o corpo (spec §5.3).
type ContentMode string

const (
	// ContentContains passa quando o valor aparece no corpo, em bytes UTF-8.
	ContentContains ContentMode = "contains"
	// ContentNotContains passa quando o valor não aparece e o corpo coube no
	// limite de leitura.
	ContentNotContains ContentMode = "not_contains"
	// ContentJSONEquals passa quando o escalar em JSONPointer tem a forma de
	// texto Value.
	ContentJSONEquals ContentMode = "json_equals"
)

// ContentRule é a regra de conteúdo de um alvo. JSONPointer só existe em
// ContentJSONEquals; nos outros modos fica vazio.
type ContentRule struct {
	Mode        ContentMode
	Value       string
	JSONPointer string
}

// Limites da regra (spec §7.2), em caracteres, não em bytes.
const (
	maxContentValueRunes = 256
	maxJSONPointerRunes  = 256
)

var errPonteiroInvalido = errors.New("ponteiro JSON inválido")

// ParseJSONPointer lê um ponteiro RFC 6901 e devolve os tokens já
// decodificados (~1 vira "/", ~0 vira "~"). O ponteiro vazio, que na RFC
// aponta o documento inteiro, é recusado: a regra compara um escalar, e o
// documento inteiro nunca é um.
func ParseJSONPointer(p string) ([]string, error) {
	resto, ok := strings.CutPrefix(p, "/")
	if !ok {
		return nil, errPonteiroInvalido
	}
	partes := strings.Split(resto, "/")
	tokens := make([]string, 0, len(partes))
	for _, parte := range partes {
		token, err := decodificarToken(parte)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

// decodificarToken lê cada escape uma vez só, da esquerda para a direita:
// "~01" vira "~1" e não "/", o que uma troca em cadeia de strings.Replace
// erraria.
func decodificarToken(s string) (string, error) {
	if !strings.Contains(s, "~") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 == len(s) {
			return "", errPonteiroInvalido
		}
		i++
		switch s[i] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", errPonteiroInvalido
		}
	}
	return b.String(), nil
}

// validateContentRule confere a regra contra o resto do alvo já normalizado:
// método em maiúscula e headers canônicos.
func validateContentRule(f *fields, v *ValidationError) {
	r := f.ContentRule
	if r == nil {
		return
	}
	modoValido := r.Mode == ContentContains || r.Mode == ContentNotContains || r.Mode == ContentJSONEquals
	switch {
	case r.Mode == "":
		// "mode": null ou ausente na API: obrigatório, como o valor vazio.
		v.add("content_rule.mode", ReasonRequired)
	case !modoValido:
		v.add("content_rule.mode", ReasonInvalid)
	}
	if motivo := checkContentText(r.Value, maxContentValueRunes); motivo != "" {
		v.add("content_rule.value", motivo)
	}
	if modoValido {
		if motivo := checkJSONPointer(r.Mode, r.JSONPointer); motivo != "" {
			v.add("content_rule.json_pointer", motivo)
		}
	}
	// HEAD não traz corpo, e a regra julgaria outra coisa. Com regra, o
	// Accept-Encoding é da sonda, que só lê gzip e o descomprime ela mesma; o
	// do dono seria ignorado, e recusá-lo deixa isso claro no cadastro. Com o
	// Range do dono, a regra julgaria só uma fatia, e a fatia de um gzip nem
	// abre.
	if f.Method == http.MethodHead || temHeader(f.Headers, "Accept-Encoding") || temHeader(f.Headers, "Range") {
		v.add("content_rule", ReasonForbidden)
	}
}

func checkJSONPointer(modo ContentMode, p string) string {
	if modo != ContentJSONEquals {
		if p != "" {
			return ReasonInvalid
		}
		return ""
	}
	if p == "" {
		return ReasonRequired
	}
	if motivo := checkContentText(p, maxJSONPointerRunes); motivo != "" {
		return motivo
	}
	if _, err := ParseJSONPointer(p); err != nil {
		return ReasonInvalid
	}
	return ""
}

// checkContentText aplica a regra comum às strings da regra: de 1 a max
// caracteres, UTF-8 válido, sem NUL nem controle (tab e quebra de linha
// inclusive, spec §7.2).
func checkContentText(s string, max int) string {
	switch {
	case s == "":
		return ReasonRequired
	case utf8.RuneCountInString(s) > max:
		return ReasonTooLong
	case !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0:
		return ReasonInvalid
	}
	return ""
}

// temHeader olha os nomes sem diferença de caixa: se normalizeHeaders recusou
// os headers, eles ainda estão como o dono os escreveu.
func temHeader(h map[string]string, procurado string) bool {
	for nome := range h {
		if strings.EqualFold(nome, procurado) {
			return true
		}
	}
	return false
}

// Equal compara duas regras, com nil igual só a nil. O fencing do sondador a
// usa para saber se a regra mudou com a sonda em voo.
func (r *ContentRule) Equal(o *ContentRule) bool {
	if r == nil || o == nil {
		return r == o
	}
	return *r == *o
}

// RuleFromColumns monta a regra a partir das três colunas de targets, para o
// cadastro e para o sondador. Modo nulo é alvo sem regra; o CHECK
// targets_content_rule_shape garante que o valor existe quando o modo existe,
// e que o ponteiro só existe em json_equals.
func RuleFromColumns(mode, value, pointer *string) *ContentRule {
	if mode == nil {
		return nil
	}
	r := &ContentRule{Mode: ContentMode(*mode)}
	if value != nil {
		r.Value = *value
	}
	if pointer != nil {
		r.JSONPointer = *pointer
	}
	return r
}

// colunasDaRegra é o inverso de RuleFromColumns: sem regra, as três colunas
// nulas; ponteiro vazio vira nulo, como o CHECK exige fora de json_equals.
func colunasDaRegra(r *ContentRule) (mode, value, pointer *string) {
	if r == nil {
		return nil, nil, nil
	}
	m, v := string(r.Mode), r.Value
	if r.JSONPointer != "" {
		p := r.JSONPointer
		pointer = &p
	}
	return &m, &v, pointer
}
