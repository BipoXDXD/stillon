package rotulo

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limites das tags (spec do painel denso). MaxTags vale por monitor e também
// para o filtro, em que todas as tags pedidas têm de estar no monitor: pedir
// mais do que ele pode ter nunca acharia nada. maxTagRunes tem par no CHECK das
// migrations 00017 e 00021.
const (
	MaxTags     = 8
	maxTagRunes = 24
)

// Motivos de recusa, no vocabulário fechado da API.
const (
	MotivoTagInvalida = "invalid"
	MotivoTagLonga    = "too_long"
	MotivoTagsDemais  = "out_of_range"
)

// ParseTag devolve a tag na forma canônica, ou o motivo da recusa. A
// allowlist barra separador, controle e caractere invisível de uma vez.
// unicode.IsLetter não aceita o acento combinante (categoria Mn), então uma
// palavra acentuada tem uma grafia só: a forma composta.
func ParseTag(cru string) (string, string) {
	tag := strings.ToLower(strings.TrimSpace(cru))
	switch {
	case tag == "" || !utf8.ValidString(tag):
		return "", MotivoTagInvalida
	case utf8.RuneCountInString(tag) > maxTagRunes:
		return "", MotivoTagLonga
	case strings.ContainsFunc(tag, foraDaAllowlist):
		return "", MotivoTagInvalida
	}
	return tag, ""
}

func foraDaAllowlist(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
}

// NormalizeTags devolve as tags canônicas, ordenadas e sem repetição; nunca
// nil, para o JSON sair [] e não null. O teto conta o que sobra depois de
// deduplicar.
func NormalizeTags(cruas []string) ([]string, string) {
	tags := make([]string, 0, len(cruas))
	for _, cru := range cruas {
		tag, motivo := ParseTag(cru)
		if motivo != "" {
			return nil, motivo
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	tags = slices.Compact(tags)
	if len(tags) > MaxTags {
		return nil, MotivoTagsDemais
	}
	return tags, ""
}
