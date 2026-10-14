package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const webhookDeTeste = "https://discord.com/api/webhooks/1/tok"

// canalFalso registra os envios e devolve, em ordem, as respostas dadas.
// Sem resposta programada, entrega.
type canalFalso struct {
	mu        sync.Mutex
	respostas []error
	envios    []Event
	destinos  []string
	bloquear  bool
	panico    bool
	// chegou, quando não é nil, recebe um sinal a cada envio que começa.
	chegou chan struct{}
}

func (*canalFalso) Kind() Kind { return KindDiscord }

func (c *canalFalso) Send(ctx context.Context, dest crypto.Secret, ev Event) error {
	c.mu.Lock()
	c.envios = append(c.envios, ev)
	c.destinos = append(c.destinos, string(dest.Reveal()))
	var resp error
	if len(c.respostas) > 0 {
		resp, c.respostas = c.respostas[0], c.respostas[1:]
	}
	bloquear, panico, chegou := c.bloquear, c.panico, c.chegou
	c.mu.Unlock()
	if chegou != nil {
		chegou <- struct{}{}
	}
	if panico {
		panic("canal quebrado")
	}
	if bloquear {
		<-ctx.Done()
		return ctx.Err()
	}
	return resp
}

func (c *canalFalso) quantos() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.envios)
}

type bancada struct {
	d         *Dispatcher
	pool      *pgxpool.Pool
	clk       *clock.Fake
	box       *crypto.Box
	canal     *canalFalso
	dono      uuid.UUID
	canalID   uuid.UUID
	incidente uuid.UUID
}

var chaveDeTeste = []byte("chave-mestra-de-teste-32-bytes!!")

// novaBancada monta usuário, alvo, incidente aberto e um canal Discord
// verificado, com o destino cifrado de verdade. mexer ajusta o canal antes
// de gravar.
func novaBancada(t *testing.T, mexer func(*db.InsertChannelParams)) *bancada {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	box, err := crypto.NewBox(chaveDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	b := &bancada{pool: pool, clk: clk, box: box, canal: &canalFalso{}, dono: uuid.Must(uuid.NewV7())}
	b.d = NewDispatcher(pool, box, clk, prometheus.NewRegistry(), b.canal)

	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		b.dono, b.dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	alvo := uuid.Must(uuid.NewV7())
	agora := clk.Now()
	b.canalID = uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(box, b.canalID, webhookDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	canal := db.InsertChannelParams{
		ID: b.canalID, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
	}
	if mexer != nil {
		mexer(&canal)
	}
	err = storage.InUserTx(ctx, pool, b.dono, func(q *db.Queries) error {
		if err := q.InsertTarget(ctx, db.InsertTargetParams{
			ID: alvo, UserID: b.dono, Name: "API", URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		}); err != nil {
			return err
		}
		return q.InsertChannel(ctx, canal)
	})
	if err != nil {
		t.Fatal(err)
	}
	b.incidente = uuid.Must(uuid.NewV7())
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: b.incidente, TargetID: alvo, StartedAt: agora.Add(-3 * time.Minute), Cause: "timeout"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *bancada) enfileirar(t *testing.T, evento string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: b.incidente, ChannelID: b.canalID, Event: evento, Now: b.clk.Now(),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type linha struct {
	Status   string
	Attempts int32
	Proxima  time.Time
	Erro     *string
	Enviada  *time.Time
}

// estado lê a notificação sob o escopo do dono (a RLS delega ao canal).
func (b *bancada) estado(t *testing.T, id uuid.UUID) linha {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	var l linha
	err = tx.QueryRow(ctx, `SELECT status, attempts, next_attempt_at, last_error, sent_at FROM notifications WHERE id = $1`, id).
		Scan(&l.Status, &l.Attempts, &l.Proxima, &l.Erro, &l.Enviada)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (b *bancada) auditorias(t *testing.T) int {
	t.Helper()
	var n int
	err := b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'notification.failed' AND entity = 'notif_channel' AND entity_id = $1 AND user_id = $2`,
		b.canalID, b.dono).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (b *bancada) despachar(t *testing.T, quer int) {
	t.Helper()
	n, err := b.d.Dispatch(context.Background())
	if err != nil || n != quer {
		t.Fatalf("Dispatch = %d, %v; queria %d", n, err, quer)
	}
}

func TestDispatchSendsAndMarksSent(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)

	if b.canal.quantos() != 1 {
		t.Fatalf("%d envios, queria 1", b.canal.quantos())
	}
	ev, dest := b.canal.envios[0], b.canal.destinos[0]
	if ev.NotificationID != id || ev.Kind != incident.EventOpened || ev.TargetName != "API" || ev.Cause != "timeout" ||
		!ev.StartedAt.Equal(b.clk.Now().Add(-3*time.Minute)) || !ev.ResolvedAt.IsZero() {
		t.Errorf("evento = %+v", ev)
	}
	if dest != webhookDeTeste {
		t.Errorf("destino decifrado = %q", dest)
	}
	if l := b.estado(t, id); l.Status != "sent" || l.Attempts != 1 || l.Enviada == nil || l.Erro != nil {
		t.Errorf("linha = %+v", l)
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "sent")); v != 1 {
		t.Errorf("notify_total{discord,sent} = %v", v)
	}
	b.despachar(t, 0) // entregue não volta
}

func TestRetryableFailureBacksOffExponentially(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonServerError}, &SendError{Reason: ReasonServerError}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	l := b.estado(t, id)
	if l.Status != "pending" || l.Attempts != 1 || !l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) ||
		l.Erro == nil || *l.Erro != "server_error" {
		t.Fatalf("depois da 1ª falha: %+v; queria pendente, próxima em 30s, server_error", l)
	}

	b.clk.Advance(29 * time.Second)
	b.despachar(t, 0)
	b.clk.Advance(time.Second)
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Attempts != 2 || !l.Proxima.Equal(b.clk.Now().Add(time.Minute)) {
		t.Errorf("depois da 2ª falha: %+v; queria próxima em 1min", l)
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "retry")); v != 2 {
		t.Errorf("notify_total{discord,retry} = %v", v)
	}
}

// O provedor pode pedir mais tempo que o backoff (429 com Retry-After).
func TestRetryAfterWinsWhenLonger(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited, RetryAfter: 5 * time.Minute}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); !l.Proxima.Equal(b.clk.Now().Add(5 * time.Minute)) {
		t.Errorf("próxima = %v, queria em 5min (Retry-After > 30s de backoff)", l.Proxima)
	}
}

// A sexta falha transitória é definitiva, e vai para a auditoria.
func TestLastAttemptFailsForGoodAndIsAudited(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	falha := &SendError{Reason: ReasonNetwork}
	for i := range MaxAttempts {
		b.canal.respostas = []error{falha}
		b.despachar(t, 1)
		if i < MaxAttempts-1 {
			if b.auditorias(t) != 0 {
				t.Fatalf("tentativa %d já auditou", i+1)
			}
			b.clk.Advance(maxBackoff)
		}
	}
	if l := b.estado(t, id); l.Status != "failed" || l.Attempts != MaxAttempts || *l.Erro != "network" {
		t.Errorf("linha = %+v; queria failed na tentativa %d", l, MaxAttempts)
	}
	if n := b.auditorias(t); n != 1 {
		t.Errorf("%d entradas de auditoria, queria 1", n)
	}
	b.clk.Advance(maxBackoff)
	b.despachar(t, 0)
}

// A sexta tentativa cujo desfecho não chegou a ser gravado (processo morto,
// commit que falhou) volta à fila quando o lease vence, já com attempts no
// teto. A reivindicação a leva além de MaxAttempts, e ela falha de vez sem
// enviar: o teto de tentativas é teto de ENVIOS.
func TestNoSendPastTheLastAttempt(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if tag, err := tx.Exec(ctx, `UPDATE notifications SET attempts = $2 WHERE id = $1`, id, MaxAttempts); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("UPDATE attempts: %v, %d linhas", err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	b.despachar(t, 1)
	if n := b.canal.quantos(); n != 0 {
		t.Errorf("enviou %d vezes além da última tentativa", n)
	}
	if l := b.estado(t, id); l.Status != "failed" || l.Erro == nil || *l.Erro != "unknown" {
		t.Errorf("linha = %+v; queria failed com unknown", l)
	}
	if n := b.auditorias(t); n != 1 {
		t.Errorf("%d entradas de auditoria, queria 1", n)
	}
	// A falha foi do StillOn (o desfecho não chegou a ser gravado), não do
	// canal: ele não vira failing (achado da revisão).
	if c := b.canalVisto(t); c.Status != StatusVerified {
		t.Errorf("canal = %+v; queria verified, sem culpa pela falha do próprio StillOn", c)
	}
}

func TestPermanentFailureIsFinalAtOnce(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonRejected, Permanent: true}}
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "failed" || l.Attempts != 1 || *l.Erro != "rejected" {
		t.Errorf("linha = %+v", l)
	}
	if b.auditorias(t) != 1 {
		t.Error("falha definitiva não foi auditada")
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "failed")); v != 1 {
		t.Errorf("notify_total{discord,failed} = %v", v)
	}
}

// Spec §5.8: a verificação vale no envio, e não só no fan-out. Um canal que
// não está verificado na hora do envio não recebe nada.
func TestUnverifiedChannelIsCancelledWithoutSending(t *testing.T) {
	b := novaBancada(t, func(p *db.InsertChannelParams) { p.VerifiedAt = nil })
	id := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if b.canal.quantos() != 0 {
		t.Error("enviou para um canal não verificado")
	}
	if l := b.estado(t, id); l.Status != "cancelled" {
		t.Errorf("linha = %+v, queria cancelled", l)
	}
}

func TestDisabledChannelIsCancelledWithoutSending(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notif_channels SET enabled = false WHERE id = $1`, b.canalID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	b.despachar(t, 1)
	if b.canal.quantos() != 0 || b.estado(t, id).Status != "cancelled" {
		t.Errorf("canal desabilitado: %d envios, status %s", b.canal.quantos(), b.estado(t, id).Status)
	}
}

// A abertura que não saiu antes de o incidente fechar fica velha. O
// fechamento, que diz quando caiu e quando voltou, sai normalmente.
func TestStaleOpenedIsCancelledOnceTheIncidentResolved(t *testing.T) {
	b := novaBancada(t, nil)
	aberta := b.enfileirar(t, incident.EventOpened)
	ctx := context.Background()
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		_, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: b.incidente, ResolvedAt: b.clk.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fechada := b.enfileirar(t, incident.EventResolved)

	b.despachar(t, 2)
	if l := b.estado(t, aberta); l.Status != "cancelled" {
		t.Errorf("abertura velha: %+v, queria cancelled", l)
	}
	if l := b.estado(t, fechada); l.Status != "sent" {
		t.Errorf("fechamento: %+v, queria sent", l)
	}
	if b.canal.quantos() != 1 || b.canal.envios[0].Kind != incident.EventResolved || b.canal.envios[0].ResolvedAt.IsZero() {
		t.Errorf("envios = %+v; queria só o fechamento, com ResolvedAt", b.canal.envios)
	}
}

// Canal cujo Notifier o operador não configurou (e-mail sem Resend) falha
// de vez, em vez de ficar pendente para sempre.
func TestChannelWithoutNotifierFailsForGood(t *testing.T) {
	b := novaBancada(t, nil)
	b.d = NewDispatcher(b.pool, b.box, b.clk, prometheus.NewRegistry()) // nenhum canal
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "failed" || *l.Erro != "invalid_channel" {
		t.Errorf("linha = %+v", l)
	}
	if b.auditorias(t) != 1 {
		t.Error("falha definitiva não foi auditada")
	}
}

// Um webhook que não responde não segura o lote além do prazo de envio.
func TestSendIsBoundedByTheTimeout(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.bloquear = true
	b.d.sendTimeout = 50 * time.Millisecond
	id := b.enfileirar(t, incident.EventOpened)

	inicio := time.Now()
	b.despachar(t, 1)
	if d := time.Since(inicio); d > 5*time.Second {
		t.Errorf("o lote levou %v com um envio travado", d)
	}
	if l := b.estado(t, id); l.Status != "pending" || l.Erro == nil || *l.Erro != "timeout" {
		t.Errorf("linha = %+v; queria retentativa por timeout", l)
	}
}

type logSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSeguro) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSeguro) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestPanickingNotifierDoesNotKillTheDispatcher(t *testing.T) {
	log := &logSeguro{}
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
	t.Cleanup(func() { slog.SetDefault(antigo) })

	b := novaBancada(t, nil)
	b.canal.panico = true
	id := b.enfileirar(t, incident.EventOpened)

	b.despachar(t, 1)
	if !strings.Contains(log.String(), "panic no envio de notificação") {
		t.Errorf("o panic não foi para o log:\n%s", log)
	}
	// Fica como a reivindicação deixou: volta quando o lease vencer.
	if l := b.estado(t, id); l.Status != "pending" || l.Attempts != 1 {
		t.Errorf("linha = %+v", l)
	}
	if strings.Contains(log.String(), webhookDeTeste) {
		t.Error("o destino apareceu no log")
	}
}

func TestBackoff(t *testing.T) {
	for tentativa, quer := range map[int32]time.Duration{
		1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 5: 8 * time.Minute, 20: maxBackoff,
	} {
		if got := backoff(tentativa); got != quer {
			t.Errorf("backoff(%d) = %v, queria %v", tentativa, got, quer)
		}
	}
}

// Um erro que não é *SendError (um Notifier novo, mal escrito) é tratado
// como transitório, e não derruba a classificação.
func TestPlainErrorIsRetried(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{errors.New("qualquer coisa")}
	id := b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "pending" || *l.Erro != "unknown" {
		t.Errorf("linha = %+v", l)
	}
}

// canalVisto é o canal como o dono o vê em GET /channels.
func (b *bancada) canalVisto(t *testing.T) Channel {
	t.Helper()
	svc := NewChannelService(b.pool, b.box, b.clk, nil, nil, "https://stillon.example")
	cs, err := svc.List(context.Background(), b.dono)
	if err != nil || len(cs) != 1 {
		t.Fatalf("List = %v, %v", cs, err)
	}
	return cs[0]
}

// Pendência do Plano 3: o canal que quebra depois de verificado não falha
// mais em silêncio. A falha definitiva o põe em failing, com o motivo; a
// próxima entrega que dá certo o tira, sem ação do dono.
func TestDefinitiveFailureMarksTheChannelFailingUntilASuccess(t *testing.T) {
	b := novaBancada(t, nil)
	if c := b.canalVisto(t); c.Status != StatusVerified || !c.LastFailureAt.IsZero() {
		t.Fatalf("antes de qualquer envio: %+v", c)
	}

	b.canal.respostas = []error{&SendError{Reason: ReasonRejected, Permanent: true}}
	b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	falhou := b.clk.Now()
	c := b.canalVisto(t)
	if c.Status != StatusFailing || c.LastError != ReasonRejected || !c.LastFailureAt.Equal(falhou) {
		t.Fatalf("depois da falha definitiva: %+v; queria failing, rejected, %v", c, falhou)
	}

	b.clk.Advance(time.Minute)
	b.enfileirar(t, incident.EventResolved)
	b.despachar(t, 1)
	c = b.canalVisto(t)
	if c.Status != StatusVerified || c.LastError != ReasonRejected || !c.LastFailureAt.Equal(falhou) {
		t.Errorf("depois de uma entrega: %+v; queria verified, com a última falha ainda visível", c)
	}
}

// Retentativa e cancelamento não mexem na saúde do canal: a primeira ainda
// pode dar certo, e o segundo nem tentou enviar.
func TestRetryAndCancelLeaveTheChannelHealthAlone(t *testing.T) {
	b := novaBancada(t, nil)
	b.canal.respostas = []error{&SendError{Reason: ReasonServerError}}
	b.enfileirar(t, incident.EventOpened)
	b.despachar(t, 1)
	if c := b.canalVisto(t); c.Status != StatusVerified || !c.LastFailureAt.IsZero() {
		t.Errorf("retentativa mexeu no canal: %+v", c)
	}
	// Conta sob o escopo prober: sem escopo, a RLS esconderia qualquer
	// linha e o 0 seria verdade por acidente.
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_health`).Scan(&n); err != nil || n != 0 {
		t.Errorf("channel_health tem %d linhas depois de uma retentativa (%v), queria 0", n, err)
	}
}

// apagao simula muitos alvos do mesmo dono caindo juntos: n alvos, cada um
// com um incidente aberto e a abertura enfileirada para o mesmo canal.
// Devolve os ids na ordem de enfileiramento.
func (b *bancada) apagao(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		alvo, incidente, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		agora := b.clk.Now()
		err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
			return q.InsertTarget(ctx, db.InsertTargetParams{
				ID: alvo, UserID: b.dono, Name: fmt.Sprintf("alvo-%d", i), URL: "https://exemplo.com", Method: "GET",
				IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		err = storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
			if err := q.OpenIncident(ctx, db.OpenIncidentParams{ID: incidente, TargetID: alvo, StartedAt: agora, Cause: "timeout"}); err != nil {
				return err
			}
			return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
				ID: id, IncidentID: incidente, ChannelID: b.canalID, Event: incident.EventOpened, Now: agora,
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// Apagão em massa (pendência do Plano 3): um 429 é o provedor pedindo
// espera, e não gasta tentativa. Dez 429 seguidos, mais que MaxAttempts,
// e a notificação ainda sai.
func TestRateLimitDoesNotSpendAttempts(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	for i := range 10 {
		b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited, RetryAfter: 2 * time.Second}}
		b.despachar(t, 1)
		l := b.estado(t, id)
		if l.Status != "pending" || l.Attempts != 0 || !l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) {
			t.Fatalf("depois do 429 nº %d: %+v; queria pendente, 0 tentativas, próxima pelo backoff", i+1, l)
		}
		b.clk.Advance(30 * time.Second)
	}
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "sent" || l.Attempts != 1 {
		t.Errorf("linha = %+v; queria sent na 11ª tentativa, com 1 tentativa contada", l)
	}
}

// O perdão tem teto: uma notificação com mais de rateLimitGrace de idade já
// perdeu a maior parte do valor, e um webhook que responde 429 para sempre
// não pode segurá-la na fila para sempre.
func TestRateLimitCountsOnceTheNotificationIsOld(t *testing.T) {
	b := novaBancada(t, nil)
	id := b.enfileirar(t, incident.EventOpened)
	b.clk.Advance(rateLimitGrace)
	b.canal.respostas = []error{&SendError{Reason: ReasonRateLimited}}
	b.despachar(t, 1)
	if l := b.estado(t, id); l.Status != "pending" || l.Attempts != 1 {
		t.Errorf("429 depois do teto: %+v; queria a tentativa contada", l)
	}
}

// Dentro do lote, o mesmo canal recebe em série, na ordem em que as
// notificações foram enfileiradas. Um 429 adia o resto do canal no lote,
// sem enviar, para quando o provedor pediu; nada gasta tentativa.
func TestSameChannelSendsInOrderAndHoldsAfterA429(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 5)
	b.canal.respostas = []error{nil, nil, &SendError{Reason: ReasonRateLimited, RetryAfter: 3 * time.Minute}}

	b.despachar(t, 5)
	if n := b.canal.quantos(); n != 3 {
		t.Fatalf("%d envios, queria 3 (dois entregues e o do 429; o resto espera)", n)
	}
	for i, ev := range b.canal.envios {
		if ev.NotificationID != ids[i] {
			t.Errorf("envio %d foi %s, queria %s (ordem de enfileiramento)", i, ev.NotificationID, ids[i])
		}
	}
	for i, id := range ids {
		l := b.estado(t, id)
		switch {
		case i < 2 && l.Status != "sent":
			t.Errorf("notificação %d: %+v, queria sent", i, l)
		case i >= 2 && (l.Status != "pending" || l.Attempts != 0 || !l.Proxima.Equal(b.clk.Now().Add(3*time.Minute)) ||
			l.Erro == nil || *l.Erro != "rate_limited"):
			t.Errorf("notificação %d: %+v; queria pendente para daqui a 3min, sem tentativa gasta", i, l)
		}
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "deferred")); v != 2 {
		t.Errorf("notify_total{discord,deferred} = %v, queria 2", v)
	}

	b.clk.Advance(3 * time.Minute)
	b.despachar(t, 3)
	for i, id := range ids {
		if l := b.estado(t, id); l.Status != "sent" {
			t.Errorf("depois da espera, notificação %d: %+v", i, l)
		}
	}
}

// Canais diferentes não esperam um pelo outro: um webhook travado não
// atrasa o canal vizinho além do prazo de envio.
func TestDifferentChannelsSendInParallel(t *testing.T) {
	b := novaBancada(t, nil)
	// 1s por envio: a folga de 1s entre "em paralelo" (~1s) e "em série"
	// (>= 2s) cobre o banco numa CI lenta com -race.
	b.d.sendTimeout = time.Second
	b.canal.bloquear = true
	b.apagao(t, 1)
	// Um segundo canal, verificado, do mesmo dono, com a abertura do
	// incidente da bancada.
	ctx := context.Background()
	outro := uuid.Must(uuid.NewV7())
	sealed, err := sealConfig(b.box, outro, webhookDeTeste)
	if err != nil {
		t.Fatal(err)
	}
	agora := b.clk.Now()
	err = storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: outro, UserID: b.dono, Kind: string(KindDiscord), ConfigEnc: sealed, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: uuid.Must(uuid.NewV7()), IncidentID: b.incidente, ChannelID: outro, Event: incident.EventOpened, Now: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	inicio := time.Now()
	b.despachar(t, 2)
	// Em série, dois envios travados levariam pelo menos 2 × 1s.
	if d := time.Since(inicio); d >= 2*time.Second {
		t.Errorf("dois canais travados levaram %v; em paralelo, ~1s", d)
	}
}

// tentativas põe attempts em todas as notificações do dono.
func (b *bancada) tentativas(t *testing.T, n int32) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, b.dono.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET attempts = $1`, n); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// Um webhook que trava não segura o lote por k × sendTimeout (achado da
// revisão): depois de uma falha transitória, o resto da fila do canal é
// adiado sem envio. Fora do 429, a tentativa conta, como contaria se cada
// uma tivesse sido enviada e falhado igual.
func TestTransientFailureHoldsTheRestOfTheChannel(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 4)
	b.canal.respostas = []error{&SendError{Reason: ReasonTimeout}}

	b.despachar(t, 4)
	if n := b.canal.quantos(); n != 1 {
		t.Fatalf("%d envios, queria 1 (o resto do canal espera)", n)
	}
	for i, id := range ids {
		l := b.estado(t, id)
		if l.Status != "pending" || l.Attempts != 1 || l.Erro == nil || *l.Erro != "timeout" ||
			!l.Proxima.Equal(b.clk.Now().Add(30*time.Second)) {
			t.Errorf("notificação %d: %+v; queria pendente, 1 tentativa, timeout, próxima em 30s", i, l)
		}
	}
	if v := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "deferred")); v != 3 {
		t.Errorf("notify_total{discord,deferred} = %v, queria 3", v)
	}
}

// As adiadas que já estão no teto falham de vez, com o motivo de quem parou
// o canal, sem esperar outro sendTimeout cada uma.
func TestDeferredAtTheLastAttemptFailWithoutSending(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 3)
	b.tentativas(t, MaxAttempts-1)
	b.canal.respostas = []error{&SendError{Reason: ReasonNetwork}}

	b.despachar(t, 3)
	if n := b.canal.quantos(); n != 1 {
		t.Fatalf("%d envios, queria 1", n)
	}
	for i, id := range ids {
		if l := b.estado(t, id); l.Status != "failed" || l.Erro == nil || *l.Erro != "network" {
			t.Errorf("notificação %d: %+v; queria failed por network", i, l)
		}
	}
	if c := b.canalVisto(t); c.Status != StatusFailing {
		t.Errorf("canal = %+v; queria failing", c)
	}
	// As adiadas que falharam contam como failed, e não como deferred.
	if f, df := testutil.ToFloat64(b.d.total.WithLabelValues("discord", "failed")),
		testutil.ToFloat64(b.d.total.WithLabelValues("discord", "deferred")); f != 3 || df != 0 {
		t.Errorf("notify_total failed=%v deferred=%v; queria 3 e 0", f, df)
	}
}

// Adiar não pula os cancelamentos de tentar (achado da segunda revisão): a
// abertura cujo incidente já fechou é cancelada, e não adiada; e a linha que
// voltou do lease além do teto falha sem culpar o canal.
func TestDeferralKeepsThePreSendDecisions(t *testing.T) {
	b := novaBancada(t, nil)
	ids := b.apagao(t, 3)
	b.comoSondador(t, `UPDATE incidents SET resolved_at = $2
		WHERE id = (SELECT incident_id FROM notifications WHERE id = $1)`, ids[1], b.clk.Now())
	// A primeira falha transitória e para o canal; a terceira volta do lease
	// além do teto (a reivindicação a leva a MaxAttempts+1).
	b.comoSondador(t, `UPDATE notifications SET attempts = $2 WHERE id = $1`, ids[2], MaxAttempts)
	b.canal.respostas = []error{&SendError{Reason: ReasonTimeout}}

	b.despachar(t, 3)
	if n := b.canal.quantos(); n != 1 {
		t.Fatalf("%d envios, queria 1", n)
	}
	if l := b.estado(t, ids[1]); l.Status != "cancelled" {
		t.Errorf("abertura velha adiada: %+v; queria cancelled", l)
	}
	if l := b.estado(t, ids[2]); l.Status != "failed" || l.Erro == nil || *l.Erro != "unknown" {
		t.Errorf("linha além do teto: %+v; queria failed por unknown", l)
	}
	if c := b.canalVisto(t); c.Status != StatusVerified {
		t.Errorf("canal = %+v; queria verified: a falha por lease vencido é do StillOn", c)
	}
}

// comoSondador roda um comando no escopo prober.
func (b *bancada) comoSondador(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if tag, err := tx.Exec(ctx, sql, args...); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("%s: %v, %d linhas", sql, err, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
