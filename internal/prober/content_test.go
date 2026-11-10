package prober

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
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
		{"json chave com barra (token já decodificado)", json("1", "a/b"), `{"a/b":1}`, false, ""},
		{"json chave com escape no corpo", json("1", "a/b"), `{"a\/b":1}`, false, ""},
		{"json chave escapada repetida: vale a última", json("2", "ab"), `{"ab":1,"a\u0062":2}`, false, ""},
		{"json chave com UTF-8 inválido vira U+FFFD", json("1", "\uFFFD"), "{\"\xff\":1}", false, ""},
		{"json chave com par substituto", json("1", "\U0001F600"), `{"\ud83d\ude00":1}`, false, ""},
		{"json chave com substituto sozinho vira U+FFFD", json("1", "\uFFFDA"), `{"\ud800\u0041":1}`, false, ""},
		{"json chave com substituto no fim vira U+FFFD", json("1", "a\uFFFD"), `{"a\udc00":1}`, false, ""},
		{"json chave com escapes de controle", json("1", "a\tb\n\b\f\r\"\\"), `{"a\tb\n\b\f\r\"\\":1}`, false, ""},
		{"json chave escapada com outra letra", json("1", "ab"), `{"a\u0063":1}`, false, KindContentMismatch},
		{"json chave escapada mais curta que o token", json("1", "abc"), `{"a\u0062":1}`, false, KindContentMismatch},
		{"json chave escapada mais longa que o token", json("1", "a"), `{"a\u0062":1}`, false, KindContentMismatch},
		{"json forma de texto: string \"1\" passa como 1", json("1", "n"), `{"n":"1"}`, false, ""},
		{"json forma de texto: string \"null\" passa como null", json("null", "a"), `{"a":"null"}`, false, ""},
		{"json última repetida sem o caminho", json("ok", "a", "b"), `{"a":{"b":"ok"},"a":1}`, false, KindContentMismatch},
		{"json lixo depois do valor", json("1", "a"), `{"a":1} x`, false, KindContentMismatch},
		{"json índice zero", json("10", "0"), `[10,20]`, false, ""},
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

// D2c do STRIDE: comparar uma chave fora do atalho não aloca. Com um
// json.Unmarshal por chave, um corpo de 64 KiB alocava mais de 1 MiB.
func TestChaveIgualDoesNotAllocate(t *testing.T) {
	casos := []struct{ crua, token string }{
		{`"a\/b"`, "a/b"},
		{`"a\u0062"`, "ab"},
		{`"\ud83d\ude00"`, "\U0001F600"},
		{"\"\xff\"", "\uFFFD"},
		{`"a\u0062"`, "outra"},
	}
	for _, c := range casos {
		crua := []byte(c.crua)
		if allocs := testing.AllocsPerRun(100, func() { chaveIgual(crua, c.token) }); allocs != 0 {
			t.Errorf("chaveIgual(%q, %q) alocou %v vezes, queria 0", c.crua, c.token, allocs)
		}
	}
}

// tetoDeMemoriaJSON é o portão do §11: 64 sondas em JSON de 64 KiB cabem em
// ~25 MiB só com até 400 KiB por avaliação.
const tetoDeMemoriaJSON = 400 << 10

// corpoJSONLargo tem 64 KiB num objeto de 1000 chaves com arrays aninhados, e o
// ponteiro aponta a última chave.
func corpoJSONLargo(tb testing.TB) ([]byte, RegraDaSonda) {
	tb.Helper()
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
		tb.Fatalf("corpo largo com %d bytes, queria 65536", corpo.Len())
	}
	ponteiro := []string{fmt.Sprintf("chave%04d", chaves-1)}
	return []byte(corpo.String()), RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("ok"), Pointer: ponteiro}
}

// corpoJSONFundo tem 64 KiB aninhados em 128 níveis, com irmãos de ~500 bytes
// antes e depois do caminho, e o ponteiro mais fundo que o cadastro aceita:
// "/a" x 128 = 256 caracteres.
func corpoJSONFundo(tb testing.TB) ([]byte, RegraDaSonda) {
	tb.Helper()
	const niveis = 128
	antes := `{"x":"` + strings.Repeat("p", 246) + `","a":`
	depois := `,"y":"` + strings.Repeat("q", 245) + `"}`
	miolo := strings.Repeat(antes, niveis) + `"ok"` + strings.Repeat(depois, niveis)
	if len(miolo) > 64<<10 {
		tb.Fatalf("corpo fundo com %d bytes, passa de 65536", len(miolo))
	}
	corpo := strings.Repeat(" ", 64<<10-len(miolo)) + miolo
	ponteiro := make([]string, niveis)
	for i := range ponteiro {
		ponteiro[i] = "a"
	}
	return []byte(corpo), RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("ok"), Pointer: ponteiro}
}

// corpoJSONDeChaves enche 64 KiB com chaves curtas na forma de chave(i), e o
// ponteiro aponta a última, já decodificada por token(i). É o pior caso de
// chaveIgual: cada chave cai fora do atalho sem barra e em UTF-8 válido.
func corpoJSONDeChaves(tb testing.TB, chave, token func(int) string) ([]byte, RegraDaSonda) {
	tb.Helper()
	var corpo strings.Builder
	corpo.WriteString("{")
	i := 0
	for ; corpo.Len() < 64<<10-64; i++ {
		fmt.Fprintf(&corpo, `"%s":0,`, chave(i))
	}
	fmt.Fprintf(&corpo, `"%s":"ok"`, chave(i))
	for corpo.Len() < 64<<10-1 {
		corpo.WriteString(" ")
	}
	corpo.WriteString("}")
	if corpo.Len() != 64<<10 {
		tb.Fatalf("corpo de chaves com %d bytes, queria 65536", corpo.Len())
	}
	return []byte(corpo.String()), RegraDaSonda{Mode: targets.ContentJSONEquals, Value: []byte("ok"), Pointer: []string{token(i)}}
}

// D2c do STRIDE: chaves com escape, que o encoding/json decodificaria uma a uma.
func corpoJSONChavesEscapadas(tb testing.TB) ([]byte, RegraDaSonda) {
	return corpoJSONDeChaves(tb,
		func(i int) string { return fmt.Sprintf(`a\/%05d`, i) },
		func(i int) string { return fmt.Sprintf("a/%05d", i) })
}

// D2c do STRIDE: chaves com um byte de UTF-8 inválido, que vira U+FFFD.
func corpoJSONChavesUTF8Invalido(tb testing.TB) ([]byte, RegraDaSonda) {
	return corpoJSONDeChaves(tb,
		func(i int) string { return fmt.Sprintf("\xff%05d", i) },
		func(i int) string { return fmt.Sprintf("\uFFFD%05d", i) })
}

var corposJSONDoPortao = map[string]func(testing.TB) ([]byte, RegraDaSonda){
	"largo":                 corpoJSONLargo,
	"fundo":                 corpoJSONFundo,
	"chaves escapadas":      corpoJSONChavesEscapadas,
	"chaves UTF-8 inválido": corpoJSONChavesUTF8Invalido,
}

// O portão de memória como teste, para não depender de alguém ler o
// benchmark: a média de bytes alocados por avaliação fica abaixo do teto,
// também com o ponteiro mais fundo que o cadastro aceita.
func TestAvaliarConteudoJSONStaysUnderTheMemoryCap(t *testing.T) {
	for nome, montar := range corposJSONDoPortao {
		t.Run(nome, func(t *testing.T) {
			corpo, regra := montar(t)
			if got := avaliarConteudo(regra, corpo, false); got != "" {
				t.Fatalf("avaliarConteudo = %q, queria passar", got)
			}
			const rodadas = 20
			var antes, depois runtime.MemStats
			runtime.ReadMemStats(&antes)
			for range rodadas {
				avaliarConteudo(regra, corpo, false)
			}
			runtime.ReadMemStats(&depois)
			if porRodada := (depois.TotalAlloc - antes.TotalAlloc) / rodadas; porRodada >= tetoDeMemoriaJSON {
				t.Errorf("%d bytes alocados por avaliação, queria menos de %d", porRodada, tetoDeMemoriaJSON)
			}
		})
	}
}

// O mesmo portão medido com -benchmem, para o corpo do PR.
func BenchmarkAvaliarConteudoJSON64KiB(b *testing.B) {
	for _, nome := range []string{"largo", "fundo", "chaves escapadas", "chaves UTF-8 inválido"} {
		b.Run(nome, func(b *testing.B) {
			corpo, regra := corposJSONDoPortao[nome](b)
			b.SetBytes(int64(len(corpo)))
			b.ReportAllocs()
			for b.Loop() {
				if got := avaliarConteudo(regra, corpo, false); got != "" {
					b.Fatalf("avaliarConteudo = %q, queria passar", got)
				}
			}
		})
	}
}

// contador conta os bytes que saem da fonte, para provar o teto de lerCorpo
// pelo que foi lido, e não só pelo que foi devolvido.
type contador struct {
	fonte io.Reader
	lidos int
}

func (c *contador) Read(p []byte) (int, error) {
	n, err := c.fonte.Read(p)
	c.lidos += n
	return n, err
}

// D1 com a descompressão nossa: a bomba de gzip (100 MiB de zeros em uns
// 100 KiB) para no teto de 64 KiB descomprimidos, e só o começo dela é lido.
// Inflar tudo e cortar depois devolveria o mesmo corpo, mas consumiria a bomba
// inteira: a contagem de bytes lidos é o que pega isso.
func TestLerCorpoDaRegraCapsTheDecompressedBody(t *testing.T) {
	bomba := bombaGzip()
	fonte := &contador{fonte: bytes.NewReader(bomba)}
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(fonte),
	}
	corpo, cortado, ilegivel, err := lerCorpoDaRegra(resp)
	if err != nil || ilegivel || !cortado || len(corpo) != safedial.MaxBodyBytes {
		t.Fatalf("len(corpo) %d, cortado %v, ilegivel %v, err %v; queria %d, true, false, nil",
			len(corpo), cortado, ilegivel, err, safedial.MaxBodyBytes)
	}
	const tetoComprimido = 16 << 10
	if fonte.lidos > tetoComprimido {
		t.Errorf("%d bytes comprimidos lidos de %d, queria no máximo %d", fonte.lidos, len(bomba), tetoComprimido)
	}
}

// Corpo que declara gzip e não é gzip: a regra não tem o que julgar, e a
// causa é a codificação, não um erro de rede.
func TestLerCorpoDaRegraWithAnInvalidGzipHeaderIsUnreadable(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(strings.NewReader("isto não é gzip")),
	}
	if _, _, ilegivel, err := lerCorpoDaRegra(resp); err != nil || !ilegivel {
		t.Errorf("ilegivel %v, err %v; queria true, nil", ilegivel, err)
	}
}

func TestLerCorpoReadsAtMostOneByteOverTheLimit(t *testing.T) {
	casos := []struct {
		nome      string
		tamanho   int
		lidos     int
		devolvido int
		cortado   bool
	}{
		{"vazio", 0, 0, 0, false},
		{"pequeno", 10, 10, 10, false},
		{"no limite", 65536, 65536, 65536, false},
		{"um byte acima", 65537, 65537, 65536, true},
		{"1 MiB", 1 << 20, 65537, 65536, true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			fonte := &contador{fonte: bytes.NewReader(make([]byte, c.tamanho))}
			corpo, cortado, err := lerCorpo(fonte)
			if err != nil {
				t.Fatal(err)
			}
			if fonte.lidos != c.lidos || len(corpo) != c.devolvido || cortado != c.cortado {
				t.Errorf("lidos %d, devolvidos %d, cortado %v; queria %d, %d, %v",
					fonte.lidos, len(corpo), cortado, c.lidos, c.devolvido, c.cortado)
			}
		})
	}
}

// escalarDeReferencia é a semântica do Ruling escrita do jeito mais direto,
// com a árvore inteira do encoding/json: o fuzz confere que o cursor, que pula
// valores à mão, diz o mesmo e nunca entra em pânico.
func escalarDeReferencia(corpo []byte, ponteiro []string) (string, bool) {
	dec := stdjson.NewDecoder(bytes.NewReader(corpo))
	dec.UseNumber()
	var valor any
	if err := dec.Decode(&valor); err != nil {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	for _, token := range ponteiro {
		switch v := valor.(type) {
		case map[string]any:
			filho, ok := v[token]
			if !ok {
				return "", false
			}
			valor = filho
		case []any:
			i, ok := indiceDoArray(token)
			if !ok || i >= len(v) {
				return "", false
			}
			valor = v[i]
		default:
			return "", false
		}
	}
	switch v := valor.(type) {
	case string:
		return v, true
	case stdjson.Number:
		return v.String(), true
	case bool:
		return fmt.Sprint(v), true
	case nil:
		return "null", true
	}
	return "", false
}

func FuzzEscalarNoPonteiro(f *testing.F) {
	for _, s := range []struct{ corpo, ponteiro string }{
		{`{"a":{"b":[0,{"c":"ok"}]}}`, "a/b/1/c"},
		{`{"a":1,"a":{"b":2}}`, "a/b"},
		{`{"ab":"x","ab":"y"}`, "ab"},
		{` [ "a\"]" , {"]":[1,2]} ] `, "1/]/1"},
		{`{"s":"\\\"}"}`, "s"},
		{`-1.5e3`, ""},
		{`{"a":1} {"a":2}`, "a"},
		{`{"a":tru}`, "a"},
		{`[[[[[[]]]]]]`, "0/0"},
		{"{\"\xff\":1}", "\uFFFD"},
		{`{"\ud83d\ude00":1,"\ud800\u0041":2,"a\udc00":3}`, "\uFFFDA"},
		{`{"\ud83d\ude00":1}`, "\U0001F600"},
		{`{"a\u0062\t":1}`, "ab\t"},
	} {
		f.Add([]byte(s.corpo), s.ponteiro)
	}
	f.Fuzz(func(t *testing.T, corpo []byte, ponteiro string) {
		var tokens []string
		if ponteiro != "" {
			tokens = strings.Split(ponteiro, "/")
		}
		gotValor, gotOk := escalarNoPonteiro(corpo, tokens)
		querValor, querOk := escalarDeReferencia(corpo, tokens)
		if gotValor != querValor || gotOk != querOk {
			t.Errorf("corpo %q ponteiro %q: cursor (%q, %v), referência (%q, %v)",
				corpo, ponteiro, gotValor, gotOk, querValor, querOk)
		}
	})
}
