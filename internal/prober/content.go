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
// A descida decodifica só o nível do caminho, guardando os filhos crus
// (Ruling do benchmark, §11): a árvore inteira de um JSON de 64 KiB custava
// cerca de 1 MiB por sonda. Quem valida o JSON é o json.Unmarshal de cada
// nível, que também recusa lixo depois do valor; por isso o ponteiro nunca é
// vazio (ParseJSONPointer o recusa), e o corpo inteiro passa pelo primeiro.
func escalarNoPonteiro(corpo []byte, ponteiro []string) (string, bool) {
	atual := bytes.TrimSpace(corpo)
	if len(atual) == 0 || len(ponteiro) == 0 {
		return "", false
	}
	for _, token := range ponteiro {
		var ok bool
		if atual, ok = filho(atual, token); !ok {
			return "", false
		}
	}
	return formaDeTexto(atual)
}

// filho segue um token do ponteiro. Em objeto, vale a última chave repetida,
// como no encoding/json. Em array, o token só vale como índice RFC 6901: "0"
// ou dígitos sem zero à esquerda; "-" (depois do último), sinal e zero à
// esquerda não apontam nada. Escalar não tem filho.
func filho(valor []byte, token string) ([]byte, bool) {
	switch valor[0] {
	case '{':
		var objeto map[string]json.RawMessage
		if err := json.Unmarshal(valor, &objeto); err != nil {
			return nil, false
		}
		v, ok := objeto[token]
		return v, ok
	case '[':
		i, ok := indiceDoArray(token)
		if !ok {
			return nil, false
		}
		var lista []json.RawMessage
		if err := json.Unmarshal(valor, &lista); err != nil || i >= len(lista) {
			return nil, false
		}
		return lista[i], true
	}
	return nil, false
}

// formaDeTexto recebe um valor JSON já validado pelo Unmarshal do nível de
// cima, sem espaço em volta.
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
