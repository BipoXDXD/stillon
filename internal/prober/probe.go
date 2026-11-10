// Package prober executa as sondas e as agenda (spec §6).
package prober

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"slices"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

type Status string

const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

const userAgent = safedial.UserAgent

// Spec é o que uma sonda precisa saber sobre o alvo, já decifrado.
type Spec struct {
	URL               string
	Method            string
	Timeout           time.Duration
	ExpectedStatus    int
	DegradedThreshold time.Duration // 0: sem classificação degraded
	Headers           map[string]string
	Content           *RegraDaSonda // nil: sem regra de conteúdo
}

// LogValue mostra os NOMES dos headers e nunca os valores (Plano 8). Os
// headers chegam aqui decifrados e costumam ser credencial de terceiro
// (Authorization, X-Api-Key); sem isto, um slog.Any("spec", spec) gravaria
// essa credencial no log. A proteção é do tipo, não da disciplina, como no
// crypto.Secret e no config.Config. A URL sai como está: o dono já a vê na
// API, e credenciais na URL são recusadas no cadastro. Da regra de conteúdo
// sai só o modo: o valor e o ponteiro são texto do dono.
func (s Spec) LogValue() slog.Value {
	nomes := make([]string, 0, len(s.Headers))
	for nome := range s.Headers {
		nomes = append(nomes, nome)
	}
	slices.Sort(nomes)
	attrs := []slog.Attr{
		slog.String("url", s.URL),
		slog.String("method", s.Method),
		slog.Duration("timeout", s.Timeout),
		slog.Int("expected_status", s.ExpectedStatus),
		slog.Duration("degraded_threshold", s.DegradedThreshold),
		slog.Any("headers", nomes),
	}
	if s.Content != nil {
		attrs = append(attrs, slog.String("content_mode", string(s.Content.Mode)))
	}
	return slog.GroupValue(attrs...)
}

// Result é o que sai de uma sonda. Não tem campo para mensagem de erro, e é
// isso que impede que uma mensagem crua chegue ao banco (spec §4.2).
type Result struct {
	Status     Status
	Latency    time.Duration
	StatusCode int       // 0 quando não houve resposta
	ErrorKind  ErrorKind // "" quando não é down
	// CertNotAfter é a validade efetiva do certificado do host cadastrado,
	// lida dos handshakes verificados desta sonda. Zero: sem leitura (http,
	// IP literal, TLS não verificado ou só handshakes de outro host).
	CertNotAfter time.Time
}

// Probe faz uma requisição e a classifica. Nunca devolve erro: toda falha é
// um resultado, e o resultado é o produto.
//
// O prazo é do context, e vale para cabeçalho E corpo. A latência vai do
// envio ao fim da leitura do corpo, limitada a 64 KiB (spec §5.1), com a
// regra de conteúdo ou sem ela. A ordem é transporte, status, regra e
// lentidão: status errado segue http_status com qualquer corpo. A latência é
// medida pelo relógio monotônico real, e não pelo relógio injetado, porque
// o relógio falso dos testes não anda durante a sonda (D11).
//
// Os handshakes TLS de todos os saltos são observados por um trace no
// context, e não por resp.TLS, que só mostra o último salto: um redirect para
// outro host leria o certificado alheio. A validade vale também quando a
// sonda sai down, se antes houve um handshake verificado do host cadastrado.
func Probe(ctx context.Context, client *http.Client, s Spec) Result {
	trace, coletados := coletorDeHandshakes()
	res := sondar(httptrace.WithClientTrace(ctx, trace), client, s)
	res.CertNotAfter = validadeLida(s.URL, coletados())
	return res
}

func sondar(ctx context.Context, client *http.Client, s Spec) Result {
	client, liberar := clienteDaSonda(client)
	defer liberar()
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, s.Method, s.URL, nil)
	if err != nil {
		// A URL já foi validada no cadastro; chegar aqui é defeito, e o
		// resultado honesto é "não deu para sondar".
		return Result{Status: StatusDown, ErrorKind: KindUnknown}
	}
	req.Header.Set("User-Agent", userAgent)
	for nome, valor := range s.Headers {
		req.Header.Set(nome, valor)
	}
	if s.Content != nil {
		// O cadastro recusa Accept-Encoding do dono quando há regra: este é
		// da sonda, que descomprime o gzip ela mesma (lerCorpoDaRegra).
		req.Header.Set("Accept-Encoding", "gzip")
	}

	inicio := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Status: StatusDown, Latency: time.Since(inicio), ErrorKind: Classify(err)}
	}
	defer resp.Body.Close()

	var (
		corpo    []byte
		cortado  bool
		ilegivel bool
	)
	if s.Content != nil {
		corpo, cortado, ilegivel, err = lerCorpoDaRegra(resp)
	} else {
		// Sem regra, corpo maior que o limite não é falha (D2): lê-se até
		// 64 KiB e fecha.
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, safedial.MaxBodyBytes))
	}
	latencia := time.Since(inicio)
	if err != nil {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: Classify(err)}
	}

	if resp.StatusCode != s.ExpectedStatus {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: KindHTTPStatus}
	}
	if s.Content != nil {
		kind := KindContentEncoding
		if !ilegivel {
			kind = avaliarConteudo(*s.Content, corpo, cortado)
		}
		if kind != "" {
			return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: kind}
		}
	}
	if s.DegradedThreshold > 0 && latencia > s.DegradedThreshold {
		return Result{Status: StatusDegraded, Latency: latencia, StatusCode: resp.StatusCode}
	}
	return Result{Status: StatusUp, Latency: latencia, StatusCode: resp.StatusCode}
}

// clienteDaSonda devolve uma cópia de modelo com um transporte só desta sonda,
// e a função que o encerra.
//
// Desde o Go 1.23 o net/http disca com context.WithoutCancel: o prazo da
// sonda não chega ao dial, e o dial que ela abandona segue vivo (num handshake
// TLS mudo, para sempre, com socket e goroutines). CloseIdleConnections
// cancela os dials que ninguém mais espera, efeito que a documentação não
// promete; por isso o safedial também põe teto no dial e no handshake.
//
// O transporte é da sonda, e não compartilhado, porque CloseIdleConnections
// num transporte comum fecharia a conexão HTTP/2 que outra sonda acabou de
// abrir e ainda não usou (o net/http a põe no pool antes do primeiro stream):
// essa sonda discaria de novo, com latência dobrada. O Clone monta pool HTTP/2
// próprio e mantém a configuração TLS do modelo. Na primeira chamada, ele
// também liga o HTTP/2 no próprio modelo (nextProtoOnce), o que é inofensivo:
// o modelo nunca disca.
//
// Um RoundTripper que não seja *http.Transport (só em testes) é usado como
// está: não há o que clonar.
func clienteDaSonda(modelo *http.Client) (*http.Client, func()) {
	base, ok := modelo.Transport.(*http.Transport)
	if !ok {
		return modelo, func() {}
	}
	proprio := base.Clone()
	client := *modelo
	client.Transport = proprio
	return &client, proprio.CloseIdleConnections
}
