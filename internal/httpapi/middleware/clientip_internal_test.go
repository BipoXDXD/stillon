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
	antigoLog, antigoAviso := slog.Default(), avisoDeProxy
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	avisoDeProxy = &aviso{agora: func() time.Time { return *agora }, intervalo: time.Minute}
	t.Cleanup(func() { slog.SetDefault(antigoLog); avisoDeProxy = antigoAviso })
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
