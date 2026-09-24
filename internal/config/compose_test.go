package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"testing"
)

// A recusa das chaves de demonstração (D14 do Plano 7) só protege se as
// chaves do compose.yaml forem ESTAS. Trocar uma chave no compose sem trocar
// aqui deixaria o compose copiado para produção subir com uma chave pública.
func TestComposeUsesTheDemoKeys(t *testing.T) {
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
		m := regexp.MustCompile(`(?m)^\s+` + nome + `:\s+(\S+)`).FindSubmatch(compose)
		if m == nil {
			t.Errorf("%s ausente do compose.yaml", nome)
			continue
		}
		chave, err := base64.StdEncoding.DecodeString(string(m[1]))
		if err != nil || !bytes.Equal(chave, chavesDeDemo[i]) {
			t.Errorf("%s do compose.yaml não é a chave de demonstração %d que o boot recusa", nome, i)
		}
	}
}
