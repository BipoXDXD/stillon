package prober

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/incident"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/targets"
)

// MaxInFlight é o teto global de sondas simultâneas (spec §6.2). Sem ele,
// 500 alvos lentos seriam 500 goroutines presas em I/O de rede.
const MaxInFlight int32 = 64

const tickEvery = time.Second

// tickTimeout é o prazo de um Tick inteiro (reivindicar e medir a fila),
// espera por conexão do pool incluída. Fica abaixo de tickEvery: uma
// consulta presa não segura o despacho de todos os alvos, e o Tick seguinte
// tenta de novo (C2 da revisão).
const tickTimeout = 900 * time.Millisecond

// reconcileEvery é o intervalo da reconciliação das métricas. Uma
// transição de incidente também dispara uma, sem esperar o intervalo.
const reconcileEvery = 30 * time.Second

// reconcileTimeout é o prazo de uma reconciliação. Ela divide o laço de Run
// com o Tick, então o prazo é curto: presa, ela custa no máximo alguns
// despachos, em vez de parar o sondador.
const reconcileTimeout = 5 * time.Second

// PersistTimeout é o prazo próprio de cada gravação de check. Uma sonda que
// termina no limite do grace de Stop ainda grava com este prazo, então Stop
// pode levar até grace + PersistTimeout. Exportado para que o wiring
// desconte esse tempo do orçamento de shutdown.
const PersistTimeout = 5 * time.Second

var errShutdown = errors.New("encerramento do sondador")

type Scheduler struct {
	pool     *pgxpool.Pool
	box      *crypto.Box
	clk      clock.Clock
	metrics  *Metrics
	client   *http.Client
	sem      *semaphore.Weighted
	max      int32
	detector incident.Detector

	// Prazos das rodadas; campos para que os testes os encurtem.
	tickTimeout      time.Duration
	reconcileTimeout time.Duration

	// transicao avisa o laço de Run que um incidente abriu ou fechou, para
	// que incidents_open não espere a próxima reconciliação periódica.
	// Buffer de 1: avisos que chegam com um já pendente se fundem nele.
	transicao chan struct{}

	emVoo sync.WaitGroup
	// sondas é o contexto de TODAS as sondas, separado do ctx de Run. O
	// SIGTERM encerra o despacho na hora; as sondas em voo só são abortadas
	// quando o prazo de Stop acaba.
	sondas  context.Context
	abortar context.CancelCauseFunc
}

// New monta o agendador com o cliente de produção, guardado pelo safedial.
// Não há como passar outro cliente por aqui: TestNewUsesTheSSRFGuardedClient
// trava isso.
func New(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, m *Metrics) *Scheduler {
	return newScheduler(pool, box, clk, m, safedial.NewClient(0), MaxInFlight)
}

func newScheduler(pool *pgxpool.Pool, box *crypto.Box, clk clock.Clock, m *Metrics, client *http.Client, max int32) *Scheduler {
	sondas, abortar := context.WithCancelCause(context.Background())
	return &Scheduler{
		pool: pool, box: box, clk: clk, metrics: m, client: client,
		sem: semaphore.NewWeighted(int64(max)), max: max,
		detector: incident.Default, transicao: make(chan struct{}, 1),
		sondas: sondas, abortar: abortar,
		tickTimeout: tickTimeout, reconcileTimeout: reconcileTimeout,
	}
}

// Run despacha a cada segundo até ctx acabar, e então chama Stop(grace).
// Reconcilia as métricas ao subir, a cada reconcileEvery e a cada
// transição de incidente.
func (s *Scheduler) Run(ctx context.Context, grace time.Duration) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	r := time.NewTicker(reconcileEvery)
	defer r.Stop()
	s.reconciliar(ctx)
	for {
		select {
		case <-ctx.Done():
			s.Stop(grace)
			return nil
		case <-t.C:
			if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar sondas", "erro", err)
			}
		case <-r.C:
			s.reconciliar(ctx)
		case <-s.transicao:
			s.reconciliar(ctx)
		}
	}
}

func (s *Scheduler) reconciliar(ctx context.Context) {
	if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
		slog.Error("reconciliar métricas", "erro", err)
	}
}

// Reconcile apaga as séries de latência de alvos que não existem mais e
// atualiza incidents_open (D9/M1 do Plano 2).
//
// forget só roda sozinho quando o alvo some com uma sonda em voo. Apagar um
// alvo parado não passa pelo sondador, e com --role=api e --role=prober
// separados nem poderia: sem esta reconciliação, a série do alvo apagado
// vivia até o restart.
func (s *Scheduler) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.reconcileTimeout)
	defer cancel()
	vistos := s.metrics.alvosVistos()
	var existentes []uuid.UUID
	var abertos int64
	err := storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		var err error
		if len(vistos) > 0 {
			if existentes, err = q.ExistingTargetIDs(ctx, vistos); err != nil {
				return err
			}
		}
		abertos, err = q.CountOpenIncidents(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("reconciliar métricas: %w", err)
	}

	vivos := make(map[uuid.UUID]struct{}, len(existentes))
	for _, id := range existentes {
		vivos[id] = struct{}{}
	}
	for _, id := range vistos {
		if _, ok := vivos[id]; !ok {
			s.metrics.forget(id)
		}
	}
	s.metrics.incidentsOpen.Set(float64(abertos))
	return nil
}

// Tick reivindica até "vagas livres" alvos vencidos e dispara uma goroutine
// por alvo. Devolve quantos disparou. O Tick inteiro tem o prazo tickTimeout;
// as sondas disparadas não herdam esse prazo.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, s.tickTimeout)
	defer cancel()
	now := s.clk.Now()

	// As vagas vêm ANTES da reivindicação. Reivindicar grava o próximo
	// next_run_at; um alvo reivindicado sem vaga ficaria um intervalo
	// inteiro sem ser sondado.
	var vagas int32
	for vagas < s.max && s.sem.TryAcquire(1) {
		vagas++
	}

	var alvos []db.ClaimDueTargetsRow
	if vagas > 0 {
		err := storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
			var err error
			alvos, err = q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: now, MaxRows: vagas})
			return err
		})
		if err != nil {
			// Todas as vagas voltam, e não só as que sobraram: um commit que
			// falha chega aqui com alvos já lidos, e nenhum deles foi
			// disparado.
			s.sem.Release(int64(vagas))
			return 0, fmt.Errorf("reivindicar alvos: %w", err)
		}
		// Guarda do semáforo. O LIMIT da reivindicação vive numa subquery
		// (IN (… LIMIT … FOR UPDATE SKIP LOCKED)), e o Postgres não promete
		// avaliá-la uma vez só: num nested loop ela pode ser reexecutada e
		// devolver mais linhas que vagas. Sem este corte, a sobra ficaria
		// negativa, nada voltaria ao semáforo e cada goroutine faria
		// Release(1) de uma vaga que nunca foi tomada: o teto de 64 seria
		// furado e viria o panic "semaphore: released more than held", que
		// derruba o processo (em --role=all, a API junto). Os excedentes já
		// tiveram o next_run_at empurrado pela reivindicação, então só perdem
		// um intervalo. Não há teste comportamental: o Postgres não produz
		// esse plano sob demanda.
		if len(alvos) > int(vagas) {
			slog.Error("reivindicação devolveu mais alvos que vagas; os excedentes ficam para o próximo intervalo",
				"vagas", vagas, "alvos", len(alvos))
			alvos = alvos[:vagas]
		}
		// A conta é em int: converter len() para int32 seria o G115 do gosec.
		if sobra := int(vagas) - len(alvos); sobra > 0 {
			s.sem.Release(int64(sobra))
		}
	}

	for _, a := range alvos {
		s.emVoo.Add(1)
		s.metrics.inflight.Inc()
		go func() {
			defer s.emVoo.Done()
			defer s.sem.Release(1)
			defer s.metrics.inflight.Dec()
			// Registrado por último, roda primeiro: segura o panic de uma
			// sonda, e os três defers acima rodam depois dele como numa
			// saída normal, devolvendo a vaga e o inflight. Sem isto, um
			// panic numa sonda derruba o processo inteiro. O log leva só o
			// valor do panic e o alvo, nunca a requisição.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic na sonda", "target_id", a.ID, "panic", fmt.Sprint(r))
				}
			}()
			s.sondar(a, now)
		}()
	}

	if err := s.medirFila(ctx, now); err != nil {
		return len(alvos), fmt.Errorf("medir fila: %w", err)
	}
	return len(alvos), nil
}

// Stop espera as sondas em voo por até grace. Depois disso, aborta as que
// sobraram e espera que terminem, o que é rápido, porque o http.Client
// respeita o cancelamento. Sonda abortada não é gravada (D12).
//
// Não chame Tick durante nem depois de Stop: Run para de despachar antes de
// chamá-lo.
func (s *Scheduler) Stop(grace time.Duration) {
	terminaram := make(chan struct{})
	go func() {
		s.emVoo.Wait()
		close(terminaram)
	}()
	select {
	case <-terminaram:
	case <-time.After(grace):
		s.abortar(errShutdown)
		<-terminaram
	}
}

func (s *Scheduler) sondar(a db.ClaimDueTargetsRow, checkedAt time.Time) {
	spec, err := s.especificar(a)
	if err != nil {
		// Headers que não abrem são defeito NOSSO (chave trocada, linha
		// adulterada), não queda do alvo. Gravar down acusaria o alvo.
		slog.Error("preparar sonda", "target_id", a.ID, "erro", err)
		return
	}

	res := Probe(s.sondas, s.client, spec)

	// Review Focus 4. O shutdown se reconhece pela CAUSA do contexto, nunca
	// pelo erro da sonda: com WithCancelCause, o erro devolvido é a própria
	// causa, e errors.Is(err, context.Canceled) é false.
	if errors.Is(context.Cause(s.sondas), errShutdown) {
		return
	}
	s.metrics.observe(a.ID, res)
	s.gravar(a, checkedAt, res)
}

func (s *Scheduler) especificar(a db.ClaimDueTargetsRow) (Spec, error) {
	headers, err := targets.OpenHeaders(s.box, a.ID, a.HeadersEnc)
	if err != nil {
		return Spec{}, err
	}
	spec := Spec{
		URL: a.URL, Method: a.Method,
		Timeout:        time.Duration(a.TimeoutMS) * time.Millisecond,
		ExpectedStatus: int(a.ExpectedStatus),
		Headers:        headers,
	}
	if a.DegradedThresholdMS != nil {
		spec.DegradedThreshold = time.Duration(*a.DegradedThresholdMS) * time.Millisecond
	}
	return spec, nil
}

// gravar persiste o check e avalia a histerese na MESMA transação: ou o
// check e o estado do alvo ficam gravados juntos, ou nenhum dos dois.
func (s *Scheduler) gravar(a db.ClaimDueTargetsRow, em time.Time, res Result) {
	// Contexto próprio: a sonda terminou, e o resultado vale mesmo com o
	// processo encerrando.
	ctx, cancel := context.WithTimeout(context.Background(), PersistTimeout)
	defer cancel()

	id, err := uuid.NewV7()
	if err != nil {
		slog.Error("gerar id de check", "erro", err)
		return
	}
	p := db.InsertCheckParams{
		ID: id, TargetID: a.ID, CheckedAt: em,
		Status: string(res.Status), LatencyMS: int32Limitado(res.Latency.Milliseconds()),
	}
	if res.StatusCode != 0 {
		code := int32Limitado(int64(res.StatusCode))
		p.StatusCode = &code
	}
	if res.ErrorKind != "" {
		kind := string(res.ErrorKind)
		p.ErrorKind = &kind
	}

	var acao incident.Action
	err = storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		if err := q.InsertCheck(ctx, p); err != nil {
			return err
		}
		var err error
		acao, err = s.avaliar(ctx, q, a, em, res)
		return err
	})
	switch {
	case err == nil:
		if acao != incident.ActionNone {
			select {
			case s.transicao <- struct{}{}:
			default:
			}
		}
	case alvoSumiu(err):
		// Review Focus 3 do Plano 2: o dono apagou o alvo com a sonda em
		// voo. Não é erro, é o dono tendo mudado de ideia.
		s.metrics.forget(a.ID)
	default:
		slog.Error("gravar check", "target_id", a.ID, "erro", err)
	}
}

// avaliar aplica o check à histerese do alvo (spec §7) e, numa transição,
// abre ou fecha o incidente e enfileira as notificações (spec §8).
//
// A linha de target_health é travada antes de ler o incidente aberto: duas
// sondas do mesmo alvo gravando ao mesmo tempo (M4 do Plano 2) passam aqui
// uma de cada vez, e a segunda vê o estado que a primeira gravou.
func (s *Scheduler) avaliar(ctx context.Context, q *db.Queries, a db.ClaimDueTargetsRow, em time.Time, res Result) (incident.Action, error) {
	if err := q.EnsureTargetHealth(ctx, a.ID); err != nil {
		return incident.ActionNone, fmt.Errorf("criar histerese do alvo: %w", err)
	}
	h, err := q.LockTargetHealth(ctx, a.ID)
	if err != nil {
		return incident.ActionNone, fmt.Errorf("travar histerese do alvo: %w", err)
	}
	aberto, err := q.GetOpenIncident(ctx, a.ID)
	temAberto := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return incident.ActionNone, fmt.Errorf("buscar incidente aberto: %w", err)
	}

	anterior := recomecarAposLacuna(incident.State{
		ConsecutiveFailures:  h.ConsecutiveFailures,
		ConsecutiveSuccesses: h.ConsecutiveSuccesses,
		StreakSince:          valorOuZero(h.StreakSince),
		LastCheckedAt:        valorOuZero(h.LastCheckedAt),
		Open:                 temAberto,
	}, em, a.IntervalSec)
	estado, acao := s.detector.Evaluate(anterior, paraDetector(em, res))

	// Depois de qualquer Evaluate os dois instantes estão preenchidos: um
	// check fora de ordem só é ignorado quando já houve um antes dele.
	desde := estado.StreakSince
	if err := q.SaveTargetHealth(ctx, db.SaveTargetHealthParams{
		TargetID:             a.ID,
		ConsecutiveFailures:  estado.ConsecutiveFailures,
		ConsecutiveSuccesses: estado.ConsecutiveSuccesses,
		StreakSince:          &desde,
		LastCheckedAt:        estado.LastCheckedAt,
	}); err != nil {
		return incident.ActionNone, fmt.Errorf("gravar histerese do alvo: %w", err)
	}

	switch acao {
	case incident.ActionOpen:
		id, err := uuid.NewV7()
		if err != nil {
			return acao, fmt.Errorf("gerar id de incidente: %w", err)
		}
		// Abrir só acontece num check down, que sempre tem error_kind.
		if err := q.OpenIncident(ctx, db.OpenIncidentParams{
			ID: id, TargetID: a.ID, StartedAt: estado.StreakSince, Cause: string(res.ErrorKind),
		}); err != nil {
			return acao, fmt.Errorf("abrir incidente: %w", err)
		}
		n, err := q.MarkOpenNotified(ctx, db.MarkOpenNotifiedParams{ID: id, Now: em})
		if err != nil {
			return acao, fmt.Errorf("marcar abertura notificada: %w", err)
		}
		if n == 1 {
			return acao, enfileirar(ctx, q, a.UserID, id, incident.EventOpened, em)
		}
	case incident.ActionClose:
		if _, err := q.ResolveIncident(ctx, db.ResolveIncidentParams{ID: aberto.ID, ResolvedAt: estado.StreakSince}); err != nil {
			return acao, fmt.Errorf("resolver incidente: %w", err)
		}
		n, err := q.MarkCloseNotified(ctx, db.MarkCloseNotifiedParams{ID: aberto.ID, Now: em})
		if err != nil {
			return acao, fmt.Errorf("marcar fechamento notificado: %w", err)
		}
		if n == 1 {
			return acao, enfileirar(ctx, q, a.UserID, aberto.ID, incident.EventResolved, em)
		}
	}
	return acao, nil
}

// fatorLacuna é quantos intervalos sem check bastam para a histerese
// recomeçar (ver recomecarAposLacuna).
const fatorLacuna = 3

// lacunaMaxima é o maior intervalo entre dois checks em que as sequências
// ainda se somam.
func lacunaMaxima(intervalSec int32) time.Duration {
	return fatorLacuna * time.Duration(intervalSec) * time.Second
}

// recomecarAposLacuna zera os dois contadores quando o check chega mais de
// fatorLacuna intervalos depois do último. Sem isso, um alvo com duas falhas
// que ficou pausado (ou cujo sondador ficou fora do ar) uma semana abriria
// incidente na primeira falha de volta, com started_at de uma semana atrás.
// Open fica como veio do banco (é a linha em incidents que manda), e
// LastCheckedAt também, para que a guarda de check fora de ordem do detector
// continue valendo. O detector segue puro: a regra depende do intervalo do
// alvo, que ele não conhece. Intervalo não positivo (a CHECK do banco não
// deixa) desliga a regra, em vez de zerar a cada check.
func recomecarAposLacuna(s incident.State, em time.Time, intervalSec int32) incident.State {
	if intervalSec <= 0 || s.LastCheckedAt.IsZero() || em.Sub(s.LastCheckedAt) <= lacunaMaxima(intervalSec) {
		return s
	}
	s.ConsecutiveFailures = 0
	s.ConsecutiveSuccesses = 0
	return s
}

// paraDetector traduz o resultado da sonda para o detector. Só down conta
// como falha; degraded conta como SUCESSO: zera a sequência de falhas e,
// somado a outros sucessos, fecha o incidente (spec §7.2).
func paraDetector(em time.Time, r Result) incident.ProbeResult {
	return incident.ProbeResult{Down: r.Status == StatusDown, At: em}
}

// enfileirar grava uma notificação por canal entregável do dono (spec §8).
// Os ids são UUIDv7 gerados aqui, como em toda a aplicação (spec §4.2); com
// no máximo notify.MaxChannels canais, um INSERT por canal é barato. O
// UNIQUE (incident, channel, event, seq), com seq 0 na abertura e no
// fechamento, torna a repetição inofensiva.
func enfileirar(ctx context.Context, q *db.Queries, dono, incidente uuid.UUID, evento string, em time.Time) error {
	canais, err := q.ListDeliverableChannels(ctx, dono)
	if err != nil {
		return fmt.Errorf("listar canais do dono: %w", err)
	}
	for _, canal := range canais {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("gerar id de notificação: %w", err)
		}
		if err := q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: evento, Now: em,
		}); err != nil {
			return fmt.Errorf("enfileirar notificação: %w", err)
		}
	}
	return nil
}

func valorOuZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (s *Scheduler) medirFila(ctx context.Context, now time.Time) error {
	return storage.InProberTx(ctx, s.pool, func(q *db.Queries) error {
		n, err := q.CountDueTargets(ctx, now)
		if err != nil {
			return err
		}
		s.metrics.queueDepth.Set(float64(n))
		return nil
	})
}

// alvoSumiu reconhece o INSERT de check para alvo apagado. O 42501 é a RLS
// de checks (o EXISTS no alvo falha) e chega ANTES da FK; o 23503 é a FK,
// mantida para o caso de a política um dia mudar
// (TestCheckForVanishedTargetFailsWithRLSCode).
func alvoSumiu(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "42501" || pgErr.Code == "23503")
}

// int32Limitado converte com teto explícito, que é o que o gosec (G115)
// exige. Latência e status code cabem com folga; o teto é garantia.
func int32Limitado(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < 0 {
		return 0
	}
	return int32(v)
}
