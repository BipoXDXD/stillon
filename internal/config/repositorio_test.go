package config

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// D2 (Plano 10, §3.4; banco S2 da revisão): uma versão maliciosa recém-
// publicada de um pacote do grafo não entra no PR agrupado da semana
// seguinte. O cooldown só vale para as atualizações de versão; as de
// segurança do Dependabot não esperam (documentação do GitHub, opção
// cooldown). Os dois ecossistemas que entram no binário e no bundle do
// painel têm 7 dias.
func TestDependabotWaitsSevenDaysForGoAndNpm(t *testing.T) {
	conteudo, err := os.ReadFile("../../.github/dependabot.yml")
	if err != nil {
		t.Fatal(err)
	}
	blocos := map[string]string{}
	for _, b := range strings.Split(string(conteudo), "\n  - package-ecosystem: ")[1:] {
		nome, resto, _ := strings.Cut(b, "\n")
		blocos[nome] = resto
	}
	re := regexp.MustCompile(`(?m)^    cooldown:\n      default-days: (\d+)$`)
	for _, eco := range []string{"gomod", "npm"} {
		b, ok := blocos[eco]
		if !ok {
			t.Errorf("dependabot.yml sem o ecossistema %s", eco)
			continue
		}
		m := re.FindStringSubmatch(b)
		if m == nil {
			t.Errorf("ecossistema %s sem cooldown.default-days", eco)
			continue
		}
		if m[1] != "7" {
			t.Errorf("ecossistema %s com cooldown de %s dias, queria 7", eco, m[1])
		}
	}
}

// D3 (Plano 10, §3.4; banco S3 da revisão): o runbook (docs/deploy/vps.md)
// gera a chave privada do age em ~ e decifra o backup em ~/vigilume-restauro,
// fora do repositório. A regra do .gitignore é a defesa para quando um desses
// arquivos for parar num clone (copiado, baixado ali, ou gerado lá por
// engano): um `git add -A` na raiz o levaria para o histórico, e o gitleaks
// do CI só o acha depois do push. Os nomes são os do runbook e do backup.yml; os
// arquivos do repositório continuam fora da regra (a perna oposta pega um
// padrão largo demais, como "*").
func TestGitignoreCoversTheBackupKeyAndDumps(t *testing.T) {
	ignorados := []string{
		"vigilume-backup.key",
		"deploy/backup/vigilume-backup.key",
		"vigilume.dump",
		"vigilume-20260925.dump.age",
		".env",
		".env.production",
		"web/.env.local",
		"web/.env.development.local",
		"web/.env.production",
	}
	// A perna oposta inclui arquivos que um dia serão versionados e que um
	// padrão largo demais esconderia sem aviso (m2 da revisão do Plano 10): o
	// exemplo de ambiente, o .env de desenvolvimento do Vite (sem segredo, por
	// convenção dele) e chaves de fixture TLS em testdata/.
	versionados := []string{
		"compose.yaml", "deploy/compose/seed.sh", "docs/deploy/vps.md", "go.mod",
		".env.example", "web/.env.example", "web/.env.development",
		"internal/prober/safedial/testdata/server.key", "testdata/tls/ca.key",
	}

	args := append([]string{"-C", "../..", "check-ignore", "--no-index"}, append(ignorados, versionados...)...)
	saida, err := exec.Command("git", args...).Output()
	// check-ignore sai 1 quando nenhum caminho é ignorado; outro erro é
	// ambiente (sem git, fora de um repositório) e falha o teste.
	if ee, ok := err.(*exec.ExitError); err != nil && (!ok || ee.ExitCode() != 1) {
		t.Fatalf("git check-ignore: %v", err)
	}
	casou := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(string(saida)), "\n") {
		casou[l] = true
	}
	for _, p := range ignorados {
		if !casou[p] {
			t.Errorf("%s não é ignorado pelo .gitignore", p)
		}
	}
	for _, p := range versionados {
		if casou[p] {
			t.Errorf("%s, que é do repositório, passou a ser ignorado", p)
		}
	}
}
