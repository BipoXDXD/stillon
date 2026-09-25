// Package notify entrega as notificações de incidente (spec §8): os dois
// canais (Discord e e-mail pelo Resend), o dispatcher que consome a outbox
// e o cadastro de canais com verificação de destino (spec §5.8).
//
// Canais, dispatcher e cadastro moram num pacote só de propósito: o
// dispatcher precisa decifrar a config que o cadastro cifra, e o cadastro
// precisa dos canais para a mensagem de teste. Em pacotes separados, isso
// seria um ciclo de importação ou um pacote de tipos sem comportamento.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

type Kind string

const (
	KindDiscord Kind = "discord"
	KindEmail   Kind = "email"
)

// Event é o que uma notificação diz. Tudo nele sai do banco, e nada do
// relógio: uma retentativa monta exatamente a mesma mensagem. O Resend
// exige isso, porque recusa a mesma Idempotency-Key com outro corpo.
type Event struct {
	NotificationID uuid.UUID
	Kind           string // incident.EventOpened ou incident.EventResolved
	TargetName     string
	StartedAt      time.Time
	ResolvedAt     time.Time // zero enquanto o incidente está aberto
	Cause          string
}

// Notifier é a interface da spec §8. dest é a URL do webhook ou o endereço
// de e-mail, já decifrados.
type Notifier interface {
	Kind() Kind
	Send(ctx context.Context, dest crypto.Secret, ev Event) error
}

// Reason é o motivo de uma falha de envio. Os valores são os do CHECK de
// notifications.last_error (migration 00005).
type Reason string

const (
	ReasonTimeout        Reason = "timeout"
	ReasonNetwork        Reason = "network"
	ReasonRateLimited    Reason = "rate_limited"
	ReasonRejected       Reason = "rejected"
	ReasonServerError    Reason = "server_error"
	ReasonInvalidChannel Reason = "invalid_channel"
	ReasonUnknown        Reason = "unknown"
)

// SendError é a falha de um envio, já classificada. Não carrega o erro de
// origem de propósito: o *url.Error do net/http traz a URL do webhook, com
// o token dentro, e um %v descuidado num log a publicaria.
type SendError struct {
	Reason    Reason
	Permanent bool
	// RetryAfter é o prazo que o provedor pediu (cabeçalho Retry-After num
	// 429). Zero quando ele não pediu nada.
	RetryAfter time.Duration
}

func (e *SendError) Error() string {
	return fmt.Sprintf("envio falhou: %s (permanente: %v)", e.Reason, e.Permanent)
}

// maxRetryAfter limita o Retry-After que o provedor pode impor.
const maxRetryAfter = time.Hour

// classificarResposta traduz o status HTTP. 2xx é entregue; 429 e 5xx são
// transitórios; o resto dos 4xx é recusa do provedor, e repetir o mesmo
// pedido daria a mesma recusa.
func classificarResposta(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return &SendError{Reason: ReasonRateLimited, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode >= 500:
		return &SendError{Reason: ReasonServerError}
	default:
		return &SendError{Reason: ReasonRejected, Permanent: true}
	}
}

// classificarErro traduz a falha de rede. Destino bloqueado pelo safedial é
// permanente: o mesmo endereço será bloqueado de novo. O redirecionamento
// recusado também (o webhook que redireciona sem fim continuará assim); até o
// Plano 10 ele chegava aqui como ErrBlocked.
func classificarErro(err error) error {
	switch {
	case errors.Is(err, safedial.ErrBlocked), errors.Is(err, safedial.ErrInvalidURL), errors.Is(err, safedial.ErrRedirect):
		return &SendError{Reason: ReasonInvalidChannel, Permanent: true}
	case errors.Is(err, context.DeadlineExceeded):
		return &SendError{Reason: ReasonTimeout}
	default:
		return &SendError{Reason: ReasonNetwork}
	}
}

// retryAfter lê o Retry-After em segundos, que é como o Discord e o Resend
// o mandam (o Discord, com fração). A forma de data HTTP vira zero, e o
// backoff do dispatcher decide.
func retryAfter(v string) time.Duration {
	s, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	// ParseFloat aceita "NaN" e "Inf"; converter qualquer um deles para
	// Duration é indefinido.
	if err != nil || math.IsNaN(s) || math.IsInf(s, 0) || s <= 0 {
		return 0
	}
	if s >= maxRetryAfter.Seconds() {
		return maxRetryAfter
	}
	return time.Duration(s * float64(time.Second))
}

// drenar lê um pedaço do corpo e fecha. Ninguém usa o conteúdo; ler até um
// limite é o que deixa a conexão fechar limpa.
func drenar(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

const userAgent = "Vigilume/1.0 (+monitor de disponibilidade)"
