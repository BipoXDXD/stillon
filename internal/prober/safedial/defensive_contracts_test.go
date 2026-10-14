package safedial_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

func TestValidateURLRejectsMalformedEscape(t *testing.T) {
	parsed, err := safedial.ValidateURL("https://exemplo.com/%zz")
	if parsed != nil {
		t.Errorf("URL malformada aceita: %v", parsed)
	}
	if !errors.Is(err, safedial.ErrInvalidURL) {
		t.Errorf("erro = %v, queria ErrInvalidURL", err)
	}
}

// DialContext é uma fronteira pública, embora HTTP normalmente use TCP.
// Uma exceção para loopback TCP nunca deve liberar um socket Unix local.
func TestLoopbackExceptionDoesNotAllowUnixSocket(t *testing.T) {
	// No macOS o caminho de um socket Unix cabe em 104 bytes, e o t.TempDir()
	// leva o nome longo do teste para dentro do $TMPDIR comprido do sistema.
	dir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	address := filepath.Join(dir, "alvo.sock")
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	control, err := net.DialTimeout("unix", address, time.Second)
	if err != nil {
		t.Fatalf("socket de controle inacessível: %v", err)
	}
	control.Close()

	client := safedial.NewClientAllowing(time.Second, netip.MustParseAddrPort("127.0.0.1:8080"))
	transport := client.Transport.(*http.Transport)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := transport.DialContext(ctx, "unix", address)
	if conn != nil {
		conn.Close()
		t.Error("a exceção TCP permitiu uma conexão Unix")
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria ErrBlocked", err)
	}
}

// O callback também deve negar confiança a uma origem IDNA inválida.
// Este contrato defensivo não representa um redirect HTTP ponta a ponta:
// o transporte padrão recusa a origem antes de receber uma resposta.
func TestRedirectFromInvalidIDNAOriginDropsTargetHeaders(t *testing.T) {
	origin, err := http.NewRequest(http.MethodGet, "https://\u200d.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := http.NewRequest(http.MethodGet, "https://exemplo.com/destino", nil)
	if err != nil {
		t.Fatal(err)
	}
	next.Header.Set("Authorization", "Bearer sentinela")
	next.Header.Set("Cookie", "sessao=sentinela")
	next.Header.Set("X-Token", "sentinela")
	next.Header.Set("User-Agent", "identificacao-do-alvo")

	client := safedial.NewClient(time.Second)
	if err := client.CheckRedirect(next, []*http.Request{origin}); err != nil {
		t.Fatalf("callback de redirect: %v", err)
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Token"} {
		if value := next.Header.Get(name); value != "" {
			t.Errorf("%s acompanhou a origem inválida: %q", name, value)
		}
	}
	if got := next.Header.Get("User-Agent"); got != safedial.UserAgent {
		t.Errorf("User-Agent = %q, queria identificação pública %q", got, safedial.UserAgent)
	}
	if len(next.Header) != 1 {
		t.Errorf("headers restantes = %v, queria somente User-Agent", next.Header)
	}
}
