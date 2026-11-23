// Package contaemail envia os e-mails de conta (confirmação, redefinição de
// senha, verificação de canal) com duas proteções de cota: o balde por
// destinatário e o teto diário global. A ordem é sempre balde, teto e
// transporte, para que um destinatário martelado não gaste o teto de todos.
//
// Os avisos de incidente (notify) nunca passam por aqui: o
// fronteiras_test.go confere que o notify não importa este pacote.
package contaemail

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
)

// TetoDiario é o máximo de e-mails de conta por dia UTC, somadas as réplicas.
const TetoDiario = 50

// PrazoDreno é o orçamento do encerramento de Rodar (o envio em curso mais
// o dreno), contado do cancelamento: quem desliga o processo o desconta do
// próprio prazo de encerramento.
const PrazoDreno = 10 * time.Second

const (
	baldeDestino   = "conta_destino"
	aCadaDestino   = 20 * time.Minute
	rajadaDestino  = 3
	capacidadeFila = 100
	prazoEnvio     = 15 * time.Second
)

// Transporte entrega o e-mail. *notify.Resend satisfaz.
type Transporte interface {
	SendAccount(ctx context.Context, to crypto.Secret, subject, body string) error
}

// Teto reserva uma vaga do teto diário; false quando esgotado.
type Teto interface {
	Reservar(ctx context.Context) (bool, error)
}

// Orcamentos é o balde compartilhado entre réplicas (ip_rate_limits).
type Orcamentos interface {
	Gastar(ctx context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error)
}

type mensagem struct {
	para, assunto, corpo string
}

// Enviador enfileira os e-mails de conta e os entrega em segundo plano, para
// que a latência do provedor não denuncie, no tempo de resposta, se a conta
// existe.
type Enviador struct {
	transporte Transporte
	teto       Teto
	orcamentos Orcamentos
	chave      []byte
	log        *slog.Logger
	fila       chan mensagem
	// prazoDreno é o orçamento do encerramento inteiro, contado do
	// cancelamento de ctx. Campo só para o teste o encurtar.
	prazoDreno time.Duration

	// mu protege parado: depois de Rodar voltar, ninguém consome a fila, e
	// SendAccountEmail registra a perda em vez de enfileirar.
	mu     sync.Mutex
	parado bool
}

// New monta o enviador. chave é a do HMAC (IP_HASH_KEY): o endereço não
// chega ao banco do balde.
func New(t Transporte, teto Teto, o Orcamentos, chave []byte, log *slog.Logger) *Enviador {
	return &Enviador{
		transporte: t, teto: teto, orcamentos: o, chave: chave, log: log,
		fila: make(chan mensagem, capacidadeFila), prazoDreno: PrazoDreno,
	}
}

// SendAccountEmail enfileira e volta nil. Com a fila cheia, descarta e
// registra no log: nunca bloqueia quem responde à requisição. Depois do
// encerramento de Rodar, também descarta e registra.
func (e *Enviador) SendAccountEmail(_ context.Context, to, subject, body string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.parado {
		e.log.Warn("e-mail de conta suprimido", "motivo", "encerrado")
		return nil
	}
	select {
	case e.fila <- mensagem{to, subject, body}:
	default:
		e.log.Warn("e-mail de conta suprimido", "motivo", "fila")
	}
	return nil
}

// Rodar consome a fila até ctx encerrar e então drena o que restou. O
// encerramento inteiro (o envio em curso mais o dreno) cabe em PrazoDreno,
// contado do cancelamento de ctx: os envios usam um contexto que não morre
// com ctx, mas que vence PrazoDreno depois dele. Cada envio tem ainda o
// prazo próprio de prazoEnvio.
func (e *Enviador) Rodar(ctx context.Context) {
	limite, cancelar := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelar()
	pararTimer := context.AfterFunc(ctx, func() { time.AfterFunc(e.prazoDreno, cancelar) })
	defer pararTimer()
	for ctx.Err() == nil {
		select {
		case m := <-e.fila:
			e.entregar(limite, m)
		case <-ctx.Done():
		}
	}
dreno:
	for limite.Err() == nil {
		select {
		case m := <-e.fila:
			e.entregar(limite, m)
		default:
			break dreno
		}
	}
	// O que ainda estiver na fila conta como descartado. Sob mu, nenhum
	// SendAccountEmail entra entre a contagem e o parado.
	e.mu.Lock()
	e.parado = true
	n := len(e.fila)
	e.mu.Unlock()
	if n > 0 {
		e.log.Warn("e-mails de conta descartados no encerramento", "quantidade", n)
	}
}

func (e *Enviador) entregar(ctx context.Context, m mensagem) {
	ctx, cancel := context.WithTimeout(ctx, prazoEnvio)
	defer cancel()
	liberado, err := e.liberar(ctx, m.para)
	if err != nil || !liberado {
		return
	}
	if err := e.transporte.SendAccount(ctx, crypto.Secret(m.para), m.assunto, m.corpo); err != nil {
		e.log.Error("e-mail de conta falhou", "erro", err)
	}
}

// liberar gasta o balde do destinatário e, só se houver ficha, uma vaga do
// teto. Suprimido volta false e nil, com o motivo no log e sem o endereço.
func (e *Enviador) liberar(ctx context.Context, para string) (bool, error) {
	chave := crypto.HashIP(e.chave, baldeDestino+":"+chaveDoDestino(para))
	_, ok, err := e.orcamentos.Gastar(ctx, baldeDestino, chave, aCadaDestino, rajadaDestino)
	if err != nil {
		e.log.Error("balde do destinatário falhou", "erro", err)
		return false, err
	}
	if !ok {
		e.log.Warn("e-mail de conta suprimido", "motivo", "destino")
		return false, nil
	}
	ok, err = e.teto.Reservar(ctx)
	if err != nil {
		e.log.Error("teto de e-mails de conta falhou", "erro", err)
		return false, err
	}
	if !ok {
		e.log.Warn("e-mail de conta suprimido", "motivo", "teto")
		return false, nil
	}
	return true, nil
}

// chaveDoDestino normaliza como o login e baixa a caixa: a coluna de e-mail é
// citext, então A@X.com e a@x.com são a mesma caixa de entrada e o mesmo balde.
func chaveDoDestino(para string) string {
	return strings.ToLower(auth.NormalizeEmail(para))
}

type verificacaoComTeto struct {
	e *Enviador
	m notify.VerificationMailer
}

// NovaVerificacaoComTeto põe a verificação de canal por e-mail atrás do
// balde e do teto, de forma síncrona. Suprimido volta nil (o canal fica
// pendente, e "reenviar" resolve); erro do balde, do teto ou do transporte
// volta como erro.
func NovaVerificacaoComTeto(e *Enviador, m notify.VerificationMailer) notify.VerificationMailer {
	return &verificacaoComTeto{e: e, m: m}
}

func (v *verificacaoComTeto) SendVerification(ctx context.Context, dest crypto.Secret, link string) error {
	liberado, err := v.e.liberar(ctx, string(dest.Reveal()))
	if err != nil || !liberado {
		return err
	}
	return v.m.SendVerification(ctx, dest, link)
}

// SemEnvio é o enviador com o e-mail desligado: registra e descarta.
type SemEnvio struct{ Log *slog.Logger }

// SendAccountEmail registra que o e-mail não saiu, sem o endereço, e volta nil.
func (s SemEnvio) SendAccountEmail(_ context.Context, _, _, _ string) error {
	s.Log.Warn("e-mail de conta não enviado", "motivo", "email_desligado")
	return nil
}
