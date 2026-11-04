package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/webui"
)

// A status page pública (spec §10): HTML renderizado aqui, sem JavaScript,
// com o CSS inline autorizado por nonce e as fontes do próprio host
// (/fonts/). É a página que se abre quando algo quebrou; ela não pode
// depender de bundle, hidratação nem API.

//go:embed templates/status.html
var templatesFS embed.FS

var statusTemplate = template.Must(template.ParseFS(templatesFS, "templates/status.html"))

const (
	// A resposta é pública e a mesma para todos: 30s no cache do navegador
	// e de qualquer proxy (spec §10.1), igual ao cache da projeção.
	cachePublico = "public, max-age=30"
	// O limite por IP é folgado: o visitante de uma página em incidente
	// recarrega. Com TRUSTED_PROXIES vazio atrás do proxy do Fly, todos os
	// visitantes dividem um balde, e a página cai no pior momento (ver
	// pendência do Plano 5).
	publicPorMinuto = 120
	publicBurst     = 60
)

// paginaPublica é a cadeia de /p/{slug}, separada da API como a de
// /verify/: sem CORS, sem RequireCustomHeader, e com a CSP e o cache
// próprios desta página.
func paginaPublica(ctx context.Context, cfg *config.Config, clk clock.Clock, svc *statuspage.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{slug}", (&PublicPageHandlers{svc: svc}).Page)

	var h http.Handler = mux
	h = middleware.RateLimit(ctx, clk, publicPorMinuto, publicBurst, proxiesDe(cfg))(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.Deadline(requestTimeout)(h)
	h = middleware.RequestID(h)
	return h
}

type PublicPageHandlers struct {
	svc *statuspage.Service
}

func (h *PublicPageHandlers) Page(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Public(r.Context(), r.PathValue("slug"))
	switch {
	case errors.Is(err, statuspage.ErrNotFound):
		// Inexistente e rascunho são a mesma resposta, byte a byte.
		pagina(w, http.StatusNotFound, paginaNaoAchada)
		return
	case err != nil:
		slog.Error("status page pública", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}

	nonce, err := crypto.NewCSPNonce()
	if err != nil {
		slog.Error("nonce da status page", "erro", err)
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}
	var buf bytes.Buffer
	if err := statusTemplate.Execute(&buf, apresentar(v, nonce)); err != nil {
		slog.Error("renderizar status page", "erro", err, "trace_id", observability.TraceID(r.Context()))
		pagina(w, http.StatusServiceUnavailable, paginaIndisponivel)
		return
	}
	corpo := buf.Bytes()
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", politicaDaPagina(nonce))
	hd.Set("Cache-Control", cachePublico)
	// O pior caso passa de 10 KB em texto puro; comprimido, fica perto de
	// 2 KB (spec §10.1). Vary: o cache público guarda as duas versões.
	hd.Add("Vary", "Accept-Encoding")
	if webui.AceitaGzip(r.Header.Get("Accept-Encoding")) {
		hd.Set("Content-Encoding", "gzip")
		corpo = comprimida(corpo)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(corpo)
}

// comprimida devolve o corpo em gzip. O nonce muda a cada resposta, então
// não há o que guardar: comprime a cada vez, no nível rápido.
func comprimida(b []byte) []byte {
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, gzip.BestSpeed) // nível válido: não falha
	_, _ = zw.Write(b)                                 // bytes.Buffer não falha
	_ = zw.Close()
	return out.Bytes()
}

// politicaDaPagina é a CSP da página pública: o <style> com o nonce desta
// resposta e as fontes do próprio host (/fonts/, servidas pelo painel na
// mesma origem). Nada de script, imagem nem conexão.
func politicaDaPagina(nonce string) string {
	return "default-src 'none'; style-src 'nonce-" + nonce + "'; font-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
}

// As páginas de erro são texto fixo e saem com a CSP de /verify/ (sem
// estilo) e o no-store de SecurityHeaders: uma página recém-publicada não
// pode ficar presa num 404 em cache.
const (
	paginaNaoAchada = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Página não encontrada</title>` +
		`<h1>Página não encontrada</h1><p>Confira o endereço com quem o enviou.</p></html>`
	paginaIndisponivel = `<!doctype html><html lang="pt-BR"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width"><title>Indisponível</title>` +
		`<h1>Status indisponível</h1><p>Tente de novo em instantes.</p></html>`
)

// O modelo de apresentação: só strings prontas. O template não formata,
// não decide e não tem acesso a nada além disto.
type paginaModelo struct {
	Nonce, Title, Generated string
	Band                    bandModelo
	Items                   []itemModelo
	Incidents               []string
}

// bandModelo é a faixa de estado do topo (spec do redesign, Telas 6): a
// frase, o detalhe com os nomes por estado e a classe da cor (o estado).
type bandModelo struct{ State, Text, Detail string }

type itemModelo struct {
	Name, State, Label, Uptime, Latency string
	// Bar é a classe da maioria dos dias, na <ol>; Days tem a classe só dos
	// dias diferentes dela, e "" nos outros. Um dia sem atributo custa 4
	// bytes, e com atributo, 14: codificar pela maioria mantém o texto puro
	// abaixo do teto de 16 KB e o comprimido bem abaixo dos 10 KB (spec
	// §10.1). "" em Bar é verde.
	Bar  string
	Days [statuspage.Days]string
}

func apresentar(v statuspage.View, nonce string) paginaModelo {
	m := paginaModelo{
		Nonce: nonce, Title: v.Title, Band: faixa(v.Items),
		Generated: v.GeneratedAt.UTC().Format("02/01/2006 15:04"),
		Items:     make([]itemModelo, 0, len(v.Items)),
		Incidents: make([]string, 0, len(v.Incidents)),
	}
	for _, it := range v.Items {
		im := itemModelo{Name: it.Name, State: string(it.State), Label: rotulo(it.State), Uptime: "Sem dados nos últimos 90 dias"}
		if it.Uptime90 != nil {
			im.Uptime = porcento(*it.Uptime90) + " no ar em 90 dias"
		}
		if it.LatencyMS != nil {
			im.Latency = fmt.Sprintf("latência típica %d ms", *it.LatencyMS)
		}
		im.Bar, im.Days = barra(it.Days)
		m.Items = append(m.Items, im)
	}
	for _, x := range v.Incidents {
		m.Incidents = append(m.Incidents, fmt.Sprintf("%s: %s a %s (%s)", x.Name,
			x.StartedAt.Format("02/01/2006 15:04"), x.ResolvedAt.Format("02/01 15:04"), duracao(x.Duration())))
	}
	return m
}

// gravidade é a ordem da faixa: o primeiro estado presente dá a frase.
var gravidade = []statuspage.State{statuspage.StateDown, statuspage.StateDegraded, statuspage.StateUnknown}

// faixa diz o estado da página numa frase, pelo pior estado presente, e
// lista os nomes por estado, do pior ao melhor, na ordem da página. Os casos
// ficam em testdata/faixa.json, que a prévia do painel também lê.
func faixa(items []statuspage.PublicItem) bandModelo {
	if len(items) == 0 {
		return bandModelo{State: string(statuspage.StateUnknown), Text: "Nenhum serviço nesta página."}
	}
	nomes := map[statuspage.State][]string{}
	for _, it := range items {
		nomes[it.State] = append(nomes[it.State], it.Name)
	}
	b := bandModelo{State: string(statuspage.StateUp), Text: "Todos os sistemas funcionando."}
	var partes []string
	for _, s := range gravidade {
		n := nomes[s]
		if len(n) == 0 {
			continue
		}
		if len(partes) == 0 {
			b.State, b.Text = string(s), fraseDaFaixa(s, len(n))
		}
		partes = append(partes, rotulo(s)+": "+strings.Join(n, ", ")+".")
	}
	b.Detail = strings.Join(partes, " ")
	return b
}

func fraseDaFaixa(s statuspage.State, n int) string {
	servicos := "1 serviço"
	if n != 1 {
		servicos = fmt.Sprintf("%d serviços", n)
	}
	switch s {
	case statuspage.StateDown:
		return servicos + " fora do ar."
	case statuspage.StateDegraded:
		return "Instabilidade em " + servicos + "."
	}
	return "Sem dados recentes de " + servicos + "."
}

func rotulo(s statuspage.State) string {
	switch s {
	case statuspage.StateUp:
		return "No ar"
	case statuspage.StateDegraded:
		return "Lento"
	case statuspage.StateDown:
		return "Fora do ar"
	}
	return "Sem dados"
}

// classeDia: no ar (sem classe, altura cheia) a partir de 99,9%, instável
// ("d", dois terços da altura) a partir de 99%, fora do ar ("f", um terço)
// abaixo, e sem dados ("n", só o contorno) sem check. Com checks de minuto
// em minuto, 99,9% de um dia é 1,4 check: uma sonda perdida não pinta o dia.
func classeDia(u *float64) string {
	switch {
	case u == nil:
		return "n"
	case *u >= 0.999:
		return ""
	case *u >= 0.99:
		return "d"
	}
	return "f"
}

// barra codifica os dias pela classe mais comum (ver itemModelo). Com a
// maioria verde, a <ol> fica sem classe e os verdes, sem atributo; com
// outra maioria, um dia verde precisa de "g" explícito.
func barra(dias [statuspage.Days]statuspage.Day) (string, [statuspage.Days]string) {
	var classes [statuspage.Days]string
	conta := map[string]int{}
	for i, d := range dias {
		classes[i] = classeDia(d.Uptime)
		conta[classes[i]]++
	}
	maioria := ""
	for _, c := range []string{"n", "d", "f"} { // empate: verde fica
		if conta[c] > conta[maioria] {
			maioria = c
		}
	}
	var out [statuspage.Days]string
	for i, c := range classes {
		switch {
		case c == maioria:
		case c == "":
			out[i] = "g"
		default:
			out[i] = c
		}
	}
	return maioria, out
}

// porcento trunca em duas casas: 99,999% não pode virar "100,00%" num dia
// com falha.
func porcento(u float64) string {
	v := math.Floor(u*10000) / 100
	return strings.Replace(fmt.Sprintf("%.2f%%", v), ".", ",", 1)
}

func duracao(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "menos de 1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d d %d h", int(d.Hours())/24, int(d.Hours())%24)
}
