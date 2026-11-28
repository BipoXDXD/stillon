package statuspage

import "testing"

// A lista reservada vale pelo nome exato e pelos prefixos da marca; o resto
// (inclusive slugs que só contêm ou começam por um nome da lista) é livre.
func TestReservedSlugs(t *testing.T) {
	casos := map[string]bool{
		"admin":          true,
		"www":            true,
		"termos":         true,
		"privacidade":    true,
		"reset-password": true,
		"stillon":        true,
		"stillon-status": true,
		"still-on":       true,
		"paypal":         true,
		"minha-loja":     false,
		"status-da-loja": false,
		"meu-stillon":    false,
		"administradora": false,
	}
	for slug, quer := range casos {
		if got := slugReservado(slug); got != quer {
			t.Errorf("slugReservado(%q) = %v, queria %v", slug, got, quer)
		}
	}
}
