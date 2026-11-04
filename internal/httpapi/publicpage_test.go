package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// paginaDeTeste monta a projeção de cinco serviços com nomes longos e o
// histórico cheio; dia(i) decide o uptime do dia i de cada barra.
func paginaDeTeste(dia func(i int) *float64) statuspage.View {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	u := 0.99912
	lat := int32(143)
	v := statuspage.View{Title: "Status da Empresa Exemplo de Serviços Online", GeneratedAt: agora}
	for i := range 5 {
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

// Spec §10.1: resposta abaixo de 10 KB, com cinco serviços. O pior caso
// tem os 90 dias espalhados por igual nas quatro classes, e a codificação
// pela maioria quase não ajuda; o caso comum (barra de uma cor só) fica bem
// abaixo. Com os 20 serviços que a página aceita, passa dos 10 KB: limite
// conhecido, registrado no plano.
func TestPublicPageFitsInTenKilobytes(t *testing.T) {
	valores := []*float64{nil, ptr(1), ptr(0.995), ptr(0.5)} // n, verde, d, f
	pior := renderizar(t, paginaDeTeste(func(i int) *float64 { return valores[i%4] }))
	if n := len(pior); n >= 10*1024 {
		t.Errorf("pior caso: %d bytes, queria menos de 10 KB", n)
	}
	if c := strings.Count(pior, "<li"); c != 5*statuspage.Days+5+statuspage.MaxIncidents {
		t.Errorf("%d <li>, queria %d (dias, serviços e incidentes)", c, 5*statuspage.Days+5+statuspage.MaxIncidents)
	}
	comum := renderizar(t, paginaDeTeste(func(int) *float64 { return ptr(1) }))
	if n := len(comum); n >= 7*1024 {
		t.Errorf("caso comum: %d bytes, queria menos de 7 KB", n)
	}
	t.Logf("pior caso %d bytes; caso comum %d bytes", len(pior), len(comum))
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
