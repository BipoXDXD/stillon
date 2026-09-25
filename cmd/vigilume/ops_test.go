package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func servidorDeProntidao(t *testing.T, h http.HandlerFunc) (httpAddr string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, porta, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ":" + porta
}

func healthcheck(t *testing.T, httpAddr string) (int, string) {
	t.Helper()
	var errb bytes.Buffer
	env := map[string]string{"HTTP_ADDR": httpAddr}
	codigo := runHealthcheck(context.Background(), nil, &errb, func(k string) string { return env[k] })
	return codigo, errb.String()
}

func TestHealthcheckPassesOnlyWithReadyzOK(t *testing.T) {
	var caminho string
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if codigo, errb := healthcheck(t, addr); codigo != 0 {
		t.Fatalf("readyz 200: código %d (%s), queria 0", codigo, errb)
	}
	if caminho != "/readyz" {
		t.Errorf("o healthcheck bateu em %q, queria /readyz (o que olha o banco)", caminho)
	}
}

func TestHealthcheckFailsWhenNotReady(t *testing.T) {
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	codigo, errb := healthcheck(t, addr)
	if codigo != 1 || !strings.Contains(errb, "503") {
		t.Errorf("readyz 503: código %d, stderr %q; queria 1 citando o 503", codigo, errb)
	}
}

func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, porta, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	if codigo, _ := healthcheck(t, ":"+porta); codigo != 1 {
		t.Errorf("porta fechada: código %d, queria 1", codigo)
	}
}

// Um servidor que aceita e não responde não pode segurar o healthcheck além
// do prazo, nem passar por pronto. O teste tem teto próprio: um checar sem
// prazo tem de FALHAR aqui, e não travar a suíte.
func TestHealthcheckGivesUpOnASilentServer(t *testing.T) {
	solta := make(chan struct{})
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-solta:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	defer close(solta)
	url, err := urlDeProntidao(addr)
	if err != nil {
		t.Fatal(err)
	}
	fim := make(chan error, 1)
	go func() { fim <- checar(context.Background(), url, 100*time.Millisecond) }()
	select {
	case err := <-fim:
		if err == nil {
			t.Fatal("servidor mudo passou por pronto")
		}
	case <-time.After(time.Second):
		t.Fatal("checar não desistiu em 1s com prazo de 100ms")
	}
}

func TestReadyzURLUsesLoopbackAndThePortOfHTTPAddr(t *testing.T) {
	for addr, queria := range map[string]string{
		"":             "http://127.0.0.1:8080/readyz",
		":9000":        "http://127.0.0.1:9000/readyz",
		"0.0.0.0:8081": "http://127.0.0.1:8081/readyz",
		"[::]:8082":    "http://127.0.0.1:8082/readyz",
	} {
		got, err := urlDeProntidao(addr)
		if err != nil || got != queria {
			t.Errorf("urlDeProntidao(%q) = %q, %v; queria %q", addr, got, err, queria)
		}
	}
	if _, err := urlDeProntidao("8080"); err == nil {
		t.Error("HTTP_ADDR sem ':' deveria ser erro")
	}
}

func TestVersionPrintsTheStampedVersion(t *testing.T) {
	antiga := version
	version = "v9.9.9-teste"
	t.Cleanup(func() { version = antiga })
	var out bytes.Buffer
	codigo := runVersion(nil, &out, io.Discard)
	if codigo != 0 || strings.TrimSpace(out.String()) != "v9.9.9-teste" {
		t.Errorf("version: código %d, saída %q; queria 0 e a versão carimbada", codigo, out.String())
	}
}

// Os dois subcomandos de operação passam pelo mesmo despacho do `user` (Plano
// 5), e o servidor continua sendo o padrão: um argumento que não está no mapa
// (a flag --role, um nome qualquer) segue para o flag.Parse de main.
func TestOperationSubcommandsShareTheDispatch(t *testing.T) {
	for _, nome := range []string{"user", "healthcheck", "version"} {
		if _, ok := subcomandos[nome]; !ok {
			t.Errorf("%q fora do mapa de subcomandos", nome)
		}
	}
	for _, arg := range []string{"--role=all", "-role", "serve", ""} {
		if _, ok := subcomandos[arg]; ok {
			t.Errorf("%q foi tratado como subcomando; o servidor é o padrão", arg)
		}
	}
}

// O compose passa o HTTP_ADDR como veio do ambiente; um espaço sobrando não
// pode virar "porta inválida" e deixar o app eternamente não pronto.
func TestHealthcheckTrimsHTTPAddr(t *testing.T) {
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if codigo, errb := healthcheck(t, "  "+addr+" \n"); codigo != 0 {
		t.Errorf("HTTP_ADDR com espaços: código %d (%s), queria 0", codigo, errb)
	}
}

// Ctrl-C no meio do healthcheck é "o operador desistiu" (130, como no `user`),
// não "o app não está pronto" (1).
func TestHealthcheckInterruptedIs130(t *testing.T) {
	chegou := make(chan struct{}, 1)
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		chegou <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-chegou; cancel() }()
	var errb bytes.Buffer
	env := map[string]string{"HTTP_ADDR": addr}
	codigo := runHealthcheck(ctx, nil, &errb, func(k string) string { return env[k] })
	if codigo != saidaInterrompida {
		t.Errorf("interrompido: código %d (%s), queria %d", codigo, errb.String(), saidaInterrompida)
	}
}

// Argumento a mais é erro de uso (2), como no `user`: `vigilume healthcheck
// --porta 9000` não pode checar a 8080 e dizer que está tudo bem.
func TestOperationSubcommandsRejectExtraArgs(t *testing.T) {
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	env := map[string]string{"HTTP_ADDR": addr}
	var errb bytes.Buffer
	if codigo := runHealthcheck(context.Background(), []string{"--porta", "9000"}, &errb, func(k string) string { return env[k] }); codigo != saidaUso {
		t.Errorf("healthcheck com argumento: código %d, queria %d", codigo, saidaUso)
	}
	var out bytes.Buffer
	errb.Reset()
	if codigo := runVersion([]string{"x"}, &out, &errb); codigo != saidaUso || out.Len() != 0 {
		t.Errorf("version com argumento: código %d, saída %q; queria %d e nada no stdout", codigo, out.String(), saidaUso)
	}
}

// O prazo do healthcheck tem de caber o do /readyz (senão um banco lento vira
// "não pronto" com o /readyz ainda esperando o ping), e o timeout do
// healthcheck do compose tem de caber o prazo do binário (senão o Docker mata
// o processo antes de ele responder).
func TestHealthcheckDeadlinesNest(t *testing.T) {
	routes, err := os.ReadFile("../../internal/httpapi/routes.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^const readyzTimeout = (\d+) \* time\.Second$`).FindSubmatch(routes)
	if m == nil {
		t.Fatal("readyzTimeout não encontrado em internal/httpapi/routes.go")
	}
	readyz, _ := strconv.Atoi(string(m[1]))
	if healthcheckPrazo <= time.Duration(readyz)*time.Second {
		t.Errorf("healthcheckPrazo %v não é maior que o readyzTimeout %ds", healthcheckPrazo, readyz)
	}

	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m = regexp.MustCompile(`"/vigilume", "healthcheck"\]\n(?:[ \t]+(?:#|\w+:)[^\n]*\n)*?[ \t]+timeout: (\S+)\n`).FindSubmatch(compose)
	if m == nil {
		t.Fatal("timeout do healthcheck do app não encontrado no compose.yaml")
	}
	timeout, err := time.ParseDuration(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if timeout <= healthcheckPrazo {
		t.Errorf("timeout do healthcheck do compose (%v) não é maior que o prazo do binário (%v)", timeout, healthcheckPrazo)
	}
}
