package targets

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// testdata/regra-de-conteudo.json tem os casos da regra que o servidor e o
// formulário do painel têm de julgar igual: os limites, os controles, os
// escapes do ponteiro e os headers recusados. O painel roda os mesmos casos em
// web/src/features/alvos/regra-contrato.test.ts. O servidor é a autoridade;
// a divergência só pioraria a mensagem, mas passava sem teste vermelho.
type casoDaRegra struct {
	Caso        string `json:"caso"`
	Modo        string `json:"modo"`
	Valor       string `json:"valor"`
	ValorAte    int    `json:"valorAte"`
	Ponteiro    string `json:"ponteiro"`
	PonteiroAte int    `json:"ponteiroAte"`
	Header      string `json:"header"`
	// Erro é "" (aceita), "valor", "ponteiro" ou "regra" (a regra inteira).
	Erro string `json:"erro"`
}

// preenchimento conta 1 runa e 4 bytes (2 unidades em UTF-16): um teto
// contado em bytes, ou no .length do JS, reprova antes da hora.
const preenchimento = "😀"

var campoDoErro = map[string]string{
	"valor":    "content_rule.value",
	"ponteiro": "content_rule.json_pointer",
	"regra":    "content_rule",
}

func TestContentRuleCasesSharedWithThePanel(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "regra-de-conteudo.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Um campo com nome errado no arquivo seria um caso que não testa nada.
	leitor := json.NewDecoder(bytes.NewReader(b))
	leitor.DisallowUnknownFields()
	var casos []casoDaRegra
	if err := leitor.Decode(&casos); err != nil || len(casos) == 0 {
		t.Fatalf("casos ilegíveis ou vazios (%v)", err)
	}
	for _, c := range casos {
		t.Run(c.Caso, func(t *testing.T) {
			f := valido()
			f.ContentRule = regra(ContentMode(c.Modo), completar(c.Valor, c.ValorAte), completar(c.Ponteiro, c.PonteiroAte))
			if c.Header != "" {
				f.Headers = map[string]string{c.Header: "x"}
			}
			var quer []string
			if c.Erro != "" {
				campo, ok := campoDoErro[c.Erro]
				if !ok {
					t.Fatalf("erro %q fora de valor, ponteiro e regra", c.Erro)
				}
				quer = []string{campo}
			}
			var achados []string
			if err := validate(&f); err != nil {
				for _, e := range err.Fields {
					achados = append(achados, e.Field)
				}
			}
			if !slices.Equal(achados, quer) {
				t.Errorf("campos com erro = %v, queria %v", achados, quer)
			}
		})
	}
}

// completar acrescenta o preenchimento até s ter ate runas; ate 0 deixa s.
func completar(s string, ate int) string {
	if falta := ate - utf8.RuneCountInString(s); falta > 0 {
		return s + strings.Repeat(preenchimento, falta)
	}
	return s
}
