// Package webui serve o painel React (web/) embutido no binário, na mesma
// origem da API: o cookie de refresh é SameSite=Strict com Path=/api/v1/auth,
// e o painel precisa estar no mesmo site (contrato dos Planos 5, 6 e 7).
//
// Este pacote só sabe servir arquivos. A cadeia de middlewares (RequestID,
// Recover, SecurityHeaders) e a montagem na raiz ficam em httpapi/routes.go.
package webui

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// Marcador é o valor de html.cspNonce em web/vite.config.ts. O Vite o põe
// nos <script> e <link> do index.html e num <meta property="csp-nonce">; o
// handler troca cada ocorrência pelo nonce da resposta.
const Marcador = "VIGILUME_CSP_NONCE"

// O dist inteiro, com o prefixo all: por dois motivos. Antes do primeiro
// build, dist/ só tem o .gitkeep, e sem all: o compilador recusa o diretório
// ("contains no embeddable files"). E o Rolldown pode gerar chunks cujo nome
// começa com "_", que o embed sem all: descartaria em silêncio.
//
//go:embed all:dist
var dist embed.FS

// Dist é o painel embutido, com dist/ como raiz.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" é um literal válido; só falha com o embed quebrado
	}
	return sub
}

const (
	cacheImutavel = "public, max-age=31536000, immutable"
	cacheRevalida = "no-cache"
	cacheNenhum   = "no-store"
	// gzipMinimo: abaixo disso o cabeçalho do gzip come o ganho.
	gzipMinimo = 1 << 10
)

// tipos é a tabela de MIME do painel. Não é mime.TypeByExtension: a tabela
// embutida do Go não tem .woff2, .webmanifest nem .map, e o que completa a
// lista numa máquina de desenvolvimento é o /etc/mime.types, que a imagem
// distroless não tem. Extensão fora daqui sai como application/octet-stream,
// que com nosniff nunca executa.
var tipos = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".html":        "text/html; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/vnd.microsoft.icon",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

// comprimiveis são os tipos que o gzip reduz; imagens e fontes já vêm
// comprimidas.
var comprimiveis = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".html": true, ".json": true,
	".map": true, ".webmanifest": true, ".svg": true, ".txt": true,
}

// TipoDe devolve o Content-Type que o painel usa para um nome de arquivo.
func TipoDe(nome string) string {
	if t, ok := tipos[strings.ToLower(path.Ext(nome))]; ok {
		return t
	}
	return "application/octet-stream"
}

type arquivo struct {
	corpo []byte
	gz    []byte // nil quando não compensa
	tipo  string
}

// Painel é o handler. Tudo é lido e comprimido uma vez, em New: servir é só
// escrever bytes que já estão em memória.
type Painel struct {
	arquivos map[string]arquivo // chave sem a barra inicial: "assets/index-x.js"
	index    []byte             // o index.html com o marcador
	// indisponivel, quando não é vazio, é o motivo de o painel responder
	// 503 no lugar do index: build ausente ou build sem o marcador.
	indisponivel string
}

// New lê o painel de fsys. Nunca falha: um dist sem index.html (go build
// sem npm run build) ou sem o marcador vira um painel que responde 503, e a
// API continua de pé.
func New(fsys fs.FS) *Painel {
	p := &Painel{arquivos: map[string]arquivo{}}
	err := fs.WalkDir(fsys, ".", func(nome string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || escondido(nome) {
			return err
		}
		corpo, err := fs.ReadFile(fsys, nome)
		if err != nil {
			return err
		}
		a := arquivo{corpo: corpo, tipo: TipoDe(nome)}
		if comprimiveis[strings.ToLower(path.Ext(nome))] && len(corpo) >= gzipMinimo {
			a.gz = comprimir(corpo)
		}
		p.arquivos[nome] = a
		return nil
	})
	if err != nil {
		slog.Error("painel: ler os arquivos embutidos", "erro", err)
		p.indisponivel = "O painel não pôde ser lido."
		return p
	}
	idx, ok := p.arquivos["index.html"]
	switch {
	case !ok:
		slog.Warn("painel ausente: o binário foi compilado sem npm run build em web/")
		p.indisponivel = "O painel não foi incluído neste binário (rode npm run build em web/ antes do go build)."
	case !bytes.Contains(idx.corpo, []byte(Marcador)):
		// Servir este index seria servir um painel cujo gráfico some: sem
		// nonce, os <style> que o Radix e o shadcn injetam são bloqueados.
		slog.Error("painel inválido: index.html sem o marcador de nonce (html.cspNonce no vite.config.ts)")
		p.indisponivel = "O painel foi compilado sem o marcador de nonce; refaça o build."
	default:
		p.index = idx.corpo
	}
	return p
}

func comprimir(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression) // nível válido: não falha
	_, _ = zw.Write(b)                                       // bytes.Buffer não falha
	_ = zw.Close()
	if buf.Len() >= len(b) {
		return nil
	}
	return buf.Bytes()
}

// escondido: algum segmento começa com ".". New não carrega esses arquivos,
// e é isso que tira o .gitkeep (e qualquer arquivo oculto que um build venha
// a deixar) do que o painel serve: o que não está no mapa é 404.
func escondido(nome string) bool {
	for _, seg := range strings.Split(nome, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return true
		}
	}
	return false
}

func (p *Painel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		texto(w, http.StatusMethodNotAllowed, "Método não permitido.")
		return
	}
	caminho := path.Clean("/" + r.URL.Path)
	nome := strings.TrimPrefix(caminho, "/")
	switch {
	case nome == "index.html":
		// O arquivo cru traz o marcador no lugar do nonce: nunca sai assim.
		p.servirIndex(w, r)
	case strings.HasPrefix(nome, "assets/"):
		// Um asset que não existe é 404, e nunca o index: depois de um
		// deploy, uma aba antiga pede o chunk velho, e o HTML no lugar dele
		// seria barrado pelo nosniff sem deixar pista. Com 404, o import()
		// falha e o painel oferece recarregar.
		p.servirArquivo(w, r, nome, cacheImutavel)
	case path.Ext(nome) != "":
		p.servirArquivo(w, r, nome, cacheRevalida)
	default:
		// Fallback de SPA: todo caminho sem extensão é rota do painel. A
		// API, a página pública e a verificação nem chegam aqui: o ServeMux
		// da raiz as manda para as cadeias delas.
		p.servirIndex(w, r)
	}
}

func (p *Painel) servirIndex(w http.ResponseWriter, r *http.Request) {
	if p.indisponivel != "" {
		texto(w, http.StatusServiceUnavailable, p.indisponivel)
		return
	}
	nonce, err := crypto.NewCSPNonce()
	if err != nil {
		slog.Error("painel: sortear o nonce", "erro", err)
		texto(w, http.StatusServiceUnavailable, "Painel indisponível.")
		return
	}
	corpo := bytes.ReplaceAll(p.index, []byte(Marcador), []byte(nonce))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", Politica(nonce))
	// no-store, e não no-cache: o corpo traz o nonce desta resposta. Num
	// cache compartilhado, todos receberiam o mesmo nonce.
	h.Set("Cache-Control", cacheNenhum)
	h.Set("Content-Length", strconv.Itoa(len(corpo)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(corpo)
	}
}

// Politica é a CSP do painel (spec §5.7, spec do Plano 6 §5.3). O nonce vale
// só para estilo: não há script inline, e nonce em script-src seria uma porta
// a mais. Sem style-src-attr: React, Radix e Recharts escrevem estilo pelo
// CSSOM, que a CSP não governa; só <style> injetado precisa do nonce.
func Politica(nonce string) string {
	return "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}

func (p *Painel) servirArquivo(w http.ResponseWriter, r *http.Request, nome, cache string) {
	a, ok := p.arquivos[nome]
	if !ok {
		texto(w, http.StatusNotFound, "Arquivo não encontrado.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.tipo)
	h.Set("Cache-Control", cache)
	corpo := a.corpo
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if aceitaGzip(r.Header.Get("Accept-Encoding")) {
			h.Set("Content-Encoding", "gzip")
			corpo = a.gz
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(corpo)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(corpo)
	}
}

// aceitaGzip lê Accept-Encoding sem cerimônia: "gzip" presente e não
// recusado com q=0.
func aceitaGzip(v string) bool {
	for _, parte := range strings.Split(v, ",") {
		nome, params, _ := strings.Cut(strings.TrimSpace(parte), ";")
		if !strings.EqualFold(strings.TrimSpace(nome), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

func texto(w http.ResponseWriter, status int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", cacheNenhum)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}
