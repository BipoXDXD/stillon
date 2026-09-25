// Package prober executa as sondas e as agenda (spec §6).
package prober

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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

const userAgent = "StillOn/1.0 (+monitor de disponibilidade)"

// Spec é o que uma sonda precisa saber sobre o alvo, já decifrado.
type Spec struct {
	URL               string
	Method            string
	Timeout           time.Duration
	ExpectedStatus    int
	DegradedThreshold time.Duration // 0: sem classificação degraded
	Headers           map[string]string
}

// LogValue mostra os NOMES dos headers e nunca os valores (Plano 8). Os
// headers chegam aqui decifrados e costumam ser credencial de terceiro
// (Authorization, X-Api-Key); sem isto, um slog.Any("spec", spec) gravaria
// essa credencial no log. A proteção é do tipo, não da disciplina, como no
// crypto.Secret e no config.Config. A URL sai como está: o dono já a vê na
// API, e credenciais na URL são recusadas no cadastro.
func (s Spec) LogValue() slog.Value {
	nomes := make([]string, 0, len(s.Headers))
	for nome := range s.Headers {
		nomes = append(nomes, nome)
	}
	slices.Sort(nomes)
	return slog.GroupValue(
		slog.String("url", s.URL),
		slog.String("method", s.Method),
		slog.Duration("timeout", s.Timeout),
		slog.Int("expected_status", s.ExpectedStatus),
		slog.Duration("degraded_threshold", s.DegradedThreshold),
		slog.Any("headers", nomes),
	)
}

// Result é o que sai de uma sonda. Não tem campo para mensagem de erro, e é
// isso que impede que uma mensagem crua chegue ao banco (spec §4.2).
type Result struct {
	Status     Status
	Latency    time.Duration
	StatusCode int       // 0 quando não houve resposta
	ErrorKind  ErrorKind // "" quando não é down
}

// Probe faz uma requisição e a classifica. Nunca devolve erro: toda falha é
// um resultado, e o resultado é o produto.
//
// O prazo é do context, e vale para cabeçalho E corpo. A latência vai do
// envio ao fim da leitura do corpo, limitada a 64 KiB (spec §5.1). Ela é
// medida pelo relógio monotônico real, e não pelo relógio injetado, porque
// o relógio falso dos testes não anda durante a sonda (D11).
func Probe(ctx context.Context, client *http.Client, s Spec) Result {
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

	inicio := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Status: StatusDown, Latency: time.Since(inicio), ErrorKind: Classify(err)}
	}
	defer resp.Body.Close()

	// Corpo maior que o limite não é falha (D2): lê-se até 64 KiB e fecha.
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, safedial.MaxBodyBytes))
	latencia := time.Since(inicio)
	if err != nil {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: Classify(err)}
	}

	if resp.StatusCode != s.ExpectedStatus {
		return Result{Status: StatusDown, Latency: latencia, StatusCode: resp.StatusCode, ErrorKind: KindHTTPStatus}
	}
	if s.DegradedThreshold > 0 && latencia > s.DegradedThreshold {
		return Result{Status: StatusDegraded, Latency: latencia, StatusCode: resp.StatusCode}
	}
	return Result{Status: StatusUp, Latency: latencia, StatusCode: resp.StatusCode}
}
