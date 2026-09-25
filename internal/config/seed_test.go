package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// D1 (Plano 10, §3.4): o seed do compose cria o único usuário com a senha de
// demonstração, que está no repositório. Num servidor, esse passa a ser O
// usuário (banco S1 da revisão). O seed.sh só roda com PUBLIC_BASE_URL http
// num host de loopback, a mesma regra que o boot usa para aceitar as chaves
// de demo (hostLocal), e recusa ANTES de chamar o binário.
//
// O /vigilume é trocado por um falso (VIGILUME_BIN) que anota a chamada e sai
// 3 ("já existe usuário"): com a URL local o seed segue e sai 0; com a URL de
// servidor ele sai 1, avisa, e o falso nunca é chamado.
func TestSeedRefusesOutsideLoopback(t *testing.T) {
	dir := t.TempDir()
	registro := filepath.Join(dir, "chamadas")
	falso := filepath.Join(dir, "vigilume")
	script := "#!/bin/sh\necho \"$*\" >> \"$REGISTRO\"\ncat >/dev/null\nexit 3\n"
	if err := os.WriteFile(falso, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		url   string
		local bool
	}{
		{"http://localhost:8080", true},
		{"http://localhost", true},
		{"http://127.0.0.1:18080/", true},
		{"http://127.1.2.3:8080", true},
		{"http://[::1]:8080", true},
		{"HTTP://LocalHost:8080", true},
		{"", false},
		{"https://localhost:8080", false},
		{"https://vigilume.exemplo.com.br", false},
		{"http://vigilume.exemplo.com.br", false},
		{"http://203.0.113.7:8080", false},
		{"http://10.0.0.5:8080", false},
		{"http://localhost.exemplo.com:8080", false},
		{"http://127.0.0.1.nip.io:8080", false},
		{"http://localhost@exemplo.com", false},
		{"http://127.0.0.1:8080@exemplo.com", false},
		{"http://[::1].exemplo.com", false},
		{"ftp://localhost", false},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			_ = os.Remove(registro)
			cmd := exec.Command("sh", "../../deploy/compose/seed.sh")
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"VIGILUME_BIN=" + falso,
				"REGISTRO=" + registro,
				"DEMO_EMAIL=demo@vigilume.local",
				"DEMO_PASSWORD=demo-vigilume-2026",
			}
			if c.url != "" {
				cmd.Env = append(cmd.Env, "PUBLIC_BASE_URL="+c.url)
			}
			saida, err := cmd.CombinedOutput()
			_, errReg := os.Stat(registro)
			chamou := errReg == nil

			if c.local {
				if err != nil || !chamou {
					t.Fatalf("PUBLIC_BASE_URL=%q: seed saiu com %v (chamou o binário: %v), queria seguir e sair 0\n%s", c.url, err, chamou, saida)
				}
				return
			}
			var sair *exec.ExitError
			if err == nil || !errors.As(err, &sair) || sair.ExitCode() != 1 {
				t.Errorf("PUBLIC_BASE_URL=%q: seed saiu com %v, queria código 1", c.url, err)
			}
			if chamou {
				t.Errorf("PUBLIC_BASE_URL=%q: o seed chamou o binário (criaria o usuário de demonstração)", c.url)
			}
			if !strings.Contains(string(saida), "só roda na máquina local") {
				t.Errorf("PUBLIC_BASE_URL=%q: a recusa não diz o motivo:\n%s", c.url, saida)
			}
		})
	}
}
