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

// O Postgres de produção, do compose, da perna 18 do CI e dos testes tem de
// ser o MESMO digest (D29 do Plano 7). O Dependabot só atualiza o
// deploy/fly/db/Dockerfile e o compose.yaml; as outras duas strings são a mão,
// e este teste pega a que ficou para trás.
func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
	digest18 := regexp.MustCompile(`postgres:18-alpine@sha256:[0-9a-f]{64}`)
	fontes := []struct {
		arquivo string
		// regex com um grupo que isola a linha onde o digest 18 deve estar.
		linha *regexp.Regexp
	}{
		{"../../deploy/fly/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
		{"../../compose.yaml", regexp.MustCompile(`(?m)^\s+image: (postgres:\S+)$`)},
		{"../../.github/workflows/ci.yml", regexp.MustCompile(`(?m)^\s+image: (postgres:18\S*)$`)},
		{"../storage/testdb/testdb.go", regexp.MustCompile(`(?m)^const imagemPadrao = "([^"]+)"$`)},
	}
	var referencia, origem string
	for _, f := range fontes {
		conteudo, err := os.ReadFile(f.arquivo)
		if err != nil {
			t.Fatal(err)
		}
		ms := f.linha.FindAllSubmatch(conteudo, -1)
		if len(ms) != 1 {
			t.Errorf("%s: esperava uma linha com a imagem do Postgres 18, achei %d", f.arquivo, len(ms))
			continue
		}
		img := string(ms[0][1])
		if !digest18.MatchString(img) || digest18.FindString(img) != img {
			t.Errorf("%s: %q não é postgres:18-alpine por digest", f.arquivo, img)
			continue
		}
		if referencia == "" {
			referencia, origem = img, f.arquivo
		} else if img != referencia {
			t.Errorf("%s usa %s, mas %s usa %s: atualize os quatro juntos", f.arquivo, img, origem, referencia)
		}
	}
}

// O deploy e o backup usam o mesmo flyctl: um comando que funciona num e
// não no outro por diferença de versão é o tipo de coisa que só aparece em
// produção.
func TestFlyctlVersionIsTheSameInDeployAndBackup(t *testing.T) {
	re := regexp.MustCompile(`(?ms)setup-flyctl@\S+.*?^\s+version: (\S+)$`)
	var versoes []string
	for _, arq := range []string{"../../.github/workflows/deploy.yml", "../../.github/workflows/backup.yml"} {
		conteudo, err := os.ReadFile(arq)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindSubmatch(conteudo)
		if m == nil {
			t.Fatalf("%s: versão do setup-flyctl não encontrada", arq)
		}
		versoes = append(versoes, string(m[1]))
	}
	if versoes[0] != versoes[1] {
		t.Errorf("flyctl %s no deploy.yml e %s no backup.yml", versoes[0], versoes[1])
	}
}
