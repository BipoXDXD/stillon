package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"testing"
	"time"
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
// ser o MESMO digest (D29 do Plano 7). O Dependabot atualiza o
// deploy/vps/db/Dockerfile, o deploy/vps/compose.prod.yaml e o compose.yaml;
// as outras duas strings são a mão, e este teste pega a que ficou para trás.
func TestPostgres18DigestIsTheSameEverywhere(t *testing.T) {
	digest18 := regexp.MustCompile(`postgres:18-alpine@sha256:[0-9a-f]{64}`)
	fontes := []struct {
		arquivo string
		// regex com um grupo que isola a linha onde o digest 18 deve estar.
		linha *regexp.Regexp
	}{
		{"../../deploy/vps/db/Dockerfile", regexp.MustCompile(`(?m)^FROM (\S+)$`)},
		{"../../deploy/vps/compose.prod.yaml", regexp.MustCompile(`(?m)^\s+image: (postgres:\S+)$`)},
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
			t.Errorf("%s usa %s, mas %s usa %s: atualize os cinco juntos", f.arquivo, img, origem, referencia)
		}
	}
}

// O compose tem de continuar subindo com as chaves de demo: o PUBLIC_BASE_URL
// dele precisa ser http e de loopback, senão o boot as recusa (hostLocal).
func TestComposePublicBaseURLIsLocal(t *testing.T) {
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s+PUBLIC_BASE_URL:\s+(\S+)`).FindSubmatch(compose)
	if m == nil {
		t.Fatal("PUBLIC_BASE_URL ausente do compose.yaml")
	}
	// A interpolação do compose com o padrão: ${STILLON_PORT:-8080} → 8080.
	base := regexp.MustCompile(`\$\{[A-Z_]+:-([^}]*)\}`).ReplaceAllString(string(m[1]), "$1")
	if EhHTTPS(base) || !hostLocal(base) {
		t.Errorf("PUBLIC_BASE_URL do compose.yaml (%s) não é http de loopback: o boot recusaria as chaves de demo", base)
	}
}

// servicoDoCompose devolve o bloco de um serviço do compose.yaml: da linha
// "  <nome>:" até o próximo serviço ou a próxima chave de topo.
func servicoDoCompose(t *testing.T, nome string) string {
	t.Helper()
	return servicoDe(t, "../../compose.yaml", nome)
}

// servicoDe devolve o bloco de um serviço de um arquivo compose qualquer.
func servicoDe(t *testing.T, arquivo, nome string) string {
	t.Helper()
	compose, err := os.ReadFile(arquivo)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^  ` + nome + `:\n((?:(?:    .*|[ \t]*)\n)*)`).FindSubmatch(compose)
	if m == nil {
		t.Fatalf("serviço %s ausente do %s", nome, arquivo)
	}
	return string(m[1])
}

// D1 (Plano 10, §3.4; banco C1 da revisão): depois de um reboot, ou de um
// restart do Docker, o daemon só religa os contêineres com política de
// restart. O app voltava e o db não: o monitor ficava parado e calado. O
// seed não entra: ele roda uma vez e sai.
func TestComposeDBRestartsWithTheHost(t *testing.T) {
	re := regexp.MustCompile(`(?m)^    restart: unless-stopped$`)
	for _, nome := range []string{"db", "app"} {
		if !re.MatchString(servicoDoCompose(t, nome)) {
			t.Errorf("serviço %s do compose.yaml sem restart: unless-stopped", nome)
		}
	}
	if regexp.MustCompile(`(?m)^    restart:`).MatchString(servicoDoCompose(t, "seed")) {
		t.Error("o seed tem restart: ele rodaria de novo a cada saída")
	}
}

// D1 (Plano 10, §3.4; banco S1 da revisão): quem copia o compose.yaml para
// um servidor leva junto o usuário de demonstração e as chaves públicas. O
// cabeçalho diz, antes de qualquer serviço, que ele é só da máquina local.
func TestComposeHeaderSaysItIsLocalOnly(t *testing.T) {
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cabecalho, _, ok := bytes.Cut(compose, []byte("\nname: stillon\n"))
	if !ok {
		t.Fatal("compose.yaml sem a linha name: stillon")
	}
	// "atrás de um proxy": o seed e o boot olham só a PUBLIC_BASE_URL
	// declarada. O compose copiado para um servidor, com um proxy na frente e
	// a URL ainda local, passa pelos dois (I1 da revisão do Plano 10), e o
	// cabeçalho não pode prometer proteção que não existe.
	for _, frase := range []string{"SÓ PARA A MÁQUINA LOCAL", "NUNCA EM SERVIDOR", "compose de produção", "atrás de um proxy"} {
		if !bytes.Contains(cabecalho, []byte(frase)) {
			t.Errorf("o cabeçalho do compose.yaml não diz %q", frase)
		}
	}
}

// C1 da revisão de 2026-10-14: sem stop_grace_period, o Docker manda SIGKILL
// 10 s depois do SIGTERM, e o app planeja até shutdownGrace (15 s) para
// terminar as sondas em voo e o lote do dispatcher. No `up` de um deploy, o
// check em gravação se perdia e um aviso já entregue, mas ainda não marcado,
// saía de novo depois do lease.
func TestComposeProdGracePeriodCoversTheAppShutdown(t *testing.T) {
	app := servicoDe(t, "../../deploy/vps/compose.prod.yaml", "app")
	m := regexp.MustCompile(`(?m)^    stop_grace_period: (\S+)$`).FindStringSubmatch(app)
	if m == nil {
		t.Fatalf("serviço app do compose.prod.yaml sem stop_grace_period: o Docker o mata aos 10 s, antes dos %v de shutdown do app", shutdownGrace)
	}
	grace, err := time.ParseDuration(m[1])
	if err != nil {
		t.Fatalf("stop_grace_period %q do app no compose.prod.yaml: %v", m[1], err)
	}
	if grace <= shutdownGrace {
		t.Errorf("stop_grace_period do app no compose.prod.yaml é %v: precisa passar dos %v de shutdown do app", grace, shutdownGrace)
	}
}

// Auditoria de segurança de 2026-10-14: o db de produção subia com todas as
// capacidades padrão do Docker e sem no-new-privileges. O entrypoint do
// Postgres oficial roda como root só para acertar dono e modo do volume e do
// /var/run/postgresql e trocar para o usuário postgres: CHOWN, DAC_OVERRIDE,
// FOWNER, SETGID e SETUID bastam (sem nenhuma, o chown do volume falha e o
// banco não sobe). O job vps do CI (deploy/ci/checar-vps.sh) prova a subida,
// o reinício, o backup e o restauro com este conjunto.
func TestComposeProdDBDropsCapabilities(t *testing.T) {
	db := servicoDe(t, "../../deploy/vps/compose.prod.yaml", "db")
	if !regexp.MustCompile(`(?m)^    security_opt: \["no-new-privileges:true"\]$`).MatchString(db) {
		t.Error(`serviço db do compose.prod.yaml sem security_opt: ["no-new-privileges:true"]`)
	}
	if !regexp.MustCompile(`(?m)^    cap_drop: \[ALL\]$`).MatchString(db) {
		t.Error("serviço db do compose.prod.yaml sem cap_drop: [ALL]")
	}
	m := regexp.MustCompile(`(?m)^    cap_add: \[([A-Z_, ]*)\]$`).FindStringSubmatch(db)
	if m == nil {
		t.Fatal("serviço db do compose.prod.yaml sem cap_add numa linha só")
	}
	const esperado = "CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID"
	if m[1] != esperado {
		t.Errorf("cap_add do db é [%s], queria [%s]", m[1], esperado)
	}
}

// Auditoria de segurança de 2026-10-14: o binário do app não escreve em disco
// (o backup roda o helper num Postgres descartável, não no contêiner do app),
// então o sistema de arquivos dele é só leitura, sem tmpfs.
func TestComposeProdAppIsReadOnly(t *testing.T) {
	app := servicoDe(t, "../../deploy/vps/compose.prod.yaml", "app")
	if !regexp.MustCompile(`(?m)^    read_only: true$`).MatchString(app) {
		t.Error("serviço app do compose.prod.yaml sem read_only: true")
	}
}
