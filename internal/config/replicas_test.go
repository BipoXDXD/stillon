package config

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
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
		{"scale 2", map[string]servicoComAPI{"app": {imagem: "stillon:x", replicas: "2"}}, 2},
		{"segundo serviço da API", map[string]servicoComAPI{
			"app":  {imagem: "stillon:x"},
			"app2": {imagem: "ghcr.io/bipoxdxd/stillon:x", papel: "api"},
		}, 2},
		{"imagem por variável do Coolify", map[string]servicoComAPI{
			"app": {imagem: "${STILLON_IMAGE:-stillon:abc@sha256:def}", replicas: "3"},
		}, 3},
		{"sondador separado não serve a API", map[string]servicoComAPI{
			"app":    {imagem: "stillon:x"},
			"prober": {imagem: "stillon:x", papel: "prober", replicas: "2"},
		}, 1},
		{"seed e banco não contam", map[string]servicoComAPI{
			"app":  {imagem: "stillon:x"},
			"seed": {imagem: "ghcr.io/bipoxdxd/stillon:x-tools"},
			"db":   {imagem: "postgres:18-alpine", replicas: "2"},
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
			if got := servicos["app"].replicas; got != "2" {
				t.Errorf("réplicas lidas = %q, esperado \"2\"", got)
			}
		})
	}
}

type servicoComAPI struct {
	imagem   string
	papel    string // vazio: o padrão do binário, all
	replicas string // vazio: 1
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
		if s.replicas == "" {
			total++
			continue
		}
		n, err := strconv.Atoi(s.replicas)
		if err != nil {
			t.Fatalf("%s: serviço %s com réplicas %q, que não é um número fixo: %s",
				arquivo, nome, s.replicas, motivoReplicaUnica)
		}
		total += n
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
		replicas := s.Scale.String()
		if s.Deploy.Replicas != "" {
			replicas = s.Deploy.Replicas.String()
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

var (
	secaoServicos   = regexp.MustCompile(`(?m)^services:\n((?:(?:[ \t].*|[ \t]*)\n)*)`)
	inicioDeServico = regexp.MustCompile(`(?m)^  ([A-Za-z0-9_.-]+):[ \t]*$`)
	campoImagem     = regexp.MustCompile(`(?m)^    image:[ \t]*["']?([^"'\s]+)`)
	campoPapel      = regexp.MustCompile(`(?m)--role=([a-z]+)|^      ROLE:[ \t]*["']?([a-z]+)`)
	campoReplicas   = regexp.MustCompile(`(?m)^[ \t]+(?:scale|replicas):[ \t]*["']?([^"'\s#]+)`)
)

// servicosDoYAML lê do bloco services só o que a regra usa, no subconjunto de
// YAML que os composes do repositório escrevem (indentação de dois espaços).
func servicosDoYAML(t *testing.T, yaml string) map[string]servicoComAPI {
	t.Helper()
	secao := secaoServicos.FindStringSubmatch(yaml)
	if secao == nil {
		t.Fatal("manifesto YAML sem bloco services")
	}
	corpo := secao[1]
	inicios := inicioDeServico.FindAllStringSubmatchIndex(corpo, -1)
	servicos := make(map[string]servicoComAPI, len(inicios))
	for i, ini := range inicios {
		fim := len(corpo)
		if i+1 < len(inicios) {
			fim = inicios[i+1][0]
		}
		bloco := corpo[ini[1]:fim]
		servicos[corpo[ini[2]:ini[3]]] = servicoComAPI{
			imagem:   primeiroGrupo(campoImagem, bloco),
			papel:    primeiroGrupo(campoPapel, bloco),
			replicas: primeiroGrupo(campoReplicas, bloco),
		}
	}
	return servicos
}

func primeiroGrupo(re *regexp.Regexp, texto string) string {
	m := re.FindStringSubmatch(texto)
	for _, grupo := range m[min(1, len(m)):] {
		if grupo != "" {
			return grupo
		}
	}
	return ""
}
