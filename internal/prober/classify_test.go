package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

func TestClassify(t *testing.T) {
	embrulhar := func(err error) error { return &url.Error{Op: "Get", URL: "https://x", Err: err} }

	cases := []struct {
		nome string
		err  error
		quer ErrorKind
	}{
		{"bloqueado no dial", embrulhar(&net.OpError{Op: "dial", Err: fmt.Errorf("%w: 10.0.0.5", safedial.ErrBlocked)}), KindBlocked},
		{"URL recusada pelo safedial", embrulhar(fmt.Errorf("%w: esquema", safedial.ErrInvalidURL)), KindBlocked},
		// C3 (Plano 10): o redirecionamento recusado não é proteção de rede.
		{"redirecionamento recusado", embrulhar(fmt.Errorf("%w: mais de 3 redirecionamentos", safedial.ErrRedirect)), KindUnknown},
		{"prazo no cabeçalho", embrulhar(context.DeadlineExceeded), KindTimeout},
		{"prazo no corpo, sem url.Error", context.DeadlineExceeded, KindTimeout},
		{"DNS inexistente", embrulhar(&net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}), KindDNS},
		{"DNS que demorou", embrulhar(&net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}), KindTimeout},
		{"conexão recusada", embrulhar(&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), KindConnRefused},
		{"certificado desconhecido", embrulhar(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), KindTLS},
		{"cabeçalho TLS inválido", embrulhar(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), KindTLS},
		{"hostname errado no certificado", embrulhar(x509.HostnameError{Host: "x"}), KindTLS},
		{"conexão cortada no meio", embrulhar(io.ErrUnexpectedEOF), KindUnknown},
		{"reset", embrulhar(&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), KindUnknown},
		{"qualquer outro", errors.New("??"), KindUnknown},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if got := Classify(c.err); got != c.quer {
				t.Errorf("Classify = %q, queria %q", got, c.quer)
			}
		})
	}
}
