package middleware

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// capturarAvisos troca o logger padrão e o relógio do aviso pelo tempo do
// teste. Não roda em paralelo: os dois são globais.
func capturarAvisos(t *testing.T, agora *time.Time) *bytes.Buffer {
	t.Helper()
	var log bytes.Buffer
	antigoLog, antigoAviso, antigoSem := slog.Default(), avisoDeProxy, avisoSemCabecalho
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	avisoDeProxy = &aviso{agora: func() time.Time { return *agora }, intervalo: time.Minute}
	avisoSemCabecalho = &aviso{agora: func() time.Time { return *agora }, intervalo: time.Minute}
	t.Cleanup(func() { slog.SetDefault(antigoLog); avisoDeProxy, avisoSemCabecalho = antigoAviso, antigoSem })
	return &log
}

// TestUntrustedHeaderWarnsAtMostOncePerMinute: com TRUSTED_PROXIES errado,
// TODA requisição cai no aviso. Sem o limite, o log vira só isso.
func TestUntrustedHeaderWarnsAtMostOncePerMinute(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Header: "Fly-Client-IP"}

	pedir := func() {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "172.19.0.5:5555"
		r.Header.Set("Fly-Client-IP", "198.51.100.7")
		ClientIP(r, px)
	}
	for range 100 {
		pedir()
	}
	if n := strings.Count(log.String(), "cabeçalho de IP do cliente ignorado"); n != 1 {
		t.Fatalf("%d avisos em 100 requisições no mesmo minuto, queria 1", n)
	}
	if !strings.Contains(log.String(), "remote_addr=172.19.0.5") {
		t.Errorf("o aviso não traz o remote_addr, que é o que falta para configurar: %s", log.String())
	}

	agora = agora.Add(time.Minute)
	pedir()
	if n := strings.Count(log.String(), "cabeçalho de IP do cliente ignorado"); n != 2 {
		t.Errorf("%d avisos depois de um minuto, queria 2", n)
	}
}

// Sem CLIENT_IP_HEADER (desenvolvimento, compose), um X-Forwarded-For de
// origem qualquer não gera aviso: seria ruído em toda máquina de dev.
func TestNoWarningWithoutTheHeaderSetting(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("Fly-Client-IP", "1.2.3.4")
	ClientIP(r, Proxies{})
	if log.Len() != 0 {
		t.Errorf("sem CLIENT_IP_HEADER saiu log: %s", log.String())
	}
}

// O caminho normal em produção (o proxy confiável com o cabeçalho legível)
// não pode gerar aviso nenhum: seria uma linha de log por minuto para sempre,
// e o aviso de verdade se perderia nela.
func TestTrustedPeerWithTheHeaderDoesNotWarn(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}, Header: "Fly-Client-IP"}

	for _, cliente := range []string{"198.51.100.7", "2001:db8::7", "::ffff:198.51.100.8"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "172.19.0.5:5555"
		r.Header.Set("Fly-Client-IP", cliente)
		ClientIP(r, px)
	}
	if log.Len() != 0 {
		t.Errorf("proxy confiável com o cabeçalho legível gerou log: %s", log.String())
	}
}

// Do proxy confiável, mas sem o cabeçalho (ou com um valor ilegível): o
// CLIENT_IP_HEADER quase certamente está com o nome errado, e todo cliente
// cai no balde do proxy. Aviso próprio, também limitado a um por minuto, e sem
// repetir o valor do cabeçalho.
func TestTrustedPeerWithoutAUsableHeaderWarnsAtMostOncePerMinute(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}, Header: "Fly-Client-IP"}
	const frase = "cabeçalho de IP do cliente ausente ou ilegível"

	pedir := func(valor string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "172.19.0.5:5555"
		if valor != "" {
			r.Header.Set("Fly-Client-IP", valor)
		}
		ClientIP(r, px)
	}
	// O ilegível primeiro: é ele que sai no log deste minuto, e o valor não
	// pode aparecer. O ausente sai no minuto seguinte.
	for range 50 {
		pedir("valor-secreto-ilegivel")
		pedir("")
	}
	if n := strings.Count(log.String(), frase); n != 1 {
		t.Fatalf("%d avisos em 100 requisições no mesmo minuto, queria 1: %s", n, log.String())
	}
	if !strings.Contains(log.String(), "remote_addr=172.19.0.5") || !strings.Contains(log.String(), "Fly-Client-IP") {
		t.Errorf("o aviso não traz o remote_addr e o nome do cabeçalho: %s", log.String())
	}
	if strings.Contains(log.String(), "valor-secreto-ilegivel") {
		t.Errorf("o aviso repete o valor do cabeçalho: %s", log.String())
	}

	agora = agora.Add(time.Minute)
	pedir("")
	if n := strings.Count(log.String(), frase); n != 2 {
		t.Errorf("%d avisos depois de um minuto, queria 2", n)
	}

	// Só o ilegível, sozinho no minuto, também avisa.
	agora = agora.Add(time.Minute)
	pedir("1.2.3.4, 5.6.7.8")
	if n := strings.Count(log.String(), frase); n != 3 {
		t.Errorf("%d avisos depois do valor ilegível sozinho, queria 3", n)
	}

	// Os dois avisos têm limites separados: o do cabeçalho forjado continua
	// saindo no mesmo minuto.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.Header.Set("Fly-Client-IP", "1.2.3.4")
	ClientIP(r, px)
	if !strings.Contains(log.String(), "cabeçalho de IP do cliente ignorado") {
		t.Errorf("o aviso de origem fora da lista sumiu depois do outro: %s", log.String())
	}
}
