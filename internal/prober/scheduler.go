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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/targets"
)

// MaxInFlight é o teto global de sondas simultâneas (spec §6.2). Sem ele,
// 500 alvos lentos seriam 500 goroutines presas em I/O de rede.
const MaxInFlight int32 = 64

const tickEvery = time.Second

// PersistTimeout é o prazo próprio de cada gravação de check. Uma sonda que
// termina no limite do grace de Stop ainda grava com este prazo, então Stop
// pode levar até grace + PersistTimeout. Exportado para que o wiring
// desconte esse tempo do orçamento de shutdown.
const PersistTimeout = 5 * time.Second

var errShutdown = errors.New("encerramento do sondador")

type Scheduler struct {
	pool    *pgxpool.Pool
	box     *crypto.Box
	clk     clock.Clock
	metrics *Metrics
	client  *http.Client
	sem     *semaphore.Weighted
	max     int32

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
		sondas: sondas, abortar: abortar,
	}
}

// Run despacha a cada segundo até ctx acabar, e então chama Stop(grace).
func (s *Scheduler) Run(ctx context.Context, grace time.Duration) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop(grace)
			return nil
		case <-t.C:
			if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				slog.Error("despachar sondas", "erro", err)
			}
		}
	}
}

// Tick reivindica até "vagas livres" alvos vencidos e dispara uma goroutine
// por alvo. Devolve quantos disparou.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
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
	s.gravar(a.ID, checkedAt, res)
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

func (s *Scheduler) gravar(alvo uuid.UUID, em time.Time, res Result) {
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
		ID: id, TargetID: alvo, CheckedAt: em,
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

	err = storage.InProberTx(ctx, s.pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) })
	switch {
	case err == nil:
	case alvoSumiu(err):
		// Review Focus 3: o dono apagou o alvo com a sonda em voo. Não é
		// erro, é o dono tendo mudado de ideia.
		s.metrics.forget(alvo)
	default:
		slog.Error("gravar check", "target_id", alvo, "erro", err)
	}
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
