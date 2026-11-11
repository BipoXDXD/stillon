package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// O job vps do CI prova o deploy/vps/compose.prod.yaml, mas o que está no ar é
// o deploy/coolify/app.json e o deploy/coolify/database.json: dois arquivos
// mantidos à mão. Sem esta comparação, um healthcheck, um read_only ou um
// mem_limit que some só do manifesto no ar deixa o job vps verde, e o receptor
// conta o contêiner sem healthcheck como saudável.
//
// Todo campo de cada serviço é comparado: o que não está em
// diferencasAceitas e difere reprova, inclusive campo novo.
const (
	composeProvado   = "../../deploy/vps/compose.prod.yaml"
	manifestoNoArApp = "../../deploy/coolify/app.json"
	manifestoNoArDB  = "../../deploy/coolify/database.json"
	campoAusente     = "<ausente>"

	volumeDoBanco       = "pgdata"
	nomeDoVolumeDoBanco = "stillon_pgdata"
	dadosDoBanco        = "volumes[/var/lib/postgresql].source"
)

// diferencaAceita é uma diferença de propósito. Vale só para os dois valores
// escritos aqui: outro valor reprova, e a entrada que deixou de ser diferença
// também, para a lista não apodrecer.
type diferencaAceita struct {
	campo   string
	provado string
	noAr    string
	// noArPadrao substitui noAr quando o valor no ar muda a cada release.
	noArPadrao *regexp.Regexp
	motivo     string
}

const (
	motivoPullPolicy = "o compose provado baixa caddy e db do registro; no ar toda imagem é importada antes e nada é baixado no corte"
	motivoNome       = "no ar o recurso do Coolify preserva o nome original do contêiner, que o runbook e a operação citam"
	motivoRotulo     = "só o Coolify tem Traefik: o rótulo o desliga, e quem recebe o tráfego é a borda própria"
	rotulosNoAr      = `["traefik.enable=false"]`
)

var diferencasAceitas = map[string][]diferencaAceita{
	"app": {
		{
			campo:      "image",
			provado:    "stillon:${STILLON_SHA:-atual}",
			noArPadrao: regexp.MustCompile(`^\$\{STILLON_IMAGE:-stillon:[0-9a-f]{40}@sha256:[0-9a-f]{64}\}$`),
			motivo:     "a entrada da VPS etiqueta por SHA; no ar o receptor injeta STILLON_IMAGE, e o padrão fica fixado por digest",
		},
		{
			campo:   "depends_on.db.condition",
			provado: "service_healthy",
			noAr:    campoAusente,
			motivo:  "no ar o banco é outro recurso (database.json), para o release do app não pará-lo",
		},
		{campo: "container_name", provado: campoAusente, noAr: "stillon-app-1", motivo: motivoNome},
		{campo: "labels", provado: campoAusente, noAr: rotulosNoAr, motivo: motivoRotulo},
	},
	"caddy": {
		{campo: "pull_policy", provado: campoAusente, noAr: "never", motivo: motivoPullPolicy},
		{campo: "container_name", provado: campoAusente, noAr: "stillon-caddy-1", motivo: motivoNome},
		{campo: "labels", provado: campoAusente, noAr: rotulosNoAr, motivo: motivoRotulo},
		{
			campo:   "volumes[/etc/caddy/Caddyfile].source",
			provado: "./Caddyfile",
			noAr:    "./deploy/vps/Caddyfile",
			motivo:  "o compose provado fica ao lado do Caddyfile; no ar o caminho parte da raiz do repositório",
		},
	},
	"db": {
		{campo: "pull_policy", provado: campoAusente, noAr: "never", motivo: motivoPullPolicy},
		{campo: "container_name", provado: campoAusente, noAr: "stillon-db-1", motivo: motivoNome},
		{campo: "labels", provado: campoAusente, noAr: rotulosNoAr, motivo: motivoRotulo},
		{
			campo:   "volumes[/docker-entrypoint-initdb.d/10-stillon.sh].source",
			provado: "./initdb.sh",
			noAr:    "/opt/stillon/initdb.sh",
			motivo:  "o compose provado fica ao lado do initdb.sh; no ar o bind aponta para o arquivo que já existe no host",
		},
	},
}

// camposCriticos são os que precisam existir nos dois lados: a comparação
// sozinha aceitaria o campo apagado dos dois arquivos, ou um leitor que não
// achasse nada.
var camposCriticos = map[string][]string{
	"app": {
		"image", "command", "pull_policy", "mem_limit", "cpus", "read_only", "cap_drop", "security_opt",
		"restart", "stop_grace_period", "logging.driver", "networks.stillon.ipv4_address",
		"healthcheck.test", "healthcheck.interval", "healthcheck.timeout", "healthcheck.retries",
		"healthcheck.start_period", "healthcheck.start_interval",
		"environment.DATABASE_URL", "environment.PUBLIC_BASE_URL", "environment.CORS_ORIGIN",
		"environment.TRUSTED_PROXIES", "environment.CLIENT_IP_HEADER", "environment.METRICS_ADDR",
		"environment.LOG_LEVEL", "environment.GOMEMLIMIT", "environment.MASTER_KEY",
		"environment.JWT_SECRET", "environment.IP_HASH_KEY", "environment.PROBE_BLOCKED_CIDRS",
	},
	"caddy": {
		"image", "mem_limit", "cpus", "cap_drop", "security_opt", "restart", "logging.driver",
		"networks.edge.ipv4_address", "networks.stillon.ipv4_address", "environment.STILLON_DOMINIO",
		"volumes[/etc/caddy/Caddyfile].read_only",
	},
	"db": {
		"image", "mem_limit", "cpus", "cap_drop", "cap_add", "security_opt", "restart", "logging.driver",
		"networks.stillon.ipv4_address", "healthcheck.test", "healthcheck.interval", "healthcheck.timeout",
		"healthcheck.retries", "healthcheck.start_period", "healthcheck.start_interval",
		"environment.POSTGRES_PASSWORD", "environment.STILLON_APP_PASSWORD",
		"environment.STILLON_BACKUP_PASSWORD", "volumes[/var/lib/postgresql].source",
		"volumes[/docker-entrypoint-initdb.d/10-stillon.sh].read_only",
	},
}

func TestManifestoNoArBateComOComposeProvado(t *testing.T) {
	casos := []struct{ servico, manifesto string }{
		{"app", manifestoNoArApp},
		{"caddy", manifestoNoArApp},
		{"db", manifestoNoArDB},
	}
	aliases := map[string]map[string]string{"log": blocoDeLogDoCompose(t)}
	for _, c := range casos {
		t.Run(c.servico, func(t *testing.T) {
			provado := achatarServicoYAML(t, servicoDe(t, composeProvado, c.servico), aliases)
			servicoNoAr, ok := servicosCrusDoManifesto(t, c.manifesto)[c.servico]
			if !ok {
				t.Fatalf("%s sem o serviço %s", c.manifesto, c.servico)
			}
			noAr := achatarServicoJSON(t, servicoNoAr)

			for _, campo := range camposCriticos[c.servico] {
				if _, existe := provado[campo]; !existe {
					t.Errorf("campo crítico %s ausente do %s", campo, composeProvado)
				}
				if _, existe := noAr[campo]; !existe {
					t.Errorf("campo crítico %s ausente do %s", campo, c.manifesto)
				}
			}
			for _, divergencia := range divergenciasDoManifesto(provado, noAr, diferencasAceitas[c.servico]) {
				t.Error(divergencia)
			}
		})
	}
}

// Serviço novo num arquivo só não entraria nas comparações acima.
func TestManifestosTemOsServicosEsperados(t *testing.T) {
	compose, err := os.ReadFile(composeProvado)
	if err != nil {
		t.Fatal(err)
	}
	secao := regexp.MustCompile(`(?ms)^services:\n(.*?)^\S`).FindSubmatch(compose)
	if secao == nil {
		t.Fatalf("%s sem a seção services", composeProvado)
	}
	var doCompose []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z_-]+):$`).FindAllSubmatch(secao[1], -1) {
		doCompose = append(doCompose, string(m[1]))
	}
	slices.Sort(doCompose)
	if want := []string{"app", "caddy", "db"}; !slices.Equal(doCompose, want) {
		t.Errorf("%s com serviços %v, queria %v", composeProvado, doCompose, want)
	}

	esperados := []struct {
		manifesto string
		servicos  []string
	}{
		{manifestoNoArApp, []string{"app", "caddy"}},
		{manifestoNoArDB, []string{"db"}},
	}
	for _, e := range esperados {
		nomes := slices.Sorted(maps.Keys(servicosCrusDoManifesto(t, e.manifesto)))
		if !slices.Equal(nomes, e.servicos) {
			t.Errorf("%s com serviços %v, queria %v", e.manifesto, nomes, e.servicos)
		}
	}
}

// O nome e o external dizem qual volume guarda os dados do Postgres: sem o
// external, ou com outro nome, o Coolify cria um volume vazio e o banco sobe
// sem nada. Nenhuma comparação por serviço enxerga o bloco de topo.
func TestVolumeDoBancoNoArEhOQueGuardaOsDados(t *testing.T) {
	banco := lerManifesto(t, manifestoNoArDB)
	queria := map[string]any{volumeDoBanco: map[string]any{"name": nomeDoVolumeDoBanco, "external": true}}
	if !reflect.DeepEqual(banco.Volumes, queria) {
		t.Errorf("%s com volumes %v, queria %v", manifestoNoArDB, banco.Volumes, queria)
	}
	if origem := achatarServicoJSON(t, banco.Services["db"])[dadosDoBanco]; origem != volumeDoBanco {
		t.Errorf("%s monta %q nos dados do Postgres, queria %q", manifestoNoArDB, origem, volumeDoBanco)
	}
	if declarado, existe := lerManifesto(t, manifestoNoArApp).Volumes[volumeDoBanco]; existe {
		t.Errorf("%s declara o volume do banco (%v): quem o declara é o %s", manifestoNoArApp, declarado, manifestoNoArDB)
	}
}

// O compose provado não escreve o nome: ele sai do projeto e da chave do bloco
// volumes, e tem de dar no mesmo volume que o manifesto no ar usa.
func TestComposeProvadoNomeiaOMesmoVolumeDoBanco(t *testing.T) {
	compose, err := os.ReadFile(composeProvado)
	if err != nil {
		t.Fatal(err)
	}
	projeto := regexp.MustCompile(`(?m)^name: (\S+)$`).FindSubmatch(compose)
	if projeto == nil {
		t.Fatalf("%s sem o nome do projeto", composeProvado)
	}
	if nome := string(projeto[1]) + "_" + volumeDoBanco; nome != nomeDoVolumeDoBanco {
		t.Errorf("%s nomeia o volume do banco %q, queria %q", composeProvado, nome, nomeDoVolumeDoBanco)
	}
	bloco := regexp.MustCompile(`(?m)^volumes:\n((?:[ #].*\n)*)`).FindSubmatch(compose)
	if bloco == nil {
		t.Fatalf("%s sem o bloco volumes", composeProvado)
	}
	if queria := "  " + volumeDoBanco + ":\n"; string(bloco[1]) != queria {
		t.Errorf("%s com o bloco volumes %q, queria %q", composeProvado, bloco[1], queria)
	}
}

// divergenciasDoManifesto devolve uma mensagem por campo que difere sem estar
// aceito e por entrada aceita que não confere mais.
func divergenciasDoManifesto(provado, noAr map[string]string, aceitas []diferencaAceita) []string {
	aceitaPorCampo := make(map[string]diferencaAceita, len(aceitas))
	campos := make(map[string]struct{}, len(provado))
	for _, a := range aceitas {
		aceitaPorCampo[a.campo] = a
		campos[a.campo] = struct{}{}
	}
	for campo := range provado {
		campos[campo] = struct{}{}
	}
	for campo := range noAr {
		campos[campo] = struct{}{}
	}

	var divergencias []string
	for _, campo := range slices.Sorted(maps.Keys(campos)) {
		valorProvado := valorOuAusente(provado, campo)
		valorNoAr := valorOuAusente(noAr, campo)
		aceita, listada := aceitaPorCampo[campo]
		switch {
		case listada && valorProvado == valorNoAr:
			divergencias = append(divergencias, fmt.Sprintf("%s: está na lista de diferenças aceitas, mas os dois lados valem %q: tire a entrada", campo, valorProvado))
		case listada && !aceita.cobre(valorProvado, valorNoAr):
			divergencias = append(divergencias, fmt.Sprintf("%s: provado %q, no ar %q: fora da diferença aceita (%s)", campo, valorProvado, valorNoAr, aceita.motivo))
		case !listada && valorProvado != valorNoAr:
			divergencias = append(divergencias, fmt.Sprintf("%s: provado %q, no ar %q", campo, valorProvado, valorNoAr))
		}
	}
	return divergencias
}

func (a diferencaAceita) cobre(valorProvado, valorNoAr string) bool {
	if valorProvado != a.provado {
		return false
	}
	if a.noArPadrao != nil {
		return a.noArPadrao.MatchString(valorNoAr)
	}
	return valorNoAr == a.noAr
}

func valorOuAusente(campos map[string]string, campo string) string {
	if valor, existe := campos[campo]; existe {
		return valor
	}
	return campoAusente
}

func servicosCrusDoManifesto(t *testing.T, arquivo string) map[string]map[string]any {
	t.Helper()
	return lerManifesto(t, arquivo).Services
}

type manifestoNoAr struct {
	Services map[string]map[string]any `json:"services"`
	Volumes  map[string]any            `json:"volumes"`
}

func lerManifesto(t *testing.T, arquivo string) manifestoNoAr {
	t.Helper()
	conteudo, err := os.ReadFile(arquivo)
	if err != nil {
		t.Fatal(err)
	}
	decodificador := json.NewDecoder(strings.NewReader(string(conteudo)))
	decodificador.UseNumber()
	var manifesto manifestoNoAr
	if err := decodificador.Decode(&manifesto); err != nil {
		t.Fatalf("%s: %v", arquivo, err)
	}
	if len(manifesto.Services) == 0 {
		t.Fatalf("%s sem serviços", arquivo)
	}
	return manifesto
}

// achatarServicoJSON leva um serviço do manifesto para campo pontuado → valor.
func achatarServicoJSON(t *testing.T, servico map[string]any) map[string]string {
	t.Helper()
	campos := make(map[string]string)
	for chave, valor := range servico {
		if chave == "volumes" {
			achatarVolumesJSON(t, valor, campos)
			continue
		}
		achatarValorJSON(t, chave, valor, campos)
	}
	return campos
}

func achatarValorJSON(t *testing.T, caminho string, valor any, campos map[string]string) {
	t.Helper()
	switch v := valor.(type) {
	case map[string]any:
		for chave, filho := range v {
			achatarValorJSON(t, caminho+"."+chave, filho, campos)
		}
	case []any:
		itens := make([]string, 0, len(v))
		for _, item := range v {
			switch item.(type) {
			case string, json.Number, bool:
				itens = append(itens, fmt.Sprint(item))
			default:
				t.Fatalf("%s: lista com item que o teste não sabe comparar: %v", caminho, item)
			}
		}
		campos[caminho] = listaComparavel(itens)
	case string, json.Number, bool:
		campos[caminho] = fmt.Sprint(v)
	default:
		t.Fatalf("%s: valor que o teste não sabe comparar: %v", caminho, valor)
	}
}

// achatarVolumesJSON guarda só a origem e o modo de cada destino: type e bind
// são detalhes da forma longa, que a forma curta do compose não escreve.
func achatarVolumesJSON(t *testing.T, valor any, campos map[string]string) {
	t.Helper()
	volumes, ok := valor.([]any)
	if !ok {
		t.Fatalf("volumes não é lista: %v", valor)
	}
	for _, item := range volumes {
		volume, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("volume não é objeto: %v", item)
		}
		somenteLeitura, _ := volume["read_only"].(bool)
		anotarVolume(campos, fmt.Sprint(volume["target"]), fmt.Sprint(volume["source"]), somenteLeitura)
	}
}

func anotarVolume(campos map[string]string, destino, origem string, somenteLeitura bool) {
	campos["volumes["+destino+"].source"] = origem
	if somenteLeitura {
		campos["volumes["+destino+"].read_only"] = "true"
	}
}

var chaveDoCompose = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// blocoDeLogDoCompose lê a âncora x-log, que os serviços usam com logging: *log.
func blocoDeLogDoCompose(t *testing.T) map[string]string {
	t.Helper()
	compose, err := os.ReadFile(composeProvado)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^x-log: &log\n((?:  .*\n)+)`).FindSubmatch(compose)
	if m == nil {
		t.Fatalf("%s sem a âncora x-log", composeProvado)
	}
	return achatarServicoYAML(t, string(m[1]), nil)
}

// achatarServicoYAML lê o subconjunto de YAML que o compose.prod.yaml usa e
// para em qualquer outra forma, para nenhum campo escapar da comparação.
func achatarServicoYAML(t *testing.T, bloco string, aliases map[string]map[string]string) map[string]string {
	t.Helper()
	type nivel struct {
		recuo int
		chave string
	}
	var pilha []nivel
	campos := make(map[string]string)
	for linha := range strings.SplitSeq(bloco, "\n") {
		texto := strings.TrimLeft(linha, " ")
		if texto == "" || strings.HasPrefix(texto, "#") {
			continue
		}
		recuo := len(linha) - len(texto)
		for len(pilha) > 0 && pilha[len(pilha)-1].recuo >= recuo {
			pilha = pilha[:len(pilha)-1]
		}
		prefixo := ""
		for _, n := range pilha {
			prefixo += n.chave + "."
		}
		if item, ehItem := strings.CutPrefix(texto, "- "); ehItem {
			if prefixo != "volumes." {
				t.Fatalf("lista em %q: o teste só conhece a de volumes", prefixo)
			}
			anotarVolumeCurto(t, item, campos)
			continue
		}
		chave, valor, achou := strings.Cut(texto, ":")
		if !achou || !chaveDoCompose.MatchString(chave) {
			t.Fatalf("linha que o teste não sabe ler: %q", linha)
		}
		valor = strings.TrimSpace(valor)
		if valor == "" {
			pilha = append(pilha, nivel{recuo, chave})
			continue
		}
		caminho := prefixo + chave
		if nome, ehAlias := strings.CutPrefix(valor, "*"); ehAlias {
			bloco, conhecido := aliases[nome]
			if !conhecido {
				t.Fatalf("%s: alias desconhecido %q", caminho, nome)
			}
			for filho, v := range bloco {
				campos[caminho+"."+filho] = v
			}
			continue
		}
		campos[caminho] = valorEscalarOuLista(t, caminho, valor)
	}
	return campos
}

func anotarVolumeCurto(t *testing.T, item string, campos map[string]string) {
	t.Helper()
	partes := strings.Split(item, ":")
	switch {
	case len(partes) == 2:
		anotarVolume(campos, partes[1], partes[0], false)
	case len(partes) == 3 && partes[2] == "ro":
		anotarVolume(campos, partes[1], partes[0], true)
	default:
		t.Fatalf("volume que o teste não sabe ler: %q", item)
	}
}

func valorEscalarOuLista(t *testing.T, caminho, valor string) string {
	t.Helper()
	if strings.Contains(valor, " #") {
		t.Fatalf("%s: comentário na linha do valor, que o teste não sabe separar: %q", caminho, valor)
	}
	interior, ehLista := strings.CutPrefix(valor, "[")
	if !ehLista {
		return strings.Trim(valor, `"`)
	}
	interior, fechou := strings.CutSuffix(interior, "]")
	if !fechou {
		t.Fatalf("%s: lista que o teste não sabe ler: %q", caminho, valor)
	}
	itens := strings.Split(interior, ",")
	for i, item := range itens {
		itens[i] = strings.Trim(strings.TrimSpace(item), `"`)
	}
	return listaComparavel(itens)
}

// listaComparavel cita cada item: juntar com espaço faria ["a b"] e ["a", "b"]
// virarem o mesmo texto.
func listaComparavel(itens []string) string {
	return fmt.Sprintf("%q", itens)
}
