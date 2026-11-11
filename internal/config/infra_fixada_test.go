package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// O release automático troca só a imagem: o app.json que decide quais
// variáveis chegam ao contêiner fica preso ao commit de infraestrutura
// escolhido no recurso do Coolify. Em 2026-11-10 o binário passou a exigir
// PROBE_BLOCKED_CIDRS em produção, o app.json do commit fixado não a
// repassava, e o app ficou 13 min em restart loop (postmortem
// docs/postmortems/2026-11-10-release-261-variavel-obrigatoria.md).
//
// Este teste roda o Load deste commit com o ambiente que o app.json do commit
// fixado entregaria ao contêiner. Se o Load recusa, a imagem deste commit não
// sobe na infraestrutura que está no ar: faça o Passo 7 (atualizar o commit de
// infraestrutura no recurso e o deploy/coolify/infra-fixada) antes do merge.
const (
	arquivoInfraFixada = "../../deploy/coolify/infra-fixada"
	manifestoNoRepo    = "deploy/coolify/app.json"
	servicoDoApp       = "app"
)

// variaveisDoRecurso imita o .env do recurso do app no Coolify: os nomes que o
// app.json interpola, com valores sintéticos válidos. Os valores reais são
// segredo e ficam só no recurso. Um nome que o app.json passe a interpolar e
// que não esteja aqui reprova: antes de incluí-lo, confira que ele já está
// cadastrado no recurso. O e-mail fica ligado, como em produção (o Resend
// está ativo no recurso), para o teste exercitar as regras que o Load só
// aplica com e-mail.
func variaveisDoRecurso() map[string]string {
	chave := func(semente byte) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{semente}, keyLen))
	}
	return map[string]string{
		"STILLON_DOMINIO":      "stillon.exemplo.test",
		"STILLON_APP_PASSWORD": "senha-sintetica-do-teste",
		"MASTER_KEY":           chave('m'),
		"JWT_SECRET":           chave('j'),
		"IP_HASH_KEY":          chave('i'),
		"PROBE_BLOCKED_CIDRS":  "203.0.113.10,2001:db8::/64",
		"EMAIL_FROM":           "StillOn <alertas@exemplo.test>",
		"RESEND_API_KEY":       "re_sintetico_do_teste",
	}
}

func TestOAppDesteCommitSobeNaInfraestruturaFixada(t *testing.T) {
	sha := lerInfraFixada(t)
	manifesto := lerManifestoDoCommit(t, sha)

	if err := carregarComOManifesto(manifesto, variaveisDoRecurso()); err != nil {
		t.Fatalf("o app deste commit não sobe com o app.json do commit de infraestrutura fixado (%s):\n%v\n\n"+
			"Faça o Passo 7 antes do merge: cadastre a variável no recurso, aponte o commit de "+
			"infraestrutura para um app.json que a repasse e atualize o deploy/coolify/infra-fixada; "+
			"ou torne a exigência tolerante (só aviso) até lá (expand/contract).", sha, err)
	}
}

// O Passo 7 aponta o recurso para um commit novo. Este caso garante que o
// app.json da árvore atual também sobe com o código atual, para que esse passo
// seja sempre seguro.
func TestOAppDesteCommitSobeComOManifestoDaArvore(t *testing.T) {
	manifesto, err := os.ReadFile("../../" + manifestoNoRepo)
	if err != nil {
		t.Fatalf("lendo %s: %v", manifestoNoRepo, err)
	}
	if err := carregarComOManifesto(manifesto, variaveisDoRecurso()); err != nil {
		t.Fatalf("o app não sobe com o %s desta árvore:\n%v", manifestoNoRepo, err)
	}
}

// O incidente, reproduzido: o app.json de antes da exigência não repassa a
// variável, e o Load deste código a recusa.
func TestManifestoSemAVariavelExigidaReprova(t *testing.T) {
	manifesto := []byte(`{"services":{"app":{
		"command":["--role=all"],
		"environment":{
			"CORS_ORIGIN":"https://${STILLON_DOMINIO}",
			"DATABASE_URL":"postgres://u:${STILLON_APP_PASSWORD:?defina}@10.30.0.4:5432/stillon",
			"IP_HASH_KEY":"${IP_HASH_KEY:?defina}",
			"JWT_SECRET":"${JWT_SECRET:?defina}",
			"MASTER_KEY":"${MASTER_KEY:?defina}",
			"METRICS_ADDR":"off",
			"PUBLIC_BASE_URL":"https://${STILLON_DOMINIO:?defina}"
		}}}}`)

	err := carregarComOManifesto(manifesto, variaveisDoRecurso())
	if err == nil || !strings.Contains(err.Error(), erroListaDaHospedagemAusente) {
		t.Fatalf("erro = %v, queria a recusa de PROBE_BLOCKED_CIDRS", err)
	}
}

func TestInterpolacaoDoManifesto(t *testing.T) {
	recurso := map[string]string{"A": "a", "VAZIA": ""}
	casos := []struct {
		nome, entrada, saida, erro string
	}{
		{"literal", "x", "x", ""},
		{"simples", "${A}", "a", ""},
		{"no meio", "p-${A}-s", "p-a-s", ""},
		{"exigida presente", "${A:?msg}", "a", ""},
		{"exigida vazia", "${VAZIA:?msg}", "", "VAZIA"},
		{"exigida ausente", "${NOVA:?msg}", "", "NOVA"},
		{"padrão com valor", "${A:-d}", "a", ""},
		{"padrão vazia", "${VAZIA:-d}", "d", ""},
		{"padrão vazio", "${VAZIA:-}", "", ""},
		{"cifrão escapado", "$${A}", "${A}", ""},
		{"ausente sem padrão", "${NOVA}", "", "variaveisDoRecurso"},
		{"sintaxe desconhecida", "${A:+x}", "", "sintaxe"},
		{"sem chaves", "$A", "", "sintaxe"},
		{"padrão aninhado", "${A:-${B}}", "", "sintaxe"},
		{"mensagem com interpolação", "${A:?veja ${B}}", "", "sintaxe"},
		{"chave aberta no padrão", "${A:-{x}", "", "sintaxe"},
		{"chave fechando sobrando", "${A:?msg}}", "", "sintaxe"},
		{"chave fechando solta", "x}", "", "sintaxe"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := interpolar(c.entrada, recurso)
			if c.erro != "" {
				if err == nil || !strings.Contains(err.Error(), c.erro) {
					t.Fatalf("interpolar(%q) = %q, %v; queria erro com %q", c.entrada, got, err, c.erro)
				}
				return
			}
			if err != nil || got != c.saida {
				t.Fatalf("interpolar(%q) = %q, %v; queria %q", c.entrada, got, err, c.saida)
			}
		})
	}
}

func TestPapelDoComando(t *testing.T) {
	casos := []struct {
		nome    string
		comando []string
		papel   string
		erro    bool
	}{
		{"sem comando", nil, "", false},
		{"com igual", []string{"--role=prober"}, "prober", false},
		{"forma separada", []string{"--role", "api"}, "", true},
		{"argumento desconhecido", []string{"--outro"}, "", true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := papelDoComando(c.comando)
			if (err != nil) != c.erro || got != c.papel {
				t.Fatalf("papelDoComando(%q) = %q, %v; queria %q, erro %v", c.comando, got, err, c.papel, c.erro)
			}
		})
	}
}

var shaCompleto = regexp.MustCompile(`^[0-9a-f]{40}$`)

func lerInfraFixada(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(arquivoInfraFixada)
	if err != nil {
		t.Fatalf("sem a fonte versionada do commit de infraestrutura fixado no recurso: %v", err)
	}
	sha := strings.TrimSpace(string(b))
	if !shaCompleto.MatchString(sha) {
		t.Fatalf("%s deve ter só o SHA completo (40 hex), tem %q", arquivoInfraFixada, sha)
	}
	return sha
}

func lerManifestoDoCommit(t *testing.T, sha string) []byte {
	t.Helper()
	cmd := exec.Command("git", "show", sha+":"+manifestoNoRepo)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	saida, err := cmd.Output()
	if err != nil {
		t.Fatalf("não consegui ler %s no commit fixado %s (clone raso? rode `git fetch --depth=1 origin %s`): %v: %s",
			manifestoNoRepo, sha, sha, err, strings.TrimSpace(stderr.String()))
	}
	return saida
}

// carregarComOManifesto monta o ambiente que o app.json entregaria ao
// contêiner do app, com o papel do command, e chama o Load deste commit.
func carregarComOManifesto(manifesto []byte, recurso map[string]string) error {
	var m struct {
		Services map[string]struct {
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(manifesto, &m); err != nil {
		return fmt.Errorf("app.json inválido: %w", err)
	}
	app, ok := m.Services[servicoDoApp]
	if !ok {
		return fmt.Errorf("app.json sem o serviço %q", servicoDoApp)
	}

	ambiente := make(map[string]string, len(app.Environment)+1)
	for nome, bruto := range app.Environment {
		valor, err := interpolar(bruto, recurso)
		if err != nil {
			return fmt.Errorf("o manifesto não renderiza %s: %w", nome, err)
		}
		ambiente[nome] = valor
	}
	papel, err := papelDoComando(app.Command)
	if err != nil {
		return err
	}
	if papel != "" {
		ambiente["ROLE"] = papel // o --role sobrepõe ROLE, como no main
	}

	_, err = Load(func(nome string) string { return ambiente[nome] })
	return err
}

// interpolacao cobre a sintaxe do Compose que o app.json usa: $$, ${X},
// ${X:?msg} e ${X:-padrão}, sem aninhar. Qualquer outro "$", um "{" dentro do
// msg ou do padrão e um "}" sobrando reprovam, para o teste não aceitar em
// silêncio uma forma que ele não sabe renderizar.
var interpolacao = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:\?|:-)([^}]*))?\}`)

func interpolar(bruto string, recurso map[string]string) (string, error) {
	if err := recusarSintaxeNaoSuportada(bruto); err != nil {
		return "", err
	}
	var erro error
	saida := interpolacao.ReplaceAllStringFunc(bruto, func(trecho string) string {
		if trecho == "$$" {
			return "$"
		}
		partes := interpolacao.FindStringSubmatch(trecho)
		nome, operador, resto := partes[1], partes[2], partes[3]
		valor, cadastrada := recurso[nome]
		switch operador {
		case ":?":
			if valor == "" && erro == nil {
				erro = fmt.Errorf("${%s:?} sem valor no recurso: %s", nome, resto)
			}
		case ":-":
			if valor == "" {
				valor = resto
			}
		default:
			if !cadastrada && erro == nil {
				erro = fmt.Errorf("${%s} não está em variaveisDoRecurso; confira que o recurso a tem e inclua-a", nome)
			}
		}
		return valor
	})
	if erro != nil {
		return "", erro
	}
	return saida, nil
}

// recusarSintaxeNaoSuportada confere o modelo, não a saída: um valor do
// recurso pode conter "$" ou chaves sem que isso seja sintaxe.
func recusarSintaxeNaoSuportada(bruto string) error {
	for _, partes := range interpolacao.FindAllStringSubmatch(bruto, -1) {
		if resto := partes[3]; strings.ContainsAny(resto, "${") {
			return fmt.Errorf("sintaxe de interpolação não suportada (aninhada) em %q", bruto)
		}
	}
	sobra := interpolacao.ReplaceAllString(bruto, "")
	if strings.Contains(sobra, "$") || temChaveFechandoSolta(sobra) {
		return fmt.Errorf("sintaxe de interpolação não suportada em %q", bruto)
	}
	return nil
}

// temChaveFechandoSolta acusa um "}" sem "{" literal antes dele: o resto de um
// ${...} aninhado ou digitado a mais. Chaves literais pareadas, como as de
// "$${A}", passam.
func temChaveFechandoSolta(literal string) bool {
	abertas := 0
	for _, r := range literal {
		switch r {
		case '{':
			abertas++
		case '}':
			if abertas == 0 {
				return true
			}
			abertas--
		}
	}
	return false
}

func papelDoComando(comando []string) (string, error) {
	papel := ""
	for _, arg := range comando {
		valor, ok := strings.CutPrefix(arg, "--role=")
		if !ok {
			return "", fmt.Errorf("argumento %q do command não suportado pelo teste; use --role=<papel>", arg)
		}
		papel = valor
	}
	return papel, nil
}
