package config

import (
	"runtime"
	"strings"
	"testing"
)

// A1 (Plano 10, §3.1): ARGON2_MAX_CONCURRENT é o teto de Argon2id simultâneos.
// Sem a variável, min(NumCPU, 4); definida, um inteiro de 1 a 64. Fora disso o
// boot recusa, nomeando a variável: um teto de zero trancaria todo login, e um
// teto enorme devolveria o OOM que o semáforo existe para impedir.
func TestArgon2MaxConcurrent(t *testing.T) {
	cases := []struct {
		valor string
		quer  int
		ok    bool
	}{
		{"", min(runtime.NumCPU(), 4), true},
		{"1", 1, true},
		{" 3 ", 3, true},
		{"64", 64, true},
		{"0", 0, false},
		{"65", 0, false},
		{"-2", 0, false},
		{"dois", 0, false},
		{"2.5", 0, false},
	}
	for _, c := range cases {
		t.Run(c.valor, func(t *testing.T) {
			env := validEnv()
			if c.valor != "" {
				env["ARGON2_MAX_CONCURRENT"] = c.valor
			}
			cfg, err := Load(getenvFrom(env))
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "ARGON2_MAX_CONCURRENT") {
					t.Fatalf("ARGON2_MAX_CONCURRENT=%q aceito (err = %v)", c.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ARGON2_MAX_CONCURRENT=%q recusado: %v", c.valor, err)
			}
			if cfg.Argon2MaxConcurrent != c.quer {
				t.Errorf("ARGON2_MAX_CONCURRENT=%q: Argon2MaxConcurrent = %d, queria %d", c.valor, cfg.Argon2MaxConcurrent, c.quer)
			}
		})
	}
}
