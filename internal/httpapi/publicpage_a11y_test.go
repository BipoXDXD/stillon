package httpapi

import (
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Acessibilidade e forma da página pública, lidas do próprio template nos
// dois esquemas (claro e prefers-color-scheme: dark). As cores são os
// tokens do painel (web/src/index.css), em OKLCH: o teste converte para
// sRGB, como o src/tokens.test.ts do painel, e aplica a fórmula do WCAG 2.
// A barra de 90 dias é objeto gráfico: cada dia precisa de 3:1 contra o
// cartão e contra a página (WCAG 1.4.11), e a cor não é o único sinal
// (1.4.1): cada classe de dia tem forma própria, a altura ou só o contorno.

type tema string

const (
	claro  tema = "claro"
	escuro tema = "escuro"
)

var temas = []tema{claro, escuro}

type oklch struct{ l, c, h float64 }

// cssDaPagina é o <style> do template: o texto, os tokens de cada tema (o
// escuro já com o que herda do claro), as regras de fora de @media e
// @font-face, por seletor, e as que só valem na tela larga
// (@media(min-width:40rem)).
type cssDaPagina struct {
	texto  string
	tokens map[tema]map[string]string
	regras map[string]map[string]string
	largas map[string]map[string]string
}

// telaLarga é a largura a partir da qual o dia sem dados vira contorno; o
// e2e mede as duas formas a 400 e a 1280 px.
const telaLarga = "@media(min-width:40rem){"

var (
	estiloRE         = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
	tokenRE          = regexp.MustCompile(`--([\w-]+):\s*([^;}]+)`)
	tokensClarosRE   = regexp.MustCompile(`(?m)^:root\{([^}]*)\}`)
	tokensEscurosRE  = regexp.MustCompile(`@media\(prefers-color-scheme:dark\)\{:root\{([^}]*)\}\}`)
	blocoComChavesRE = regexp.MustCompile(`@(?:media|font-face)[^{]*\{(?:[^{}]*\{[^}]*\})*[^{}]*\}`)
	regraCSS         = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	varRE            = regexp.MustCompile(`^var\(--([\w-]+)\)$`)
	oklchRE          = regexp.MustCompile(`^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$`)
)

func lerCSSDaPagina(t *testing.T) cssDaPagina {
	t.Helper()
	fonte, err := templatesFS.ReadFile("templates/status.html")
	if err != nil {
		t.Fatal(err)
	}
	estilo := estiloRE.FindSubmatch(fonte)
	if estilo == nil {
		t.Fatal("template sem <style>")
	}
	texto := string(estilo[1])
	claros, escuros := tokensClarosRE.FindStringSubmatch(texto), tokensEscurosRE.FindStringSubmatch(texto)
	if claros == nil || escuros == nil {
		t.Fatal("template sem os tokens do claro (:root{…} no começo da linha) ou do escuro (@media(prefers-color-scheme:dark){:root{…}})")
	}
	c := cssDaPagina{
		texto:  texto,
		tokens: comHeranca(declaracoesDeTokens(claros[1]), declaracoesDeTokens(escuros[1])),
		regras: map[string]map[string]string{},
		largas: map[string]map[string]string{},
	}
	juntarRegras(c.regras, blocoComChavesRE.ReplaceAllString(texto, ""))
	for _, m := range blocoLargoRE.FindAllStringSubmatch(texto, -1) {
		juntarRegras(c.largas, m[1])
	}
	return c
}

var blocoLargoRE = regexp.MustCompile(regexp.QuoteMeta(telaLarga) + `((?:[^{}]*\{[^}]*\})*)\}`)

func declaracoesDeTokens(bloco string) map[string]string {
	out := map[string]string{}
	for _, m := range tokenRE.FindAllStringSubmatch(bloco, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// comHeranca monta os dois temas: o escuro vive no mesmo documento que o
// claro, e o que ele não redefine vem do claro.
func comHeranca(claros, escuros map[string]string) map[tema]map[string]string {
	todos := maps.Clone(claros)
	maps.Copy(todos, escuros)
	return map[tema]map[string]string{claro: claros, escuro: todos}
}

func juntarRegras(regras map[string]map[string]string, css string) {
	for _, r := range regraCSS.FindAllStringSubmatch(css, -1) {
		for _, sel := range strings.Split(r[1], ",") {
			sel = strings.TrimSpace(sel)
			if regras[sel] == nil {
				regras[sel] = map[string]string{}
			}
			for _, decl := range strings.Split(r[2], ";") {
				if prop, valor, ok := strings.Cut(decl, ":"); ok {
					regras[sel][strings.TrimSpace(prop)] = strings.TrimSpace(valor)
				}
			}
		}
	}
}

// valor lê uma propriedade de uma regra do template; falta é erro.
func (c cssDaPagina) valor(t *testing.T, seletor, prop string) string {
	t.Helper()
	v := c.regras[seletor][prop]
	if v == "" {
		t.Fatalf("regra %q sem %s", seletor, prop)
	}
	return v
}

// cor segue var() até um oklch(L C H), com os tokens de um tema.
func cor(t *testing.T, tokens map[string]string, valor string) oklch {
	t.Helper()
	for range 10 {
		m := varRE.FindStringSubmatch(strings.TrimSpace(valor))
		if m == nil {
			break
		}
		v, ok := tokens[m[1]]
		if !ok {
			t.Fatalf("token --%s ausente", m[1])
		}
		valor = v
	}
	m := oklchRE.FindStringSubmatch(strings.TrimSpace(valor))
	if m == nil {
		t.Fatalf("%q não é oklch(L C H) nem var(--x)", valor)
	}
	numero := func(s string) float64 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%q: %v", valor, err)
		}
		return f
	}
	return oklch{numero(m[1]), numero(m[2]), numero(m[3])}
}

// srgbLinear: OKLCH → OKLab → sRGB linear, com as matrizes de Björn
// Ottosson, as mesmas do CSS Color 4 e do src/tokens.test.ts do painel. Um
// canal fora do gamut é cortado em [0, 1].
func srgbLinear(c oklch) [3]float64 {
	angulo := c.h * math.Pi / 180
	a, b := c.c*math.Cos(angulo), c.c*math.Sin(angulo)
	longo := math.Pow(c.l+0.3963377774*a+0.2158037573*b, 3)
	medio := math.Pow(c.l-0.1055613458*a-0.0638541728*b, 3)
	curto := math.Pow(c.l-0.0894841775*a-1.291485548*b, 3)
	cortar := func(x float64) float64 { return math.Min(1, math.Max(0, x)) }
	return [3]float64{
		cortar(4.0767416621*longo - 3.3077115913*medio + 0.2309699292*curto),
		cortar(-1.2684380046*longo + 2.6097574011*medio - 0.3413193965*curto),
		cortar(-0.0041960863*longo - 0.7034186147*medio + 1.707614701*curto),
	}
}

// luminancia é a luminância relativa do WCAG 2; o sRGB já está linear.
func luminancia(c oklch) float64 {
	r := srgbLinear(c)
	return 0.2126*r[0] + 0.7152*r[1] + 0.0722*r[2]
}

// contraste segue a definição de contrast ratio do WCAG 2.
func contraste(a, b oklch) float64 {
	la, lb := luminancia(a), luminancia(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// Cada classe de dia em duas regras: a da maioria, na <ol> ("" é a <ol> sem
// classe, só com a regra base "ol li"), e a explícita, no <li> (ver
// itemModelo.Bar).
var classesDeDia = []struct{ nome, maioria, explicita string }{
	{"no ar", "", "li.g"},
	{"instável", ".d li", "li.d"},
	{"fora do ar", ".f li", "li.f"},
	{"sem dados", ".n li", "li.n"},
}

// forma de um dia: a altura, o preenchimento, o contorno (box-shadow interno,
// "" sem contorno) e a borda, que nenhum dia pode ter: com flex:1, a borda
// alarga o dia (a 400 px, 3,19 px contra 2,72 px dos outros).
type forma struct{ altura, fundo, contorno, borda string }

func (f forma) contornada() bool { return f.fundo == "none" && f.contorno != "" }

// corDaForma é o preenchimento, ou a cor do contorno do dia sem dados.
func (f forma) corDaForma() string {
	if f.contornada() {
		campos := strings.Fields(f.contorno)
		return campos[len(campos)-1]
	}
	return f.fundo
}

// larguras: na estreita (o celular, abaixo de 40rem) cada dia tem ~2,7 px, e
// um contorno de 1 px fecharia o miolo; lá, o sem dados é um traço baixo e
// cheio. Na larga, ele é o contorno da spec.
var larguras = []struct {
	nome  string
	larga bool
}{{"estreita", false}, {"larga", true}}

// formaDoDia aplica, em ordem, a regra base, a da maioria da <ol> e, se o dia
// não é da maioria, a explícita; na tela larga, cada uma seguida da sua versão
// de @media(min-width:40rem). Todas têm a mesma especificidade, e vence a
// última no texto (TestPublicPageDaysHaveTheirOwnShape confere a ordem).
func (c cssDaPagina) formaDoDia(dia, barra int, larga bool) forma {
	d := map[string]string{}
	aplicar := func(seletor string) {
		maps.Copy(d, c.regras[seletor])
		if larga {
			maps.Copy(d, c.largas[seletor])
		}
	}
	aplicar("ol li")
	aplicar(classesDeDia[barra].maioria)
	if dia != barra {
		aplicar(classesDeDia[dia].explicita)
	}
	contorno := d["box-shadow"]
	if contorno == "none" {
		contorno = ""
	}
	borda := d["border"]
	if borda == "0" || borda == "none" {
		borda = ""
	}
	return forma{altura: d["height"], fundo: d["background"], contorno: contorno, borda: borda}
}

// alturaEmPorcento lê "66%"; outra unidade é erro.
func alturaEmPorcento(t *testing.T, f forma) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(strings.TrimSuffix(f.altura, "%"), 64)
	if err != nil || !strings.HasSuffix(f.altura, "%") {
		t.Fatalf("altura %q não é porcentagem", f.altura)
	}
	return v
}

func TestPublicPageDaysHaveTheirOwnShape(t *testing.T) {
	c := lerCSSDaPagina(t)
	ultimaDaMaioria, primeiraExplicita := -1, len(c.texto)
	for _, d := range classesDeDia {
		if d.maioria != "" {
			ultimaDaMaioria = max(ultimaDaMaioria, strings.Index(c.texto, "\n"+d.maioria+"{"))
			if _, temLarga := c.largas[d.maioria]; temLarga {
				ultimaDaMaioria = max(ultimaDaMaioria, strings.Index(c.texto, "\n"+telaLarga+d.maioria+"{"))
			}
		}
		primeiraExplicita = min(primeiraExplicita, strings.Index(c.texto, "\n"+d.explicita+"{"))
	}
	if ultimaDaMaioria < 0 || primeiraExplicita < 0 || ultimaDaMaioria > primeiraExplicita {
		t.Fatalf("as regras explícitas (li.g, li.d…) vêm depois das da maioria (.d li…, inclusive as de tela larga), "+
			"cada uma no começo da linha: %d, %d", ultimaDaMaioria, primeiraExplicita)
	}
	for _, l := range larguras {
		t.Run(l.nome, func(t *testing.T) {
			semCor := map[string]string{}
			for i, d := range classesDeDia {
				f := c.formaDoDia(i, i, l.larga)
				for j, barra := range classesDeDia {
					if g := c.formaDoDia(i, j, l.larga); g != f {
						t.Errorf("dia %s numa barra de maioria %s desenha %+v, e não %+v", d.nome, barra.nome, g, f)
					}
				}
				if f.altura == "" || f.fundo == "" {
					t.Errorf("dia %s sem altura ou sem fundo: %+v", d.nome, f)
				}
				if f.borda != "" {
					t.Errorf("dia %s com border %q: a borda alarga o dia; o contorno é box-shadow inset", d.nome, f.borda)
				}
				chave := f.altura + " " + strconv.FormatBool(f.contornada())
				if outro, repetida := semCor[chave]; repetida {
					t.Errorf("dias %s e %s têm a mesma forma (%s): a cor seria o único sinal", d.nome, outro, chave)
				}
				semCor[chave] = d.nome
				if contornada := f.contornada(); contornada != (l.larga && d.nome == "sem dados") {
					t.Errorf("dia %s: contorno = %v; só o sem dados, e só na tela larga, é só o contorno", d.nome, contornada)
				}
			}
			if l.larga {
				return
			}
			// Na estreita, o sem dados é o traço mais baixo de todos.
			semDados := alturaEmPorcento(t, c.formaDoDia(3, 3, false))
			for i, d := range classesDeDia[:3] {
				if outra := alturaEmPorcento(t, c.formaDoDia(i, i, false)); semDados >= outra/2 {
					t.Errorf("na tela estreita, o sem dados (%v%%) não é bem mais baixo que o %s (%v%%)", semDados, d.nome, outra)
				}
			}
		})
	}
}

func TestPublicPageColorsAreAccessible(t *testing.T) {
	c := lerCSSDaPagina(t)
	for _, tm := range temas {
		t.Run(string(tm), func(t *testing.T) {
			tokens := c.tokens[tm]
			corDe := func(sel, prop string) oklch { return cor(t, tokens, c.valor(t, sel, prop)) }
			fundos := map[string]oklch{"página": corDe("body", "background"), "cartão": corDe(".s>li", "background")}
			for nomeDoFundo, fundo := range fundos {
				for _, sel := range []string{"body", ".up", ".degraded", ".down", ".unknown", ".m", ".l", "footer"} {
					if r := contraste(corDe(sel, "color"), fundo); r < 4.5 {
						t.Errorf("texto %s sobre o fundo da %s: %.2f:1, queria 4,5:1", sel, nomeDoFundo, r)
					}
				}
				for _, l := range larguras {
					for i, d := range classesDeDia {
						if r := contraste(cor(t, tokens, c.formaDoDia(i, i, l.larga).corDaForma()), fundo); r < 3 {
							t.Errorf("dia %s, tela %s, sobre o fundo da %s: %.2f:1, queria 3:1", d.nome, l.nome, nomeDoFundo, r)
						}
					}
				}
			}
			for _, e := range []string{"up", "degraded", "down", "unknown"} {
				if r := contraste(corDe("."+e, "color"), corDe(".b."+e, "background")); r < 4.5 {
					t.Errorf("faixa %s: %.2f:1, queria 4,5:1", e, r)
				}
			}
		})
	}
}

// A página usa os tokens do painel, nos dois temas. Um token do template que
// não existe no index.css, ou com outro valor, quebra aqui.
func TestPublicPageUsesThePanelTokens(t *testing.T) {
	c := lerCSSDaPagina(t)
	painel := lerTokensDoPainel(t)
	for _, tm := range temas {
		for nome := range c.tokens[tm] {
			if _, ok := painel[tm][nome]; !ok {
				t.Errorf("%s: --%s não existe no web/src/index.css", tm, nome)
				continue
			}
			daPagina, doPainel := cor(t, c.tokens[tm], "var(--"+nome+")"), cor(t, painel[tm], "var(--"+nome+")")
			if math.Abs(daPagina.l-doPainel.l) > 1e-9 || math.Abs(daPagina.c-doPainel.c) > 1e-9 || math.Abs(daPagina.h-doPainel.h) > 1e-9 {
				t.Errorf("%s: --%s é %+v na página e %+v no painel", tm, nome, daPagina, doPainel)
			}
		}
	}
}

// lerTokensDoPainel lê os blocos :root e .dark do index.css do painel.
func lerTokensDoPainel(t *testing.T) map[tema]map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "index.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	bloco := func(seletor string) string {
		i := strings.Index(css, "\n"+seletor+" {")
		if i < 0 {
			t.Fatalf("web/src/index.css sem o bloco %q", seletor)
		}
		return css[i : i+strings.Index(css[i:], "}")]
	}
	return comHeranca(declaracoesDeTokens(bloco(":root")), declaracoesDeTokens(bloco(".dark")))
}

// O template só aponta fontes do próprio host, que é o que
// o font-src 'self' da politicaDaPagina libera.
func TestTemplateFontsComeFromTheOwnHost(t *testing.T) {
	c := lerCSSDaPagina(t)
	urls := regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(c.texto, -1)
	if len(urls) != 2 {
		t.Fatalf("%d url() no template, queria as 2 fontes", len(urls))
	}
	for _, u := range urls {
		if !strings.HasPrefix(u[1], "/fonts/") || !strings.HasSuffix(u[1], ".woff2") {
			t.Errorf("url(%s) fora de /fonts/*.woff2", u[1])
		}
	}
}

// A conversão e a fórmula conferem com os valores de referência: preto ×
// branco = 21:1; #767676, o cinza mais claro com 4,5:1 sobre branco, é
// oklch(0.5658 0 0); o vermelho sRGB puro tem luminância 0,2126.
func TestContrastFormulaMatchesReference(t *testing.T) {
	branco := oklch{1, 0, 0}
	if r := contraste(oklch{0, 0, 0}, branco); math.Abs(r-21) > 1e-6 {
		t.Errorf("preto x branco = %.4f, queria 21", r)
	}
	if r := contraste(oklch{0.5658, 0, 0}, branco); math.Abs(r-4.54) > 0.005 {
		t.Errorf("#767676 x branco = %.4f, queria 4,54", r)
	}
	if l := luminancia(oklch{0.62796, 0.25768, 29.2339}); math.Abs(l-0.2126) > 0.001 {
		t.Errorf("vermelho puro: luminância %.4f, queria 0,2126", l)
	}
}
