package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// paginaDeTeste monta a projeção de n serviços com nomes longos e o
// histórico cheio; dia(i) decide o uptime do dia i de cada barra.
func paginaDeTeste(servicos int, dia func(i int) *float64) statuspage.View {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	u := 0.99912
	lat := int32(143)
	v := statuspage.View{Title: "Status da Empresa Exemplo de Serviços Online", GeneratedAt: agora}
	for i := range servicos {
		it := statuspage.PublicItem{Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i+1),
			State: statuspage.StateDegraded, Uptime90: &u, LatencyMS: &lat}
		for d := range it.Days {
			it.Days[d] = statuspage.Day{Date: agora.AddDate(0, 0, d-statuspage.Days+1), Uptime: dia(d)}
		}
		v.Items = append(v.Items, it)
	}
	for i := range statuspage.MaxIncidents {
		inicio := agora.Add(-time.Duration(i+1) * 26 * time.Hour)
		v.Incidents = append(v.Incidents, statuspage.PublicIncident{
			Name: fmt.Sprintf("Serviço número %d da empresa exemplo", i%5+1), StartedAt: inicio, ResolvedAt: inicio.Add(95 * time.Minute),
		})
	}
	return v
}

func renderizar(t *testing.T, v statuspage.View) string {
	t.Helper()
	var buf bytes.Buffer
	if err := statusTemplate.Execute(&buf, apresentar(v, "AAAAAAAAAAAAAAAAAAAAAA==")); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Spec §10.1: resposta abaixo de 10 KB. A página sai em gzip para quem o
// aceita (todo navegador), e o teto vale para o corpo comprimido: no pior
// caso de cinco serviços (os 90 dias espalhados por igual nas quatro
// classes, onde a codificação pela maioria quase não ajuda) e com os 20
// serviços que a página aceita, que em texto puro passavam dos 10 KB. Sem
// compressão, o teto de 16 KB pega uma regressão grande para o cliente que
// não aceita gzip.
func TestPublicPageFitsInTenKilobytes(t *testing.T) {
	valores := []*float64{nil, ptr(1), ptr(0.995), ptr(0.5)} // n, verde, d, f
	espalhado := func(i int) *float64 { return valores[i%4] }
	pior := renderizar(t, paginaDeTeste(5, espalhado))
	if n := len(comprimida([]byte(pior))); n >= 10*1024 {
		t.Errorf("pior caso comprimido: %d bytes, queria menos de 10 KB", n)
	}
	if n := len(pior); n >= 16*1024 {
		t.Errorf("pior caso sem compressão: %d bytes, queria menos de 16 KB", n)
	}
	if c, quer := strings.Count(pior, "<li"), 5*statuspage.Days+5+statuspage.MaxIncidents+itensDaLegenda; c != quer {
		t.Errorf("%d <li>, queria %d (dias, serviços, incidentes e legenda)", c, quer)
	}
	cheia := renderizar(t, paginaDeTeste(statuspage.MaxItems, espalhado))
	if n := len(comprimida([]byte(cheia))); n >= 10*1024 {
		t.Errorf("%d serviços comprimidos: %d bytes, queria menos de 10 KB", statuspage.MaxItems, n)
	}
	t.Logf("pior caso %d bytes (%d comprimido); %d serviços %d bytes (%d comprimido)",
		len(pior), len(comprimida([]byte(pior))), statuspage.MaxItems, len(cheia), len(comprimida([]byte(cheia))))
}

func ptr(v float64) *float64 { return &v }

// A barra sai pela maioria e reconstrói cada dia: a classe efetiva de um
// dia é a dele, ou a da <ol> quando ele não tem.
func TestBarEncodesByMajority(t *testing.T) {
	var dias [statuspage.Days]statuspage.Day
	for i := range dias {
		dias[i].Uptime = ptr(0.995) // amarelo
	}
	dias[0].Uptime, dias[1].Uptime, dias[2].Uptime = nil, ptr(1), ptr(0.1)
	maioria, classes := barra(dias)
	if maioria != "d" || classes[0] != "n" || classes[1] != "g" || classes[2] != "f" || classes[3] != "" {
		t.Errorf("barra = %q, %q", maioria, classes[:4])
	}
	var verdes [statuspage.Days]statuspage.Day
	for i := range verdes {
		verdes[i].Uptime = ptr(1)
	}
	if maioria, classes := barra(verdes); maioria != "" || classes != ([statuspage.Days]string{}) {
		t.Errorf("barra toda verde = %q, %v; queria sem classe nenhuma", maioria, classes)
	}
}

func TestPresentationRules(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		u    *float64
		quer string
	}{{nil, "n"}, {f(1), ""}, {f(0.999), ""}, {f(0.9989), "d"}, {f(0.99), "d"}, {f(0.9899), "f"}, {f(0), "f"}} {
		if got := classeDia(c.u); got != c.quer {
			t.Errorf("classeDia(%v) = %q, queria %q", c.u, got, c.quer)
		}
	}
	for u, quer := range map[float64]string{1: "100,00%", 0.99999: "99,99%", 0.5: "50,00%", 0: "0,00%"} {
		if got := porcento(u); got != quer {
			t.Errorf("porcento(%v) = %q, queria %q", u, got, quer)
		}
	}
	for d, quer := range map[time.Duration]string{
		20 * time.Second: "menos de 1 min", 7 * time.Minute: "7 min",
		65 * time.Minute: "1 h 5 min", 50 * time.Hour: "2 d 2 h",
	} {
		if got := duracao(d); got != quer {
			t.Errorf("duracao(%v) = %q, queria %q", d, got, quer)
		}
	}
}

// A frase da faixa segue os casos de testdata/faixa.json, que a prévia do
// painel (web/src/features/statuspage/faixa.test.ts) também lê: a página
// publicada e a prévia dizem a mesma coisa.
func TestBandPhraseFollowsTheSharedCases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "faixa.json"))
	if err != nil {
		t.Fatal(err)
	}
	var casos []struct {
		Caso  string `json:"caso"`
		Itens []struct {
			Nome   string           `json:"nome"`
			Estado statuspage.State `json:"estado"`
		} `json:"itens"`
		Estado  string `json:"estado"`
		Frase   string `json:"frase"`
		Detalhe string `json:"detalhe"`
	}
	if err := json.Unmarshal(b, &casos); err != nil {
		t.Fatal(err)
	}
	if len(casos) != 9 {
		t.Fatalf("%d casos em testdata/faixa.json, queria 9", len(casos))
	}
	for _, c := range casos {
		t.Run(c.Caso, func(t *testing.T) {
			items := make([]statuspage.PublicItem, 0, len(c.Itens))
			for _, it := range c.Itens {
				items = append(items, statuspage.PublicItem{Name: it.Nome, State: it.Estado})
			}
			quer := bandModelo{State: c.Estado, Text: c.Frase, Detail: c.Detalhe}
			if got := faixa(items); got != quer {
				t.Errorf("faixa = %+v, queria %+v", got, quer)
			}
		})
	}
}

// Review Focus 4 da parte 3 do redesign: a página carrega as fontes de
// /fonts/, no próprio host. A CSP abre font-src 'self' e nada além: a string
// inteira é conferida, para uma diretiva a mais (script, imagem, outro host)
// também quebrar o teste.
func TestPublicPagePolicyOpensOnlyTheOwnFonts(t *testing.T) {
	const quer = "default-src 'none'; style-src 'nonce-abc'; font-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	if got := politicaDaPagina("abc"); got != quer {
		t.Errorf("CSP da página pública:\n got %q\nwant %q", got, quer)
	}
}

// A legenda tem quatro <li> de texto, cada um com uma barra de um <li>.
const itensDaLegenda = 8

var (
	listaDeServicosRE = regexp.MustCompile(`(?s)<ul class=s>(.*?)</ul>`)
	barraRE           = regexp.MustCompile(`<ol(?: class="(\w)")? aria-hidden=true>(.*?)</ol>`)
	diaRE             = regexp.MustCompile(`<li(?: class="(\w)")?>`)
)

// diasEfetivos lê de volta a classe de cada dia de cada barra da lista de
// serviços: a do <li>, ou a da <ol> (a maioria), ou "g" (no ar) sem as duas.
func diasEfetivos(t *testing.T, html string) [][]string {
	t.Helper()
	lista := listaDeServicosRE.FindStringSubmatch(html)
	if lista == nil {
		t.Fatal("a página não tem a lista de serviços")
	}
	var barras [][]string
	for _, b := range barraRE.FindAllStringSubmatch(lista[1], -1) {
		var dias []string
		for _, d := range diaRE.FindAllStringSubmatch(b[2], -1) {
			switch {
			case d[1] != "":
				dias = append(dias, d[1])
			case b[1] != "":
				dias = append(dias, b[1])
			default:
				dias = append(dias, "g")
			}
		}
		barras = append(barras, dias)
	}
	return barras
}

func contem(t *testing.T, html string, trechos ...string) {
	t.Helper()
	for _, s := range trechos {
		if !strings.Contains(html, s) {
			t.Errorf("a página não tem %q", s)
		}
	}
}

// Spec do redesign, Telas 6: a faixa de estado em frase, a legenda e a classe
// do estado de cada dia. Sabotagem: em classeDia, troque o "d" pelo "f" →
// o segundo dia sai "f", e a lista de dias não confere.
func TestTemplateRendersBandLegendAndDayClasses(t *testing.T) {
	it := statuspage.PublicItem{Name: "API", State: statuspage.StateDegraded, Uptime90: ptr(0.9991)}
	for d := range it.Days {
		it.Days[d].Uptime = ptr(1)
	}
	it.Days[0].Uptime, it.Days[1].Uptime, it.Days[2].Uptime = nil, ptr(0.995), ptr(0.5)
	html := renderizar(t, statuspage.View{Title: "Empresa", Items: []statuspage.PublicItem{it}})
	contem(t, html,
		`<div class="b degraded"><strong>Instabilidade em 1 serviço.</strong><p>Lento: API.</div>`,
		`<span class=degraded>Lento</span>`,
		"<ul class=l>", "No ar (99,9% ou mais)", "Instável (99% a 99,9%)", "Fora do ar (menos de 99%)", "Sem dados</ul>",
		"Cada barra é um dia, de 90 dias atrás até hoje.",
		"99,91% no ar em 90 dias",
		"Monitorado pelo StillOn",
	)
	quer := append([]string{"n", "d", "f"}, slices.Repeat([]string{"g"}, statuspage.Days-3)...)
	if got := diasEfetivos(t, html); len(got) != 1 || !slices.Equal(got[0], quer) {
		t.Errorf("dias = %v, queria %v", got, quer)
	}
}

// Review Focus 1: sem itens, a faixa diz isso, e não há lista, legenda nem
// a nota das barras; o histórico continua.
func TestPublicPageWithoutItems(t *testing.T) {
	html := renderizar(t, statuspage.View{Title: "Empresa"})
	contem(t, html,
		`<div class="b unknown"><strong>Nenhum serviço nesta página.</strong></div>`,
		"Nenhum incidente nos últimos 90 dias.",
	)
	for _, nao := range []string{"<ul class=s>", "<ul class=l>", "Cada barra é um dia"} {
		if strings.Contains(html, nao) {
			t.Errorf("a página sem itens tem %q", nao)
		}
	}
}

// Review Focus 2: um serviço sem nenhum check em 90 dias é a barra inteira
// em contorno, com o uptime e o selo dizendo "sem dados".
func TestComponentWithoutDataIn90Days(t *testing.T) {
	novo := statuspage.PublicItem{Name: "Novo", State: statuspage.StateUnknown}
	html := renderizar(t, statuspage.View{Title: "Empresa", Items: []statuspage.PublicItem{novo}})
	contem(t, html,
		`<ol class="n" aria-hidden=true>`+strings.Repeat("<li>", statuspage.Days)+`</ol>`,
		"Sem dados nos últimos 90 dias",
		`<span class=unknown>Sem dados</span>`,
		`<div class="b unknown"><strong>Sem dados recentes de 1 serviço.</strong><p>Sem dados: Novo.</div>`,
	)
}

// Review Focus 3: o maior título que o servidor aceita, sem espaço, quebra em
// qualquer ponto, e o nome do serviço e o detalhe da faixa também.
func TestLongOrganizationNameWraps(t *testing.T) {
	titulo := strings.Repeat("Organizacao", 9) + "X"
	if n := utf8.RuneCountInString(titulo); n != 100 {
		t.Fatalf("título de %d runas, queria 100 (o teto do servidor)", n)
	}
	html := renderizar(t, statuspage.View{Title: titulo})
	contem(t, html, "<title>"+titulo+"</title>", "<h1>"+titulo+"</h1>")
	c := lerCSSDaPagina(t)
	for _, sel := range []string{"h1", ".h strong", ".b p", ".i li"} {
		if v := c.regras[sel]["overflow-wrap"]; v != "anywhere" {
			t.Errorf("%s com overflow-wrap %q, queria anywhere", sel, v)
		}
	}
}

// A resposta declara o tamanho do que vai no fio, em cada codificação, e o
// HEAD traz os mesmos cabeçalhos sem corpo (como o painel em
// webui.servirArquivo). O corpo de entrada é o mesmo nos quatro pedidos, então
// os tamanhos têm de bater byte a byte.
func TestDeliveredPageDeclaresItsLengthAndHeadHasNoBody(t *testing.T) {
	html := []byte(renderizar(t, paginaDeTeste(5, func(int) *float64 { return ptr(1) })))
	for _, aceita := range []string{"gzip", "identity"} {
		t.Run(aceita, func(t *testing.T) {
			pedir := func(metodo string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(metodo, "/p/empresa", nil)
				req.Header.Set("Accept-Encoding", aceita)
				rec := httptest.NewRecorder()
				entregar(rec, req, html, "abc")
				return rec
			}
			get, head := pedir(http.MethodGet), pedir(http.MethodHead)
			if get.Code != http.StatusOK || head.Code != http.StatusOK {
				t.Fatalf("GET %d e HEAD %d, queria 200", get.Code, head.Code)
			}
			if cl, quer := get.Header().Get("Content-Length"), strconv.Itoa(get.Body.Len()); cl != quer {
				t.Errorf("GET com Content-Length %q, e o corpo tem %s bytes", cl, quer)
			}
			if cl := head.Header().Get("Content-Length"); cl != get.Header().Get("Content-Length") {
				t.Errorf("HEAD com Content-Length %q, o GET com %q", cl, get.Header().Get("Content-Length"))
			}
			if head.Body.Len() != 0 {
				t.Errorf("HEAD escreveu %d bytes de corpo", head.Body.Len())
			}
			for _, h := range []string{"Content-Type", "Content-Encoding", "Content-Security-Policy", "Cache-Control", "Vary"} {
				if g, hd := get.Header().Get(h), head.Header().Get(h); g != hd {
					t.Errorf("%s: GET %q, HEAD %q", h, g, hd)
				}
			}
		})
	}
}
