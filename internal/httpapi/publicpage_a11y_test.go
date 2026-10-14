package httpapi

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Acessibilidade das cores da página pública, lidas do próprio template nos
// dois esquemas (claro e prefers-color-scheme: dark). A barra de 90 dias é
// objeto gráfico: cada cor de dia precisa de 3:1 contra o fundo (WCAG
// 1.4.11), e a cor não pode ser o único sinal (1.4.1): verde e vermelho têm
// 1,37:1 entre si, então cada classe de dia tem uma altura própria. O texto
// de estado e o texto secundário precisam de 4,5:1.

// Cada classe de dia aparece em duas regras: a da maioria, na <ol>, e a
// explícita, no <li> (ver itemModelo.Bar). As duas têm de desenhar igual.
var classesDeDia = []struct{ nome, maioria, explicita string }{
	{"no ar", "ol li", "li.g"},
	{"lento", ".d li", "li.d"},
	{"fora do ar", ".f li", "li.f"},
	{"sem dados", ".n li", "li.n"},
}

var textosDaPagina = []string{".up", ".degraded", ".down", ".unknown", ".m"}

func TestPublicPageColorsAreAccessible(t *testing.T) {
	claro, escuro := regrasDoTemplate(t)
	for _, tema := range []struct {
		nome   string
		regras map[string]map[string]string
	}{{"claro", claro}, {"escuro", escuro}} {
		t.Run(tema.nome, func(t *testing.T) {
			fundo := tema.regras["body"]["background"]
			alturas := map[string]string{}
			for _, c := range classesDeDia {
				m, e := tema.regras[c.maioria], tema.regras[c.explicita]
				if m["background"] != e["background"] || m["height"] != e["height"] {
					t.Errorf("%s: %q desenha %v e %q desenha %v", c.nome, c.maioria, m, c.explicita, e)
				}
				if r := contraste(t, e["background"], fundo); r < 3 {
					t.Errorf("barra %s (%s) sobre %s: %.2f:1, queria 3:1", c.nome, e["background"], fundo, r)
				}
				if outra, repetida := alturas[e["height"]]; repetida || e["height"] == "" {
					t.Errorf("barra %s sem altura própria (%q, igual a %q): a cor seria o único sinal", c.nome, e["height"], outra)
				}
				alturas[e["height"]] = c.nome
			}
			for _, sel := range textosDaPagina {
				cor := tema.regras[sel]["color"]
				if r := contraste(t, cor, fundo); r < 4.5 {
					t.Errorf("texto %s (%s) sobre %s: %.2f:1, queria 4,5:1", sel, cor, fundo, r)
				}
			}
		})
	}
}

// regrasDoTemplate devolve, para cada seletor, as declarações efetivas no
// tema claro e no escuro (o claro com o bloco do @media por cima).
func regrasDoTemplate(t *testing.T) (claro, escuro map[string]map[string]string) {
	t.Helper()
	fonte, err := templatesFS.ReadFile("templates/status.html")
	if err != nil {
		t.Fatal(err)
	}
	estilo := regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`).FindSubmatch(fonte)
	if estilo == nil {
		t.Fatal("template sem <style>")
	}
	css := string(estilo[1])
	const media = "@media(prefers-color-scheme:dark){"
	i := strings.Index(css, media)
	if i < 0 {
		t.Fatal("template sem o bloco do tema escuro")
	}
	fim := strings.LastIndex(css, "}")
	claro = map[string]map[string]string{}
	juntarRegras(claro, css[:i])
	escuro = map[string]map[string]string{}
	juntarRegras(escuro, css[:i])
	juntarRegras(escuro, css[i+len(media):fim])
	return claro, escuro
}

var regraCSS = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)

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

// contraste segue a definição de contrast ratio do WCAG 2.
func contraste(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminancia(t, a), luminancia(t, b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func luminancia(t *testing.T, hex string) float64 {
	t.Helper()
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		t.Fatalf("cor %q fora do formato #rgb ou #rrggbb", hex)
	}
	canal := func(i int) float64 {
		v, err := strconv.ParseUint(h[i:i+2], 16, 8)
		if err != nil {
			t.Fatalf("cor %q: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*canal(0) + 0.7152*canal(2) + 0.0722*canal(4)
}

// A fórmula do teste confere com os valores de referência: preto x branco
// = 21:1; #767676 sobre branco = 4,54:1.
func TestContrastFormulaMatchesReference(t *testing.T) {
	if r := contraste(t, "#000", "#fff"); math.Abs(r-21) > 1e-9 {
		t.Errorf("preto x branco = %.4f, queria 21", r)
	}
	if r := contraste(t, "#767676", "#ffffff"); math.Abs(r-4.54) > 0.005 {
		t.Errorf("#767676 x branco = %.4f, queria 4,54", r)
	}
}
