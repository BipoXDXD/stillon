package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Os baldes do limitador de autenticação (login por IP, 2FA e troca de senha
// por conta) vivem só na memória de cada processo (ADR 0002, issue #328). Com
// duas réplicas da API atrás do mesmo balanceador, cada uma conta por si e o
// teto vira N vezes o anunciado, sem erro nenhum. Esta fitness function trava
// a configuração: nenhum manifesto pode declarar mais de uma réplica de
// processo que sirva a API. Quem precisar de N>1 troca antes o limitador por
// um compartilhado (opção b do #328) e só então muda esta regra.
//
// A contagem é por manifesto: soma, entre os serviços que rodam a imagem do
// StillOn com papel all ou api, o scale/deploy.replicas (1 se ausente). O
// seed (imagem -tools) e um sondador (--role=prober) não servem a API.
var manifestosComAPI = []string{
	"../../compose.yaml",
	"../../deploy/vps/compose.prod.yaml",
	"../../deploy/coolify/app.json",
}

const motivoReplicaUnica = "o limitador de autenticação é só em memória (ADR 0002, issue #328): " +
	"troque-o por um compartilhado antes de escalar a API para N>1"

func TestLimitadorEmMemoriaExigeUmaReplicaDaAPI(t *testing.T) {
	for _, arquivo := range manifestosComAPI {
		t.Run(arquivo, func(t *testing.T) {
			servicos := servicosDoManifesto(t, arquivo)
			if total := replicasDaAPI(t, arquivo, servicos); total != 1 {
				t.Errorf("%s declara %d réplicas da API; o esperado é 1: %s", arquivo, total, motivoReplicaUnica)
			}
		})
	}
}

// A regra também precisa recusar o que ela promete recusar: cada forma de
// declarar N>1 que o compose e o Coolify aceitam.
func TestReplicasDaAPIContaCadaFormaDeEscalar(t *testing.T) {
	casos := []struct {
		nome     string
		servicos map[string]servicoComAPI
		esperado int
	}{
		{"sem réplica declarada", map[string]servicoComAPI{"app": {imagem: "stillon:x"}}, 1},
		{"scale 2", map[string]servicoComAPI{"app": {imagem: "stillon:x", replicas: []string{"2"}}}, 2},
		{"segundo serviço da API", map[string]servicoComAPI{
			"app":  {imagem: "stillon:x"},
			"app2": {imagem: "ghcr.io/bipoxdxd/stillon:x", papel: "api"},
		}, 2},
		{"imagem por variável do Coolify", map[string]servicoComAPI{
			"app": {imagem: "${STILLON_IMAGE:-stillon:abc@sha256:def}", replicas: []string{"3"}},
		}, 3},
		{"sondador separado não serve a API", map[string]servicoComAPI{
			"app":    {imagem: "stillon:x"},
			"prober": {imagem: "stillon:x", papel: "prober", replicas: []string{"2"}},
		}, 1},
		{"seed e banco não contam", map[string]servicoComAPI{
			"app":  {imagem: "stillon:x"},
			"seed": {imagem: "ghcr.io/bipoxdxd/stillon:x-tools"},
			"db":   {imagem: "postgres:18-alpine", replicas: []string{"2"}},
		}, 1},
		{"sem a API", map[string]servicoComAPI{"db": {imagem: "postgres:18-alpine"}}, 0},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if total := replicasDaAPI(t, c.nome, c.servicos); total != c.esperado {
				t.Errorf("total = %d, esperado %d", total, c.esperado)
			}
		})
	}
}

func TestManifestoYAMLLeScaleEDeployReplicas(t *testing.T) {
	casos := map[string]string{
		"scale":           "services:\n  app:\n    image: stillon:x\n    scale: 2\n",
		"deploy.replicas": "services:\n  app:\n    image: stillon:x\n    deploy:\n      replicas: 2\n",
	}
	for nome, yaml := range casos {
		t.Run(nome, func(t *testing.T) {
			servicos := servicosDoYAML(t, yaml)
			if got := servicos["app"].replicas; len(got) != 1 || got[0] != "2" {
				t.Errorf("réplicas lidas = %q, esperado [\"2\"]", got)
			}
		})
	}
}

// Formas de YAML que um leitor por regex perde (revisão do #378): um
// comentário na coluna 0 dentro de services esconderia o serviço seguinte, e
// um scale vindo de âncora com merge não aparece no texto do serviço.
func TestManifestoYAMLContaReplicasDaAPI(t *testing.T) {
	casos := []struct {
		nome     string
		yaml     string
		esperado int
	}{
		{"comentário na coluna 0 dentro de services",
			"services:\n  app:\n    image: stillon:x\n# segunda réplica\n  app2:\n    image: ghcr.io/bipoxdxd/stillon:x\n",
			2},
		{"scale vindo de âncora com merge",
			"x-escala: &escala\n  scale: 2\nservices:\n  app:\n    <<: *escala\n    image: stillon:x\n",
			2},
		{"scale e deploy.replicas no mesmo serviço somam",
			"services:\n  app:\n    image: stillon:x\n    scale: 1\n    deploy:\n      replicas: 1\n",
			2},
		{"papel por lista no environment",
			"services:\n  app:\n    image: stillon:x\n  prober:\n    image: stillon:x\n    scale: 3\n    environment:\n      - ROLE=prober\n",
			1},
		{"papel por command em texto",
			"services:\n  app:\n    image: stillon:x\n  prober:\n    image: stillon:x\n    scale: 3\n    command: --role=prober\n",
			1},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			servicos := servicosDoYAML(t, c.yaml)
			if total := replicasDaAPI(t, c.nome, servicos); total != c.esperado {
				t.Errorf("total = %d, esperado %d", total, c.esperado)
			}
		})
	}
}

type servicoComAPI struct {
	imagem   string
	papel    string   // vazio: o padrão do binário, all
	replicas []string // cada scale/deploy.replicas declarado; nenhum: 1
}

var imagemDoStillOn = regexp.MustCompile(`(^|[/-])stillon:`)

func (s servicoComAPI) servesAPI() bool {
	ehOBinario := imagemDoStillOn.MatchString(s.imagem) && !strings.HasSuffix(s.imagem, "-tools")
	return ehOBinario && s.papel != string(RoleProber)
}

func replicasDaAPI(t *testing.T, arquivo string, servicos map[string]servicoComAPI) int {
	t.Helper()
	total := 0
	for nome, s := range servicos {
		if !s.servesAPI() {
			continue
		}
		if len(s.replicas) == 0 {
			total++
			continue
		}
		for _, declarado := range s.replicas {
			n, err := strconv.Atoi(declarado)
			if err != nil {
				t.Fatalf("%s: serviço %s com réplicas %q, que não é um número fixo: %s",
					arquivo, nome, declarado, motivoReplicaUnica)
			}
			total += n
		}
	}
	return total
}

func servicosDoManifesto(t *testing.T, arquivo string) map[string]servicoComAPI {
	t.Helper()
	conteudo, err := os.ReadFile(arquivo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(arquivo, ".json") {
		return servicosDoJSON(t, arquivo, conteudo)
	}
	return servicosDoYAML(t, string(conteudo))
}

func servicosDoJSON(t *testing.T, arquivo string, conteudo []byte) map[string]servicoComAPI {
	t.Helper()
	var manifesto struct {
		Services map[string]struct {
			Image       string            `json:"image"`
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
			Scale       json.Number       `json:"scale"`
			Deploy      struct {
				Replicas json.Number `json:"replicas"`
			} `json:"deploy"`
		} `json:"services"`
	}
	if err := json.Unmarshal(conteudo, &manifesto); err != nil {
		t.Fatalf("%s: %v", arquivo, err)
	}
	servicos := make(map[string]servicoComAPI, len(manifesto.Services))
	for nome, s := range manifesto.Services {
		var replicas []string
		for _, declarado := range []json.Number{s.Scale, s.Deploy.Replicas} {
			if declarado != "" {
				replicas = append(replicas, declarado.String())
			}
		}
		papel := s.Environment["ROLE"]
		for _, arg := range s.Command {
			if p, ok := strings.CutPrefix(arg, "--role="); ok {
				papel = p
			}
		}
		servicos[nome] = servicoComAPI{imagem: s.Image, papel: papel, replicas: replicas}
	}
	return servicos
}

// composeYAML é o que a regra usa de um compose. O decodificador do yaml.v3
// resolve âncoras e merge keys (<<) antes de preencher os campos, então um
// scale herdado de x-escala aparece aqui como se estivesse escrito no serviço.
type composeYAML struct {
	Services map[string]struct {
		Image       string `yaml:"image"`
		Command     any    `yaml:"command"`
		Environment any    `yaml:"environment"`
		Scale       any    `yaml:"scale"`
		Deploy      struct {
			Replicas any `yaml:"replicas"`
		} `yaml:"deploy"`
	} `yaml:"services"`
}

// servicosDoYAML decodifica o compose com um parser YAML de verdade:
// comentários, indentação e âncoras não escondem serviço nem réplica.
func servicosDoYAML(t *testing.T, conteudo string) map[string]servicoComAPI {
	t.Helper()
	var manifesto composeYAML
	if err := yaml.Unmarshal([]byte(conteudo), &manifesto); err != nil {
		t.Fatalf("manifesto YAML inválido: %v", err)
	}
	if len(manifesto.Services) == 0 {
		t.Fatal("manifesto YAML sem bloco services")
	}
	servicos := make(map[string]servicoComAPI, len(manifesto.Services))
	for nome, s := range manifesto.Services {
		var replicas []string
		for _, declarado := range []any{s.Scale, s.Deploy.Replicas} {
			if declarado != nil {
				replicas = append(replicas, fmt.Sprint(declarado))
			}
		}
		servicos[nome] = servicoComAPI{
			imagem:   s.Image,
			papel:    papelDoServico(s.Environment, s.Command),
			replicas: replicas,
		}
	}
	return servicos
}

// papelDoServico aceita as duas formas que o compose aceita para environment
// (mapa ou lista de CHAVE=valor) e para command (lista ou texto); o --role do
// command vence o ROLE do ambiente.
func papelDoServico(ambiente, comando any) string {
	papel := ""
	switch v := ambiente.(type) {
	case map[string]any:
		if r, ok := v["ROLE"]; ok && r != nil {
			papel = fmt.Sprint(r)
		}
	case []any:
		for _, item := range v {
			if r, ok := strings.CutPrefix(fmt.Sprint(item), "ROLE="); ok {
				papel = r
			}
		}
	}
	var args []string
	switch v := comando.(type) {
	case string:
		args = strings.Fields(v)
	case []any:
		for _, item := range v {
			args = append(args, fmt.Sprint(item))
		}
	}
	for _, arg := range args {
		if r, ok := strings.CutPrefix(arg, "--role="); ok {
			papel = r
		}
	}
	return papel
}
