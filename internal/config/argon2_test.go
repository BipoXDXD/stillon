package config

import (
	"runtime"
	"strings"
	"testing"
)

// ARGON2_MAX_CONCURRENT sobrescreve o teto padrão com um inteiro de 1 a 64.
func TestArgon2MaxConcurrent(t *testing.T) {
	anterior := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(anterior) })

	cases := []struct {
		valor string
		quer  int
		ok    bool
	}{
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
			env["ARGON2_MAX_CONCURRENT"] = c.valor
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

func TestArgon2MaxConcurrentDefaultsToGOMAXPROCS(t *testing.T) {
	anterior := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(anterior) })
	for _, c := range []struct{ procs, quer int }{{1, 1}, {5, 4}} {
		runtime.GOMAXPROCS(c.procs)
		cfg, err := Load(getenvFrom(validEnv()))
		if err != nil {
			t.Fatalf("GOMAXPROCS=%d: Load falhou: %v", c.procs, err)
		}
		if cfg.Argon2MaxConcurrent != c.quer {
			t.Errorf("GOMAXPROCS=%d: Argon2MaxConcurrent = %d, queria %d", c.procs, cfg.Argon2MaxConcurrent, c.quer)
		}
	}
}
