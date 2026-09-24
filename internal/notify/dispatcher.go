package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/incident"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

const (
	dispatchEvery = 2 * time.Second
	// maxConcurrentSends é o tamanho do lote e o teto de envios simultâneos.
	// O Resend aceita 10 pedidos por segundo por conta; 8 fica abaixo.
	maxConcurrentSends = 8
	// sendTimeout é o prazo de um envio (spec §8): um webhook lento não
	// segura o lote além disso.
	sendTimeout = 10 * time.Second
	// lease é quanto uma notificação reivindicada fica invisível para os
	// outros dispatchers. Tem de passar com folga de sendTimeout +
	// persistTimeout; um processo que morre no meio devolve a notificação à
	// fila quando o lease vence.
	lease          = 2 * time.Minute
	persistTimeout = 5 * time.Second

	// MaxAttempts é o teto de tentativas (spec §8). Com o backoff abaixo, a
	// sexta tentativa acontece cerca de 15 minutos depois da primeira.
	MaxAttempts  = 6
	firstBackoff = 30 * time.Second
	maxBackoff   = time.Hour
)

// Dispatcher consome a outbox de notificações (spec §8). Roda no papel do
// sondador, fora do caminho crítico da sonda.
type Dispatcher struct {
	pool        *pgxpool.Pool
	box         *crypto.Box
	clk         clock.Clock
	notifiers   map[Kind]Notifier
	total       *prometheus.CounterVec
	sendTimeout time.Duration
}

// NewDispatcher recebe os canais que o operador configurou. Um canal de
// e-mail com o Resend desligado falha de forma definitiva no envio, em vez
// de ficar pendente para sempre.
func NewDispatcher(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, reg prometheus.Registerer, notifiers ...Notifier) *Dispatcher {
	d := &Dispatcher{
		pool: pool, box: box, clk: clk, notifiers: map[Kind]Notifier{},
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_total",
			Help: "Desfechos de envio de notificação, por canal e resultado.",
		}, []string{"kind", "result"}),
		sendTimeout: sendTimeout,
	}
	for _, n := range notifiers {
		d.notifiers[n.Kind()] = n
	}
	reg.MustRegister(d.total)
	return d
}

// Run despacha a cada dispatchEvery até ctx acabar. Um lote em andamento
// termina antes de Run voltar: os envios têm prazo próprio, e não herdam o
// cancelamento do shutdown.
func (d *Dispatcher) Run(ctx context.Context) error {
	t := time.NewTicker(dispatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := d.Dispatch(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar notificações", "erro", err)
			}
		}
	}
}

// Dispatch reivindica um lote, envia em paralelo e espera todos terminarem.
// Devolve quantas notificações reivindicou.
func (d *Dispatcher) Dispatch(ctx context.Context) (int, error) {
	now := d.clk.Now()
	var lote []db.ClaimDueNotificationsRow
	err := storage.InProberTx(ctx, d.pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: now, LeaseUntil: now.Add(lease), MaxRows: maxConcurrentSends,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("reivindicar notificações: %w", err)
	}

	var wg sync.WaitGroup
	for _, n := range lote {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Um panic num canal não derruba o processo. A notificação fica
			// como está e volta quando o lease vencer.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic no envio de notificação", "notification_id", n.ID, "panic", fmt.Sprint(r))
				}
			}()
			d.entregar(n)
		}()
	}
	wg.Wait()
	return len(lote), nil
}

// desfecho é o que fazer com a notificação depois da tentativa.
type desfecho struct {
	status  string // sent, pending (retentativa), failed ou cancelled
	reason  Reason
	proxima time.Time
}

func (d *Dispatcher) entregar(n db.ClaimDueNotificationsRow) {
	f := d.tentar(n)
	d.total.WithLabelValues(n.Kind, resultado(f.status)).Inc()
	if f.status == "failed" {
		// Só o motivo vai ao log, nunca o destino.
		slog.Warn("notificação falhou de vez", "notification_id", n.ID, "channel_id", n.ChannelID, "motivo", f.reason)
	}
	if err := d.persistir(n, f); err != nil {
		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
	}
}

func (d *Dispatcher) tentar(n db.ClaimDueNotificationsRow) desfecho {
	// Spec §5.8: a verificação vale no ENVIO, não só no fan-out. Um canal
	// desabilitado depois de enfileirado não recebe mais nada.
	if !n.Enabled || n.VerifiedAt == nil {
		return desfecho{status: "cancelled"}
	}
	// Uma abertura que ainda não saiu quando o incidente já fechou fica
	// velha: o fechamento diz quando caiu e quando voltou.
	if n.Event == incident.EventOpened && n.ResolvedAt != nil {
		return desfecho{status: "cancelled"}
	}
	// A reivindicação já contou esta tentativa. Passar do teto só acontece
	// quando o desfecho da última não chegou a ser gravado (processo morto,
	// commit que falhou) e a linha voltou quando o lease venceu: ela falha
	// de vez sem enviar, porque o teto é de envios.
	if n.Attempts > MaxAttempts {
		return desfecho{status: "failed", reason: ReasonUnknown}
	}
	canal, ok := d.notifiers[Kind(n.Kind)]
	if !ok {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}
	dest, err := openConfig(d.box, n.ChannelID, n.ConfigEnc)
	if err != nil {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.sendTimeout)
	defer cancel()
	err = canal.Send(ctx, dest, Event{
		NotificationID: n.ID, Kind: n.Event, TargetName: n.TargetName,
		StartedAt: n.StartedAt, ResolvedAt: valorOuZero(n.ResolvedAt), Cause: n.Cause,
	})
	if err == nil {
		return desfecho{status: "sent"}
	}

	var se *SendError
	if !errors.As(err, &se) {
		se = &SendError{Reason: ReasonUnknown}
		if errors.Is(err, context.DeadlineExceeded) {
			se.Reason = ReasonTimeout
		}
	}
	if se.Permanent || n.Attempts >= MaxAttempts {
		return desfecho{status: "failed", reason: se.Reason}
	}
	espera := max(backoff(n.Attempts), se.RetryAfter)
	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera)}
}

// backoff dobra a cada tentativa a partir de firstBackoff: 30s, 1min,
// 2min, 4min, 8min.
func backoff(tentativa int32) time.Duration {
	d := firstBackoff
	for i := int32(1); i < tentativa && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

func (d *Dispatcher) persistir(n db.ClaimDueNotificationsRow, f desfecho) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	var motivo *string
	if f.reason != "" {
		r := string(f.reason)
		motivo = &r
	}
	return storage.InProberTx(ctx, d.pool, func(q *db.Queries) error {
		switch f.status {
		case "sent":
			if err := q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: n.ID, Now: d.clk.Now()}); err != nil {
				return err
			}
			return q.MarkChannelSuccess(ctx, db.MarkChannelSuccessParams{ChannelID: n.ChannelID, Now: d.clk.Now()})
		case "pending":
			return q.RetryNotification(ctx, db.RetryNotificationParams{ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo})
		case "cancelled":
			return q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "cancelled", LastError: motivo})
		}
		if err := q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "failed", LastError: motivo}); err != nil {
			return err
		}
		// O canal passa a "failing" até a próxima entrega que der certo
		// (pendência do Plano 3: antes, a falha só ia para a auditoria).
		if err := q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: n.ChannelID, Now: d.clk.Now(), LastError: motivo}); err != nil {
			return err
		}
		// Falha definitiva vai para a trilha de auditoria (spec §8).
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		entidade := "notif_channel"
		return q.InsertAudit(ctx, db.InsertAuditParams{
			ID: id, UserID: &n.UserID, Action: "notification.failed",
			Entity: &entidade, EntityID: &n.ChannelID, At: d.clk.Now(),
		})
	})
}

// resultado é o rótulo de notify_total: "pending" vira "retry".
func resultado(status string) string {
	if status == "pending" {
		return "retry"
	}
	return status
}

func valorOuZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
