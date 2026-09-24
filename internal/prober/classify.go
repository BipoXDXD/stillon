package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

type ErrorKind string

// Os valores são os do CHECK de checks.error_kind (migration 00004). A
// mensagem crua do erro nunca sai do processo (spec §4.2, §5.6): uma string
// como "dial tcp 10.0.3.42:5432: connect: connection refused" publicaria
// topologia interna. Só o tipo sai.
const (
	KindTimeout     ErrorKind = "timeout"
	KindDNS         ErrorKind = "dns"
	KindTLS         ErrorKind = "tls"
	KindConnRefused ErrorKind = "conn_refused"
	KindHTTPStatus  ErrorKind = "http_status"
	// KindBodyLimit está no enum da spec, e o Plano 2 nunca o emite: corpo
	// grande é lido até 64 KiB e descartado, não é falha (D2).
	KindBodyLimit ErrorKind = "body_limit"
	KindBlocked   ErrorKind = "blocked"
	// KindUnknown é o residual (D1): reset, EOF no meio da resposta, erro de
	// protocolo. Sem ele, cada um teria de fingir ser outra coisa.
	KindUnknown ErrorKind = "unknown"
)

// Classify reduz um erro de rede ao enum. A ordem importa: o bloqueio vem
// primeiro porque o erro do safedial chega embrulhado num *net.OpError, e o
// DNS antes do timeout genérico porque *net.DNSError também responde
// Timeout().
//
// Cancelamento com causa (o shutdown da Task 7) NÃO é reconhecido aqui, e de
// propósito: nesse caso o erro devolvido é a própria causa, e não
// context.Canceled. Quem cancelou é que sabe, olhando context.Cause.
func Classify(err error) ErrorKind {
	if errors.Is(err, safedial.ErrBlocked) || errors.Is(err, safedial.ErrInvalidURL) {
		return KindBlocked
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return KindTimeout
		}
		return KindDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return KindConnRefused
	}
	if isTLS(err) {
		return KindTLS
	}
	return KindUnknown
}

func isTLS(err error) bool {
	var (
		verificacao *tls.CertificateVerificationError
		registro    tls.RecordHeaderError
		alerta      tls.AlertError
		autoridade  x509.UnknownAuthorityError
		hostname    x509.HostnameError
		invalido    x509.CertificateInvalidError
	)
	return errors.As(err, &verificacao) || errors.As(err, &registro) ||
		errors.As(err, &alerta) || errors.As(err, &autoridade) ||
		errors.As(err, &hostname) || errors.As(err, &invalido)
}
