package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// version é carimbada no build: -ldflags "-X main.version=…" (Dockerfile,
// Makefile, CI). Sai no log de boot e em `stillon version`, e em nenhuma
// resposta HTTP: expor a versão facilita achar uma vulnerável.
var version = "dev"

// healthcheckPrazo é o teto do `stillon healthcheck`. É maior que o
// readyzTimeout (2 s) do servidor, para um banco lento aparecer como o 503 do
// /readyz e não como um prazo estourado aqui, e menor que o timeout do
// healthcheck do compose (TestHealthcheckDeadlinesNest).
const healthcheckPrazo = 3 * time.Second

// runVersion é `stillon version`: imprime a versão carimbada no build.
func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "uso: stillon version")
		return saidaUso
	}
	fmt.Fprintln(stdout, version)
	return 0
}

// runHealthcheck é `stillon healthcheck`: 0 se o /readyz do próprio processo
// responde 200 dentro do prazo, 1 em qualquer outro caso; 2 com argumento a
// mais e 130 se interrompido (Ctrl-C, SIGTERM), como o `user`.
//
// A imagem final não tem shell nem curl (spec §5.9): o próprio binário é a
// ferramenta do healthcheck do compose.
func runHealthcheck(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "uso: stillon healthcheck (a porta vem de HTTP_ADDR)")
		return saidaUso
	}
	url, err := urlDeProntidao(getenv("HTTP_ADDR"))
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	if err := checar(ctx, url, healthcheckPrazo); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "healthcheck: interrompido")
			return saidaInterrompida
		}
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	return 0
}

// urlDeProntidao monta o /readyz em loopback a partir do HTTP_ADDR do
// processo. O host do HTTP_ADDR não interessa: o healthcheck roda dentro do
// mesmo contêiner, e o servidor escuta em todas as interfaces.
func urlDeProntidao(httpAddr string) (string, error) {
	httpAddr = strings.TrimSpace(httpAddr)
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	_, porta, err := net.SplitHostPort(httpAddr)
	if err != nil || porta == "" {
		return "", fmt.Errorf("HTTP_ADDR %q sem porta", httpAddr)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", porta) + "/readyz", nil
}

// checar exige 200 do /readyz dentro do prazo. Qualquer outra coisa (503 com
// o banco fora, conexão recusada, servidor mudo) é "não pronto".
func checar(ctx context.Context, url string, prazo time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, prazo)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return errors.New("readyz respondeu " + resp.Status)
	}
	return nil
}
