package config

import (
	"strings"
	"testing"
)

// D-T3: CONTENT_RULES_PER_HOST é o teto de alvos com regra de conteúdo por
// host de destino. Sem a variável, 5; definida, um inteiro de 1 a 100. Fora
// disso o boot recusa, nomeando a variável: zero barraria toda regra, e acima
// de 100 (o teto de alvos por usuário) o valor não teria efeito.
func TestContentRulesPerHost(t *testing.T) {
	cases := []struct {
		valor string
		quer  int
		ok    bool
	}{
		{"", 5, true},
		{"1", 1, true},
		{"100", 100, true},
		{" 7 ", 7, true},
		{"0", 0, false},
		{"101", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"5.0", 0, false},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			if c.valor != "" {
				env["CONTENT_RULES_PER_HOST"] = c.valor
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "CONTENT_RULES_PER_HOST") {
					t.Fatalf("CONTENT_RULES_PER_HOST=%q aceito (err = %v)", c.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CONTENT_RULES_PER_HOST=%q recusado: %v", c.valor, err)
			}
			if cfg.ContentRulesPerHost != c.quer {
				t.Errorf("CONTENT_RULES_PER_HOST=%q: ContentRulesPerHost = %d, queria %d", c.valor, cfg.ContentRulesPerHost, c.quer)
			}
		})
	}
}
