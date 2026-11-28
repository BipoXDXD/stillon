package tenancy

import (
	"errors"
	"strings"
	"testing"
)

func TestParseProjectName(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
		want    string
		wantErr error
	}{
		{"apara as pontas", "  Produção  ", "Produção", nil},
		{"vazio", "", "", ErrProjectNameRequired},
		{"só espaços", "   ", "", ErrProjectNameRequired},
		{"no limite de runas", strings.Repeat("ç", 100), strings.Repeat("ç", 100), nil},
		{"uma runa além", strings.Repeat("ç", 101), "", ErrProjectNameTooLong},
		{"espaço de largura zero", "a\u200bb", "", ErrProjectNameInvalid},
		{"caractere de controle", "a\x00b", "", ErrProjectNameInvalid},
		{"inversão bidi", "a\u202eb", "", ErrProjectNameInvalid},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := ParseProjectName(c.entrada)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("ParseProjectName(%q) erro = %v; quer %v", c.entrada, err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("ParseProjectName(%q) = %q; quer %q", c.entrada, got, c.want)
			}
		})
	}
}
