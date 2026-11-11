package prober

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// RegraDaSonda é a regra de conteúdo pronta para a sonda (spec §5.3): o valor
// em bytes e o ponteiro já decodificado em tokens. nil no Spec é alvo sem
// regra.
type RegraDaSonda struct {
	Mode    targets.ContentMode
	Value   []byte
	Pointer []string
}

// lerCorpo lê até MaxBodyBytes + 1 bytes: o byte a mais só diz que o corpo
// passou do teto, e nunca é julgado. O leitor já é o corpo descomprimido,
// então o teto vale também contra bomba de gzip. Bater no teto
// comprimido antes também é corpo cortado: ele não foi lido inteiro.
func lerCorpo(r io.Reader) (corpo []byte, cortado bool, err error) {
	corpo, err = io.ReadAll(io.LimitReader(r, safedial.MaxBodyBytes+1))
	cortado = errors.Is(err, errTetoComprimido)
	if err != nil && !cortado {
		return nil, false, err
	}
	if len(corpo) > safedial.MaxBodyBytes {
		return corpo[:safedial.MaxBodyBytes], true, nil
	}
	return corpo, cortado, nil
}

// tetoComprimido limita os bytes lidos da rede quando a sonda descomprime: o teto de lerCorpo vale sobre a saída, e um gzip
// que não gera saída (membros vazios, um atrás do outro) seria lido até o
// prazo. O deflate honesto nunca cresce mais que os 5 bytes de cada bloco
// stored de 64 KiB, então 64 KiB de saída pedem pouco mais que 64 KiB de
// entrada; o dobro dá folga para cabeçalhos e para o bufio do gzip.Reader.
const tetoComprimido = 2 * safedial.MaxBodyBytes

// errTetoComprimido diz que o corpo comprimido passou de tetoComprimido.
var errTetoComprimido = errors.New("prober: corpo comprimido passou do teto")

// leitorComTeto entrega até restante bytes e depois errTetoComprimido. Não é
// um io.LimitReader porque o EOF dele, no fim de um membro, pareceria o fim
// do gzip: quem lê precisa saber que parou pelo teto.
type leitorComTeto struct {
	r        io.Reader
	restante int64
}

func comTetoComprimido(r io.Reader) *leitorComTeto {
	return &leitorComTeto{r: r, restante: tetoComprimido}
}

func (l *leitorComTeto) Read(p []byte) (int, error) {
	if l.restante <= 0 {
		return 0, errTetoComprimido
	}
	if int64(len(p)) > l.restante {
		p = p[:l.restante]
	}
	n, err := l.r.Read(p)
	l.restante -= int64(n)
	return n, err
}

// lerCorpoDaRegra lê o corpo que a regra vai julgar. Só um corpo sem
// codificação ou com exatamente um gzip, somando todas as linhas de
// Content-Encoding, tem leitura; o resto devolve ilegivel, e a sonda sai
// content_encoding. O gzip é desfeito aqui, e não pelo
// transporte, que decide só pela primeira linha: com "gzip" e "br" em duas
// linhas, ele desfazia uma camada e apagava as duas. O teto de lerCorpo vale
// sobre o descomprimido. Corpo vazio não tem o que estar comprimido, e é
// julgado como vazio com qualquer codificação.
func lerCorpoDaRegra(resp *http.Response) (corpo []byte, cortado, ilegivel bool, err error) {
	leitor := bufio.NewReader(comTetoComprimido(resp.Body))
	if _, err := leitor.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, false, false, nil
		}
		return nil, false, false, err
	}
	switch codificacaoDoCorpo(resp.Header) {
	case semCodificacao:
		corpo, cortado, err = lerCorpo(leitor)
		return corpo, cortado, false, err
	case umGzip:
		// O maior cabeçalho gzip que o Go aceita (FEXTRA de 64 KiB, FNAME e
		// FCOMMENT de até 512 B) cabe no teto comprimido, então o erro daqui
		// nunca é o do teto.
		zr, err := gzip.NewReader(leitor)
		if errors.Is(err, gzip.ErrHeader) {
			// Declara gzip e não é: a causa é a codificação.
			return drenarIlegivel(leitor)
		}
		if err != nil {
			return nil, false, false, err
		}
		defer zr.Close()
		corpo, cortado, err = lerCorpo(zr)
		return corpo, cortado, false, err
	}
	return drenarIlegivel(leitor)
}

// drenarSemRegra lê até 64 KiB do corpo de um alvo sem regra em que a sonda
// pediu gzip, só pela latência e pelos erros de leitura: corpo maior que o
// limite não é falha. Com exatamente um gzip, o teto vale sobre o descomprimido, como
// quando o transporte do Go descomprimia: drenar o gzip cru
// leria da rede até 3× mais numa página grande. Um gzip que não abre é erro de
// leitura, e a sonda sai unknown, como saía com o descompressor do transporte.
// Bater no teto comprimido é só parar de drenar.
// Qualquer outra codificação é drenada crua, como o transporte fazia.
func drenarSemRegra(resp *http.Response) error {
	if codificacaoDoCorpo(resp.Header) != umGzip {
		return drenarCru(resp.Body)
	}
	zr, err := gzip.NewReader(comTetoComprimido(resp.Body))
	if errors.Is(err, io.EOF) {
		return nil // corpo vazio: não há o que descomprimir
	}
	if err == nil {
		defer zr.Close()
		_, err = io.Copy(io.Discard, io.LimitReader(zr, safedial.MaxBodyBytes))
	}
	if errors.Is(err, errTetoComprimido) {
		return nil // drenou o bastante, como no teto descomprimido
	}
	return err
}

// drenarCru lê até 64 KiB do corpo como chegou, sem descomprimir: é o caso
// do alvo sem regra cujo dono mandou Accept-Encoding ou Range, em que o
// transporte do Go também não descomprimia.
func drenarCru(r io.Reader) error {
	_, err := io.Copy(io.Discard, io.LimitReader(r, safedial.MaxBodyBytes))
	return err
}

// drenarIlegivel lê até o teto um corpo que a regra não julga: a latência e o
// erro de leitura (prazo, conexão caída) valem como nos outros casos.
func drenarIlegivel(r io.Reader) (corpo []byte, cortado, ilegivel bool, err error) {
	_, _, err = lerCorpo(r)
	return nil, false, true, err
}

type codificacao int

const (
	semCodificacao codificacao = iota
	umGzip
	outraCodificacao
)

// codificacaoDoCorpo soma os tokens de todas as linhas de Content-Encoding.
// identity é a ausência de codificação e não conta.
func codificacaoDoCorpo(h http.Header) codificacao {
	var tokens []string
	for _, linha := range h.Values("Content-Encoding") {
		for token := range strings.SplitSeq(linha, ",") {
			token = strings.TrimSpace(token)
			if token != "" && !strings.EqualFold(token, "identity") {
				tokens = append(tokens, token)
			}
		}
	}
	switch {
	case len(tokens) == 0:
		return semCodificacao
	case len(tokens) == 1 && strings.EqualFold(tokens[0], "gzip"):
		return umGzip
	}
	return outraCodificacao
}

// avaliarConteudo julga o corpo pela regra e devolve "" quando ela passa.
// Corpo cortado sem conclusão é body_limit: inconclusivo nunca vira up.
func avaliarConteudo(r RegraDaSonda, corpo []byte, cortado bool) ErrorKind {
	switch r.Mode {
	case targets.ContentContains:
		if bytes.Contains(corpo, r.Value) {
			return ""
		}
		return reprovadoOuCortado(cortado)
	case targets.ContentNotContains:
		if bytes.Contains(corpo, r.Value) {
			return KindContentMismatch
		}
		if cortado {
			return KindBodyLimit
		}
		return ""
	case targets.ContentJSONEquals:
		if cortado {
			return KindBodyLimit
		}
		if escalar, ok := escalarNoPonteiro(corpo, r.Pointer); ok && escalar == string(r.Value) {
			return ""
		}
		return KindContentMismatch
	}
	// O modo foi validado no cadastro; chegar aqui é defeito, e o resultado
	// honesto é "não deu para sondar", como na URL ilegível de Probe.
	return KindUnknown
}

func reprovadoOuCortado(cortado bool) ErrorKind {
	if cortado {
		return KindBodyLimit
	}
	return KindContentMismatch
}

// escalarNoPonteiro devolve a forma de texto do escalar que o ponteiro
// aponta: string decodificada, número como escrito (1.0 não é 1), e true,
// false e null literais. O corpo inteiro tem de ser UM valor JSON; JSON
// inválido, ponteiro ausente, objeto ou array devolvem ok = false.
//
// O corpo é validado uma vez (json.Valid, sem alocar) e a descida o percorre
// numa passada só, sem copiar: os irmãos fora do caminho são pulados byte a
// byte. Com a árvore inteira, ou com um Unmarshal por nível, um ponteiro de
// 128 tokens custava megabytes por sonda (portão do §11).
func escalarNoPonteiro(corpo []byte, ponteiro []string) (string, bool) {
	if !json.Valid(corpo) {
		return "", false
	}
	c := cursor{b: corpo}
	c.espacos()
	valor, ok := c.descer(ponteiro)
	if !ok {
		return "", false
	}
	return formaDeTexto(valor)
}

// cursor anda sobre um JSON que json.Valid já aceitou: por isso não confere
// sintaxe, só a estrutura que precisa para pular valores.
type cursor struct {
	b []byte
	i int
}

// descer consome o valor que começa em c.i e devolve o pedaço que o ponteiro
// aponta dentro dele. Num objeto, todos os membros são lidos, e vale a última
// chave repetida, como no encoding/json: um caminho achado antes é trocado
// pelo da repetição, mesmo que ela não o tenha.
func (c *cursor) descer(ponteiro []string) ([]byte, bool) {
	if len(ponteiro) == 0 {
		inicio := c.i
		c.pular()
		return c.b[inicio:c.i], true
	}
	switch c.b[c.i] {
	case '{':
		return c.descerNoObjeto(ponteiro)
	case '[':
		return c.descerNoArray(ponteiro)
	}
	c.pular() // escalar não tem filho
	return nil, false
}

func (c *cursor) descerNoObjeto(ponteiro []string) (achado []byte, ok bool) {
	c.i++ // {
	for c.espacos(); c.b[c.i] != '}'; c.espacos() {
		chave := c.lerString()
		c.espacos()
		c.i++ // :
		c.espacos()
		if chaveIgual(chave, ponteiro[0]) {
			achado, ok = c.descer(ponteiro[1:])
		} else {
			c.pular()
		}
		c.depoisDoMembro()
	}
	c.i++ // }
	return achado, ok
}

func (c *cursor) descerNoArray(ponteiro []string) (achado []byte, ok bool) {
	alvo, indiceValido := indiceDoArray(ponteiro[0])
	c.i++ // [
	for n := 0; ; n++ {
		if c.espacos(); c.b[c.i] == ']' {
			break
		}
		if indiceValido && n == alvo {
			achado, ok = c.descer(ponteiro[1:])
		} else {
			c.pular()
		}
		c.depoisDoMembro()
	}
	c.i++ // ]
	return achado, ok
}

// depoisDoMembro passa os espaços e a vírgula que separam membros.
func (c *cursor) depoisDoMembro() {
	c.espacos()
	if c.b[c.i] == ',' {
		c.i++
	}
}

func (c *cursor) espacos() {
	for c.i < len(c.b) {
		switch c.b[c.i] {
		case ' ', '\t', '\n', '\r':
			c.i++
		default:
			return
		}
	}
}

// pular passa um valor inteiro, contando a profundidade de { e [ e ignorando
// o que está dentro de strings.
func (c *cursor) pular() {
	switch c.b[c.i] {
	case '"':
		c.lerString()
	case '{', '[':
		profundidade := 0
		for {
			switch c.b[c.i] {
			case '"':
				c.lerString()
				continue
			case '{', '[':
				profundidade++
			case '}', ']':
				profundidade--
			}
			c.i++
			if profundidade == 0 {
				return
			}
		}
	default: // número, true, false ou null
		for c.i < len(c.b) && !fimDeLiteral(c.b[c.i]) {
			c.i++
		}
	}
}

func fimDeLiteral(b byte) bool {
	switch b {
	case ',', '}', ']', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// lerString passa uma string e a devolve com as aspas, ainda escapada.
func (c *cursor) lerString() []byte {
	inicio := c.i
	for c.i++; c.b[c.i] != '"'; c.i++ {
		if c.b[c.i] == '\\' {
			c.i++
		}
	}
	c.i++
	return c.b[inicio:c.i]
}

// chaveIgual compara a chave crua com o token já decodificado do ponteiro.
// Sem barra invertida e em UTF-8 válido, a chave crua é a própria chave. Fora
// desse atalho, ela é decodificada runa a runa contra o token, sem alocar: um
// json.Unmarshal por chave passava de 1 MiB num corpo de 64 KiB (D2c).
func chaveIgual(crua []byte, token string) bool {
	miolo := crua[1 : len(crua)-1]
	if bytes.IndexByte(miolo, '\\') < 0 && utf8.Valid(miolo) {
		return string(miolo) == token
	}
	var runa [utf8.UTFMax]byte
	for len(miolo) > 0 {
		r, lidos := proximaRunaDaString(miolo)
		miolo = miolo[lidos:]
		n := utf8.EncodeRune(runa[:], r)
		if len(token) < n || token[:n] != string(runa[:n]) {
			return false
		}
		token = token[n:]
	}
	return token == ""
}

// proximaRunaDaString decodifica a primeira runa do miolo de uma string JSON
// que json.Valid já aceitou, como o encoding/json faz: par substituto
// inválido e byte de UTF-8 inválido viram U+FFFD.
func proximaRunaDaString(s []byte) (r rune, lidos int) {
	if s[0] != '\\' {
		return utf8.DecodeRune(s)
	}
	switch s[1] {
	case 'b':
		return '\b', 2
	case 'f':
		return '\f', 2
	case 'n':
		return '\n', 2
	case 'r':
		return '\r', 2
	case 't':
		return '\t', 2
	case 'u':
		r = hex4(s[2:6])
		if !utf16.IsSurrogate(r) {
			return r, 6
		}
		if len(s) >= 12 && s[6] == '\\' && s[7] == 'u' {
			if par := utf16.DecodeRune(r, hex4(s[8:12])); par != unicode.ReplacementChar {
				return par, 12
			}
		}
		return unicode.ReplacementChar, 6
	}
	return rune(s[1]), 2 // ", \ ou /
}

// hex4 lê os quatro dígitos de um \uXXXX já validado.
func hex4(s []byte) rune {
	var r rune
	for _, c := range s[:4] {
		switch {
		case c <= '9':
			c -= '0'
		case c <= 'F':
			c -= 'A' - 10
		default:
			c -= 'a' - 10
		}
		r = r<<4 | rune(c)
	}
	return r
}

// formaDeTexto recebe um valor JSON já validado, sem espaço em volta.
func formaDeTexto(valor []byte) (string, bool) {
	switch valor[0] {
	case '{', '[':
		return "", false
	case '"':
		var s string
		if err := json.Unmarshal(valor, &s); err != nil {
			return "", false
		}
		return s, true
	}
	// Número, true, false ou null: o token como está escrito no corpo.
	return string(valor), true
}

func indiceDoArray(token string) (int, bool) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(token); i++ {
		if token[i] < '0' || token[i] > '9' {
			return 0, false
		}
	}
	i, err := strconv.Atoi(token)
	return i, err == nil
}
