package prober

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/targets"
)

func TestAvaliarConteudo(t *testing.T) {
	contem := func(v string) RegraDaSonda { return RegraDaSonda{Mode: targets.ContentContains, Value: []byte(v)} }
	naoContem := func(v string) RegraDaSonda { return RegraDaSonda{Mode: targets.ContentNotContains, Value: []byte(v)} }
	json := func(v string, ponteiro ...string) RegraDaSonda {
		return RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte(v), Pointer: ponteiro}
	}
	casos := []struct {
		nome    string
		regra   RegraDaSonda
		corpo   string
		cortado bool
		quer    ErrorKind
	}{
		{"contains achado", contem("ok"), `{"status":"ok"}`, false, ""},
		{"contains não achado e completo", contem("ok"), `{"status":"falhou"}`, false, KindContentMismatch},
		{"contains não achado e cortado", contem("ok"), `{"status":"falhou"`, true, KindBodyLimit},
		{"contains achado e cortado", contem("ok"), `{"status":"ok"`, true, ""},
		{"not_contains achado", naoContem("erro"), `houve um erro`, false, KindContentMismatch},
		{"not_contains achado e cortado", naoContem("erro"), `houve um erro`, true, KindContentMismatch},
		{"not_contains não achado e completo", naoContem("erro"), `tudo certo`, false, ""},
		{"not_contains não achado e cortado", naoContem("erro"), `tudo certo`, true, KindBodyLimit},
		{"contains compara bytes, sem charset", contem("ação"), "a\xe7\xe3o", false, KindContentMismatch},

		{"json string igual", json("ok", "status"), `{"status":"ok"}`, false, ""},
		{"json string com outra caixa", json("OK", "status"), `{"status":"ok"}`, false, KindContentMismatch},
		{"json número como escrito: 1.0 não é 1", json("1", "n"), `{"n":1.0}`, false, KindContentMismatch},
		{"json número como escrito: 1.0", json("1.0", "n"), `{"n":1.0}`, false, ""},
		{"json true", json("true", "a"), `{"a":true}`, false, ""},
		{"json null", json("null", "a"), `{"a":null}`, false, ""},
		{"json objeto não é escalar", json("{}", "a"), `{"a":{}}`, false, KindContentMismatch},
		{"json array não é escalar", json("[1]", "a"), `{"a":[1]}`, false, KindContentMismatch},
		{"json com espaço em volta", json("1.0", "b", "1"), " \n{ \"a\" : 1 , \"b\" : [ 0 , 1.0 ] }\t", false, ""},
		{"json descida funda", json("ok", "a", "b", "1", "c"), `{"a":{"b":[0,{"c":"ok"}]}}`, false, ""},
		{"json string com escape", json("a\"b/é", "s"), `{"s":"a\"b\/\u00e9"}`, false, ""},
		{"json chave com barra", json("1", "a/b"), `{"a/b":1}`, false, ""},
		{"json índice de array", json("20", "1"), `[10,20]`, false, ""},
		{"json índice fora do array", json("20", "2"), `[10,20]`, false, KindContentMismatch},
		{"json índice com zero à esquerda", json("20", "01"), `[10,20]`, false, KindContentMismatch},
		{"json índice '-' (depois do último)", json("20", "-"), `[10,20]`, false, KindContentMismatch},
		{"json índice com sinal", json("20", "+1"), `[10,20]`, false, KindContentMismatch},
		{"json índice negativo", json("20", "-1"), `[10,20]`, false, KindContentMismatch},
		{"json chave '01' em objeto vale", json("1", "01"), `{"01":1}`, false, ""},
		{"json chave '-' em objeto vale", json("1", "-"), `{"-":1}`, false, ""},
		{"json ponteiro ausente", json("ok", "outra"), `{"status":"ok"}`, false, KindContentMismatch},
		{"json descida por escalar", json("ok", "status", "x"), `{"status":"ok"}`, false, KindContentMismatch},
		{"json dois valores", json("1", "a"), `{"a":1}{"b":2}`, false, KindContentMismatch},
		{"json chave duplicada: vale a última", json("2", "a"), `{"a":1,"a":2}`, false, ""},
		{"json corpo vazio", json("ok", "status"), ``, false, KindContentMismatch},
		{"json inválido", json("ok", "status"), `{"status":`, false, KindContentMismatch},
		{"json cortado", json("ok", "status"), `{"status":"ok","resto":"`, true, KindBodyLimit},
		{"modo desconhecido não passa", RegraDaSonda{Mode: "regex", Value: []byte("ok")}, `ok`, false, KindUnknown},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := avaliarConteudo(c.regra, []byte(c.corpo), c.cortado); got != c.quer {
				t.Errorf("avaliarConteudo = %q, queria %q", got, c.quer)
			}
		})
	}
}

// O portão do §11: acima de 400 KiB por operação, a descida troca de árvore
// para json.Decoder.Token (Ruling do benchmark).
func BenchmarkAvaliarConteudoJSON64KiB(b *testing.B) {
	var corpo strings.Builder
	corpo.WriteString("{")
	const chaves = 1000
	for i := range chaves - 1 {
		fmt.Fprintf(&corpo, `"chave%04d":{"lista":[1,2,[3,4]],"texto":"abcdefghij"},`, i)
	}
	fmt.Fprintf(&corpo, `"chave%04d":"ok"`, chaves-1)
	for corpo.Len() < 64<<10-1 {
		corpo.WriteString(" ")
	}
	corpo.WriteString("}")
	if corpo.Len() != 64<<10 {
		b.Fatalf("corpo com %d bytes, queria 65536", corpo.Len())
	}
	regra := RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("ok"), Pointer: []string{fmt.Sprintf("chave%04d", chaves-1)}}
	dados := []byte(corpo.String())
	b.SetBytes(int64(len(dados)))
	b.ReportAllocs()
	for b.Loop() {
		if got := avaliarConteudo(regra, dados, false); got != "" {
			b.Fatalf("avaliarConteudo = %q, queria passar", got)
		}
	}
}
