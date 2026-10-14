package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

const (
	dispatchEvery = 2 * time.Second
	// claimTimeout é o prazo da reivindicação do lote, espera por conexão do
	// pool incluída. Fica abaixo de dispatchEvery: uma consulta presa não
	// para a entrega de todos os avisos (C2 da revisão). Os envios e as
	// gravações dos desfechos têm prazos próprios.
	claimTimeout = 1500 * time.Millisecond
	// maxConcurrentSends é o tamanho do lote e o teto de envios simultâneos.
	// O Resend aceita 10 pedidos por segundo por conta; 8 fica abaixo.
	maxConcurrentSends = 8
	// sendTimeout é o prazo de um envio (spec §8): um webhook lento não
	// segura o lote além disso.
	sendTimeout    = 10 * time.Second
	persistTimeout = 5 * time.Second
	// lease é quanto uma notificação reivindicada fica invisível para os
	// outros dispatchers; um processo que morre no meio devolve a notificação
	// à fila quando ele vence. O mesmo canal recebe em série, então o pior
	// lote é o lote inteiro num canal só, com cada envio lento mas bem
	// sucedido: maxConcurrentSends × (sendTimeout + persistTimeout), mais
	// folga. Menos que isso, um segundo dispatcher reenviaria.
	lease = maxConcurrentSends*(sendTimeout+persistTimeout) + time.Minute

	// MaxAttempts é o teto de tentativas (spec §8). Com o backoff abaixo, a
	// sexta tentativa acontece cerca de 15 minutos depois da primeira.
	MaxAttempts  = 6
	firstBackoff = 30 * time.Second
	maxBackoff   = time.Hour

	// rateLimitGrace é por quanto tempo, contado do enfileiramento, um 429
	// não gasta tentativa. Num apagão em massa, dezenas de alertas vão ao
	// mesmo webhook, e os 429 esgotariam as 6 tentativas justamente quando
	// o alerta mais importa. Depois disso, o 429 conta: um alerta com uma
	// hora de atraso já perdeu quase todo o valor, e um webhook que responde
	// 429 para sempre não segura a fila para sempre.
	rateLimitGrace = time.Hour

	// shutdownSendGrace é quanto, depois do sinal de encerramento, os envios
	// em curso ainda têm antes de serem abortados (C1 da revisão). Nenhum
	// envio novo começa depois do sinal.
	shutdownSendGrace = 7 * time.Second

	// ShutdownBudget é o máximo que Dispatch leva depois do sinal de
	// encerramento: os envios em curso, e depois a gravação dos desfechos. Tem
	// de caber no ShutdownGrace do app (TestDispatcherFitsTheShutdownGrace),
	// senão o processo sai com aviso entregue e ainda não marcado, que volta
	// pelo lease e sai duplicado.
	ShutdownBudget = shutdownSendGrace + persistTimeout
)

// errEncerramento é a causa do cancelamento dos envios abortados no
// encerramento.
var errEncerramento = errors.New("encerramento do dispatcher")

// Dispatcher consome a outbox de notificações (spec §8). Roda no papel do
// sondador, fora do caminho crítico da sonda.
type Dispatcher struct {
	pool         *pgxpool.Pool
	box          *crypto.Box
	clk          clock.Clock
	notifiers    map[Kind]Notifier
	total        *prometheus.CounterVec
	sendTimeout  time.Duration
	claimTimeout time.Duration
	// shutdownSendGrace é campo para que os testes o encurtem.
	shutdownSendGrace time.Duration
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
		sendTimeout: sendTimeout, claimTimeout: claimTimeout, shutdownSendGrace: shutdownSendGrace,
	}
	for _, n := range notifiers {
		d.notifiers[n.Kind()] = n
	}
	reg.MustRegister(d.total)
	return d
}

// Run despacha a cada dispatchEvery até ctx acabar. Um lote em andamento
// termina antes de Run voltar, em até ShutdownBudget (ver Dispatch).
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
//
// O fim de ctx é o sinal de encerramento. Depois dele, nenhum envio novo
// começa; os envios em curso têm shutdownSendGrace para terminar, e os
// desfechos, mais persistTimeout para serem gravados. O que não chegou a sair
// volta à fila na hora, sem gastar tentativa, em vez de esperar o lease.
func (d *Dispatcher) Dispatch(ctx context.Context) (int, error) {
	lote, err := d.reivindicar(ctx)
	if err != nil {
		return 0, err
	}

	envios, fimEnvios := sobrevida(ctx, d.shutdownSendGrace)
	defer fimEnvios()
	gravacoes, fimGravacoes := sobrevida(ctx, d.shutdownSendGrace+persistTimeout)
	defer fimGravacoes()
	p := prazos{parada: ctx, envios: envios, gravacoes: gravacoes}

	var wg sync.WaitGroup
	for _, fila := range porCanal(lote) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.entregarEmSerie(p, fila)
		}()
	}
	wg.Wait()
	return len(lote), nil
}

// prazos são os contextos de um lote, todos ligados ao sinal de
// encerramento.
type prazos struct {
	// parada acaba no sinal: nenhum envio começa depois dele.
	parada context.Context
	// envios acaba shutdownSendGrace depois do sinal, abortando os envios
	// em curso.
	envios context.Context
	// gravacoes acaba persistTimeout depois de envios: o desfecho de um
	// envio abortado ainda é gravado.
	gravacoes context.Context
}

// sobrevida devolve um contexto que acaba prazo depois de ctx acabar, com a
// causa errEncerramento. A função devolvida o encerra antes disso.
func sobrevida(ctx context.Context, prazo time.Duration) (context.Context, context.CancelFunc) {
	sob, cancelar := context.WithCancelCause(context.WithoutCancel(ctx))
	parar := context.AfterFunc(ctx, func() {
		time.AfterFunc(prazo, func() { cancelar(errEncerramento) })
	})
	return sob, func() {
		parar()
		cancelar(context.Canceled)
	}
}

// reivindicar pega o lote, com o prazo claimTimeout.
func (d *Dispatcher) reivindicar(ctx context.Context) ([]db.ClaimDueNotificationsRow, error) {
	ctx, cancel := context.WithTimeout(ctx, d.claimTimeout)
	defer cancel()
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
		return nil, fmt.Errorf("reivindicar notificações: %w", err)
	}
	return lote, nil
}

// porCanal separa o lote por canal, cada fila na ordem de enfileiramento
// (os ids são UUID v7). Canais diferentes saem em paralelo; o mesmo canal,
// em série: dezenas de pedidos simultâneos ao mesmo webhook só compram 429,
// e em série a abertura sai antes do fechamento.
func porCanal(lote []db.ClaimDueNotificationsRow) [][]db.ClaimDueNotificationsRow {
	indice := map[uuid.UUID]int{}
	var filas [][]db.ClaimDueNotificationsRow
	for _, n := range lote {
		i, ok := indice[n.ChannelID]
		if !ok {
			i = len(filas)
			indice[n.ChannelID] = i
			filas = append(filas, nil)
		}
		filas[i] = append(filas[i], n)
	}
	for _, f := range filas {
		slices.SortFunc(f, func(a, b db.ClaimDueNotificationsRow) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	}
	return filas
}

// entregarEmSerie entrega a fila de um canal. Depois de uma falha
// transitória (429, timeout, rede, 5xx), o resto da fila não é enviado: o
// canal está pedindo espera ou está fora, e mandar mais só gastaria até
// sendTimeout por notificação. Sem isso, um webhook que trava seguraria o
// lote inteiro por k × sendTimeout, e os outros canais esperariam junto
// (achado da revisão).
//
// Com o sinal de encerramento, o resto da fila volta à fila sem envio.
func (d *Dispatcher) entregarEmSerie(p prazos, fila []db.ClaimDueNotificationsRow) {
	var parou *desfecho
	for _, n := range fila {
		switch {
		case p.parada.Err() != nil:
			d.liberar(p, n)
		case parou != nil:
			d.adiar(p, n, *parou)
		default:
			if f := d.entregarProtegido(p, n); f.transitoria {
				parou = &f
			}
		}
	}
}

// liberar devolve à fila, para já e sem gastar tentativa, uma notificação
// que o encerramento impediu de sair: outro dispatcher, ou este processo
// quando voltar, a envia sem esperar o lease.
func (d *Dispatcher) liberar(p prazos, n db.ClaimDueNotificationsRow) {
	d.total.WithLabelValues(n.Kind, "deferred").Inc()
	if err := d.persistir(p.gravacoes, n, interrompida(d.clk.Now())); err != nil {
		slog.Error("devolver notificação no encerramento", "notification_id", n.ID, "erro", err)
	}
}

// interrompida é o desfecho de uma notificação que o encerramento parou:
// pendente para agora, com a tentativa devolvida. A falha é do StillOn, e
// não do canal.
func interrompida(agora time.Time) desfecho {
	return desfecho{status: "pending", proxima: agora, devolver: true}
}

// entregarProtegido isola o panic de um envio: a notificação fica como a
// reivindicação a deixou e volta quando o lease vencer, e o resto da fila
// do canal segue.
func (d *Dispatcher) entregarProtegido(p prazos, n db.ClaimDueNotificationsRow) (f desfecho) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic no envio de notificação", "notification_id", n.ID, "panic", fmt.Sprint(r))
			f = desfecho{}
		}
	}()
	return d.entregar(p, n)
}

// perdoa429 diz se um 429 para esta notificação ainda não gasta tentativa.
func (d *Dispatcher) perdoa429(n db.ClaimDueNotificationsRow) bool {
	return d.clk.Now().Sub(n.CreatedAt) < rateLimitGrace
}

// adiar devolve à fila, sem enviar, uma notificação cujo canal acabou de
// falhar de forma transitória. Só o 429 dentro de rateLimitGrace devolve a
// tentativa: é o provedor pedindo espera. Nos outros casos a tentativa conta,
// como contaria se ela tivesse sido enviada e falhado igual, e quem já está
// no teto falha de vez, sem esperar outro sendTimeout.
func (d *Dispatcher) adiar(p prazos, n db.ClaimDueNotificationsRow, parou desfecho) {
	f, decidido := semEnvio(n)
	if !decidido {
		proxima := parou.proxima
		if proxima.IsZero() { // quem parou o canal já tinha falhado de vez
			proxima = d.clk.Now().Add(backoff(n.Attempts))
		}
		f = desfecho{status: "pending", reason: parou.reason, proxima: proxima}
		switch {
		case parou.reason == ReasonRateLimited && d.perdoa429(n):
			f.devolver = true
		case n.Attempts >= MaxAttempts:
			f = desfecho{status: "failed", reason: parou.reason}
		}
	}
	// "deferred" é só o que volta à fila; o resto conta pelo desfecho.
	rotulo := "deferred"
	if f.status != "pending" {
		rotulo = resultado(f.status)
	}
	d.total.WithLabelValues(n.Kind, rotulo).Inc()
	if f.status == "failed" && !f.doSistema {
		slog.Warn("notificação falhou de vez", "notification_id", n.ID, "channel_id", n.ChannelID, "motivo", f.reason)
	}
	if err := d.persistir(p.gravacoes, n, f); err != nil {
		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
	}
}

// desfecho é o que fazer com a notificação depois da tentativa.
type desfecho struct {
	status  string // sent, pending (retentativa), failed ou cancelled
	reason  Reason
	proxima time.Time
	// devolver desconta a tentativa que a reivindicação contou (429 dentro
	// de rateLimitGrace).
	devolver bool
	// transitoria: o envio falhou por um motivo que passa (429, timeout,
	// rede, 5xx), mesmo que a notificação tenha esgotado as tentativas. O
	// resto da fila do canal no lote é adiado.
	transitoria bool
	// doSistema: a falha foi do StillOn, e não do canal (o desfecho da
	// última tentativa não chegou a ser gravado). Não marca o canal failing.
	doSistema bool
}

func (d *Dispatcher) entregar(p prazos, n db.ClaimDueNotificationsRow) desfecho {
	f := d.tentar(p.envios, n)
	d.total.WithLabelValues(n.Kind, resultado(f.status)).Inc()
	if f.status == "failed" {
		// Só o motivo vai ao log, nunca o destino.
		slog.Warn("notificação falhou de vez", "notification_id", n.ID, "channel_id", n.ChannelID, "motivo", f.reason)
	}
	if err := d.persistir(p.gravacoes, n, f); err != nil {
		slog.Error("gravar desfecho de notificação", "notification_id", n.ID, "erro", err)
	}
	return f
}

// semEnvio decide o que não depende de enviar: canal desabilitado ou não
// verificado, abertura velha, lembrete de incidente resolvido ou reconhecido,
// linha que voltou do lease além do teto. Vale
// para quem vai ser enviado (tentar) e para quem vai ser adiado (adiar):
// uma abertura velha é cancelada mesmo quando o canal parou no lote.
func semEnvio(n db.ClaimDueNotificationsRow) (desfecho, bool) {
	// Spec §5.8: a verificação vale no ENVIO, não só no fan-out. Um canal
	// desabilitado depois de enfileirado não recebe mais nada.
	if !n.Enabled || n.VerifiedAt == nil {
		return desfecho{status: "cancelled"}, true
	}
	// Uma abertura que ainda não saiu quando o incidente já fechou fica
	// velha: o fechamento diz quando caiu e quando voltou.
	if n.Event == incident.EventOpened && n.ResolvedAt != nil {
		return desfecho{status: "cancelled"}, true
	}
	// Um lembrete só faz sentido com o incidente aberto e sem ack:
	// reconhecer interrompe os lembretes, inclusive os que já estão na fila.
	if n.Event == incident.EventReminder && (n.ResolvedAt != nil || n.AckAt != nil) {
		return desfecho{status: "cancelled"}, true
	}
	// A reivindicação já contou esta tentativa. Passar do teto só acontece
	// quando o desfecho da última não chegou a ser gravado (processo morto,
	// commit que falhou) e a linha voltou quando o lease venceu: ela falha
	// de vez sem enviar, porque o teto é de envios.
	if n.Attempts > MaxAttempts {
		return desfecho{status: "failed", reason: ReasonUnknown, doSistema: true}, true
	}
	return desfecho{}, false
}

// envios é o contexto do lote: acaba quando o encerramento aborta os envios.
func (d *Dispatcher) tentar(envios context.Context, n db.ClaimDueNotificationsRow) desfecho {
	if f, ok := semEnvio(n); ok {
		return f
	}
	canal, ok := d.notifiers[Kind(n.Kind)]
	if !ok {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}
	dest, err := openConfig(d.box, n.ChannelID, n.ConfigEnc)
	if err != nil {
		return desfecho{status: "failed", reason: ReasonInvalidChannel}
	}

	ctx, cancel := context.WithTimeout(envios, d.sendTimeout)
	defer cancel()
	err = canal.Send(ctx, dest, Event{
		NotificationID: n.ID, Kind: n.Event, TargetName: n.TargetName,
		StartedAt: n.StartedAt, ResolvedAt: valorOuZero(n.ResolvedAt), QueuedAt: n.CreatedAt, Cause: n.Cause,
	})
	if err == nil {
		return desfecho{status: "sent"}
	}
	// Abortado pelo encerramento: volta à fila para já. Se o destino chegou
	// a receber, sai duplicado, mas só este aviso, e não o lote inteiro
	// depois do lease.
	if errors.Is(context.Cause(envios), errEncerramento) {
		return interrompida(d.clk.Now())
	}

	var se *SendError
	if !errors.As(err, &se) {
		se = &SendError{Reason: ReasonUnknown}
		if errors.Is(err, context.DeadlineExceeded) {
			se.Reason = ReasonTimeout
		}
	}
	if se.Permanent {
		return desfecho{status: "failed", reason: se.Reason}
	}
	espera := max(backoff(n.Attempts), se.RetryAfter)
	if se.Reason == ReasonRateLimited && d.perdoa429(n) {
		return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), devolver: true, transitoria: true}
	}
	if n.Attempts >= MaxAttempts {
		return desfecho{status: "failed", reason: se.Reason, transitoria: true}
	}
	return desfecho{status: "pending", reason: se.Reason, proxima: d.clk.Now().Add(espera), transitoria: true}
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

// gravacoes é o contexto do lote: acaba persistTimeout depois de os envios
// serem abortados no encerramento.
func (d *Dispatcher) persistir(gravacoes context.Context, n db.ClaimDueNotificationsRow, f desfecho) error {
	ctx, cancel := context.WithTimeout(gravacoes, persistTimeout)
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
			return q.RetryNotification(ctx, db.RetryNotificationParams{
				ID: n.ID, NextAttemptAt: f.proxima, LastError: motivo, Refund: f.devolver,
			})
		case "cancelled":
			return q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "cancelled", LastError: motivo})
		}
		if err := q.CloseNotification(ctx, db.CloseNotificationParams{ID: n.ID, Status: "failed", LastError: motivo}); err != nil {
			return err
		}
		// O canal passa a "failing" até a próxima entrega que der certo
		// (pendência do Plano 3: antes, a falha só ia para a auditoria). A
		// falha do próprio StillOn não é culpa do canal.
		if !f.doSistema {
			if err := q.MarkChannelFailure(ctx, db.MarkChannelFailureParams{ChannelID: n.ChannelID, Now: d.clk.Now(), LastError: motivo}); err != nil {
				return err
			}
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
