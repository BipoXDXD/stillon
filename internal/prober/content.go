package prober

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

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
// então o teto vale também contra bomba de gzip (D1).
func lerCorpo(r io.Reader) (corpo []byte, cortado bool, err error) {
	corpo, err = io.ReadAll(io.LimitReader(r, safedial.MaxBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(corpo) > safedial.MaxBodyBytes {
		return corpo[:safedial.MaxBodyBytes], true, nil
	}
	return corpo, false, nil
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
// Sem barra invertida, a chave crua é a própria chave e a comparação não
// aloca; com escape, ela é decodificada antes.
func chaveIgual(crua []byte, token string) bool {
	miolo := crua[1 : len(crua)-1]
	if bytes.IndexByte(miolo, '\\') < 0 {
		return string(miolo) == token
	}
	var chave string
	if err := json.Unmarshal(crua, &chave); err != nil {
		return false
	}
	return chave == token
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
