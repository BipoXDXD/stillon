package heartbeat_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

type ambiente struct {
	pool *pgxpool.Pool
	clk  *clock.Fake
	svc  *heartbeat.PingService
	dono uuid.UUID
}

func novoAmbiente(t *testing.T) ambiente {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(agora)
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return ambiente{pool: pool, clk: clk, svc: heartbeat.NewPingService(pool, clk), dono: dono}
}

func (a ambiente) novoHeartbeat(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: a.dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo,
			GraceSec: 300, Enabled: true, CreatedAt: agora, DeadlineAt: agora.Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func (a ambiente) linha(t *testing.T, id uuid.UUID) db.Heartbeat {
	t.Helper()
	ctx := context.Background()
	var h db.Heartbeat
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		var err error
		h, err = q.GetHeartbeat(ctx, db.GetHeartbeatParams{UserID: a.dono, ID: id})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (a ambiente) eventos(t *testing.T, id uuid.UUID) []db.HeartbeatEvent {
	t.Helper()
	ctx := context.Background()
	var ev []db.HeartbeatEvent
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		var err error
		ev, err = q.ListHeartbeatEvents(ctx, db.ListHeartbeatEventsParams{UserID: a.dono, HeartbeatID: id, MaxRows: 5000})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func (a ambiente) pingar(t *testing.T, token string, sig heartbeat.Signal) {
	t.Helper()
	if err := a.svc.Ping(context.Background(), token, sig, nil); err != nil {
		t.Fatalf("ping %s: %v", sig, err)
	}
}

// O sinal grava o estado e deixa uma linha no log, com o tempo de execução do
// início casado. Estado e evento vão na mesma transação.
func TestPingRecordsTheEvent(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)

	a.pingar(t, token, heartbeat.SignalStart)
	a.clk.Advance(90 * time.Second)
	a.pingar(t, token, heartbeat.SignalSuccess)

	h := a.linha(t, id)
	if want := agora.Add(90*time.Second + time.Hour + 5*time.Minute); !h.DeadlineAt.Equal(want) {
		t.Errorf("prazo %v, queria %v", h.DeadlineAt, want)
	}
	if h.LastStartAt != nil || h.LastSuccessAt == nil || *h.LastSignal != "success" {
		t.Errorf("linha depois do sucesso: %+v", h)
	}
	if h.LastRuntimeMS == nil || *h.LastRuntimeMS != 90000 {
		t.Errorf("última duração: %v", h.LastRuntimeMS)
	}
	ev := a.eventos(t, id)
	if len(ev) != 2 {
		t.Fatalf("%d eventos, queria 2", len(ev))
	}
	if ev[0].Kind != "success" || ev[0].RuntimeMS == nil || *ev[0].RuntimeMS != 90000 {
		t.Errorf("evento mais novo: %+v", ev[0])
	}
	if ev[1].Kind != "start" || ev[1].RuntimeMS != nil || !ev[1].At.Equal(agora) {
		t.Errorf("evento mais velho: %+v", ev[1])
	}
}

func TestFailSignalMarksTheHeartbeatFailing(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	a.pingar(t, token, heartbeat.SignalFail)
	if h := a.linha(t, id); h.FailingSince == nil || !h.FailingSince.Equal(agora) {
		t.Fatalf("failing_since %v, queria %v", h.FailingSince, agora)
	}
	a.clk.Advance(time.Minute)
	a.pingar(t, token, heartbeat.SignalSuccess)
	if h := a.linha(t, id); h.FailingSince != nil {
		t.Errorf("sucesso deixou failing_since em %v", h.FailingSince)
	}
}

func TestPingOfAnUnknownOrMalformedTokenIsTheSameError(t *testing.T) {
	a := novoAmbiente(t)
	desconhecido, _ := heartbeat.NewPingToken()
	for _, token := range []string{desconhecido, "", "curto"} {
		if err := a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, nil); !errors.Is(err, heartbeat.ErrUnknownToken) {
			t.Errorf("token %q deu %v", token, err)
		}
	}
}

func TestRepeatedPingHasOneEffect(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	a.pingar(t, token, heartbeat.SignalSuccess)
	antes := a.linha(t, id)

	a.clk.Advance(heartbeat.RepeatWindow - time.Second)
	a.pingar(t, token, heartbeat.SignalSuccess)
	if depois := a.linha(t, id); !depois.DeadlineAt.Equal(antes.DeadlineAt) {
		t.Errorf("a repetição moveu o prazo para %v", depois.DeadlineAt)
	}
	if n := len(a.eventos(t, id)); n != 1 {
		t.Errorf("%d eventos, queria 1", n)
	}

	a.clk.Advance(2 * time.Second)
	a.pingar(t, token, heartbeat.SignalSuccess)
	if n := len(a.eventos(t, id)); n != 2 {
		t.Errorf("fora da janela: %d eventos, queria 2", n)
	}
}

// A guarda da repetição vale sob corrida porque a decisão sai da linha
// travada: sem o FOR UPDATE, todos leem "sem ping" e gravam um evento cada.
func TestConcurrentPingsHaveOneEffect(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)

	const n = 8
	var wg sync.WaitGroup
	erros := make(chan error, n)
	largada := make(chan struct{})
	for range n {
		wg.Go(func() {
			<-largada
			erros <- a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, nil)
		})
	}
	close(largada)
	wg.Wait()
	close(erros)
	for err := range erros {
		if err != nil {
			t.Error(err)
		}
	}
	if ev := a.eventos(t, id); len(ev) != 1 {
		t.Errorf("%d eventos de %d pings simultâneos, queria 1", len(ev), n)
	}
}

// largarJuntos solta as funções numa largada comum e devolve os erros.
func largarJuntos(fns ...func() error) []error {
	var wg sync.WaitGroup
	erros := make(chan error, len(fns))
	largada := make(chan struct{})
	for _, fn := range fns {
		wg.Go(func() {
			<-largada
			erros <- fn()
		})
	}
	close(largada)
	wg.Wait()
	close(erros)
	var out []error
	for err := range erros {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

// O ping-now trava a linha como o ping: sem o FOR UPDATE, os cliques leem a
// linha de antes dos pings e cada um grava o seu evento.
func TestConcurrentPingNowAndPingHaveOneEffect(t *testing.T) {
	a := novoAmbiente(t)
	painel := heartbeat.NewService(a.pool, a.clk)
	id, token := a.novoHeartbeat(t)

	const n = 8
	var fns []func() error
	for range n {
		fns = append(fns,
			func() error {
				_, err := painel.PingNow(context.Background(), a.dono, id)
				return err
			},
			func() error { return a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, nil) },
		)
	}
	for _, err := range largarJuntos(fns...) {
		t.Error(err)
	}

	if ev := a.eventos(t, id); len(ev) != 1 || ev[0].Kind != "success" {
		t.Errorf("eventos %+v de %d sinais simultâneos, queria um success", ev, 2*n)
	}
	h := a.linha(t, id)
	if h.LastSignal == nil || *h.LastSignal != "success" || !mesmoInstante(h.LastSuccessAt, &agora) {
		t.Errorf("last_signal %s, last_success_at %s; queria success em %v", valor(h.LastSignal), valor(h.LastSuccessAt), agora)
	}
	if !h.DeadlineAt.Equal(agora.Add(65 * time.Minute)) {
		t.Errorf("prazo %v, queria %v", h.DeadlineAt, agora.Add(65*time.Minute))
	}
}

// Com um /fail simultâneo, a execução aberta fecha uma vez só e o estado final
// é o do último evento do log: a falha não some por baixo de um ping-now que
// leu a linha de antes dela.
func TestConcurrentPingNowAndFailKeepTheFailure(t *testing.T) {
	a := novoAmbiente(t)
	painel := heartbeat.NewService(a.pool, a.clk)
	id, token := a.novoHeartbeat(t)
	a.pingar(t, token, heartbeat.SignalStart)
	a.clk.Advance(time.Minute)

	const n = 8
	var fns []func() error
	for range n {
		fns = append(fns,
			func() error {
				_, err := painel.PingNow(context.Background(), a.dono, id)
				return err
			},
			func() error { return a.svc.Ping(context.Background(), token, heartbeat.SignalFail, nil) },
		)
	}
	for _, err := range largarJuntos(fns...) {
		t.Error(err)
	}

	ev := a.eventos(t, id)
	var medidos, falhas int
	for _, e := range ev {
		if e.RuntimeMS != nil {
			medidos++
			if *e.RuntimeMS != 60000 {
				t.Errorf("duração %d ms, queria 60000", *e.RuntimeMS)
			}
		}
		if e.Kind == "fail" {
			falhas++
		}
	}
	if medidos != 1 {
		t.Errorf("%d eventos fecharam a execução, queria 1: %+v", medidos, ev)
	}
	if falhas == 0 {
		t.Errorf("a falha sumiu do log: %+v", ev)
	}
	h := a.linha(t, id)
	ultimo := ev[0].Kind
	if h.LastSignal == nil || *h.LastSignal != ultimo {
		t.Errorf("last_signal %s, e o último evento é %s", valor(h.LastSignal), ultimo)
	}
	if falhando := h.FailingSince != nil; falhando != (ultimo == "fail") {
		t.Errorf("failing_since %s com o último evento %s", valor(h.FailingSince), ultimo)
	}
}

func valor[T any](p *T) string {
	if p == nil {
		return "NULL"
	}
	return fmt.Sprint(*p)
}

// O teto vale na transação do ping, e o que sai é o mais velho.
func TestEventsStayUnderTheCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	id, token := a.novoHeartbeat(t)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		for range heartbeat.MaxEventsPerHeartbeat {
			err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: uuid.Must(uuid.NewV7()), HeartbeatID: id, At: agora, Kind: "start", Source: "ping",
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	maisVelho := a.eventos(t, id)[heartbeat.MaxEventsPerHeartbeat-1].ID

	a.pingar(t, token, heartbeat.SignalSuccess)

	ev := a.eventos(t, id)
	if len(ev) != heartbeat.MaxEventsPerHeartbeat {
		t.Fatalf("%d eventos, queria o teto %d", len(ev), heartbeat.MaxEventsPerHeartbeat)
	}
	if ev[0].Kind != "success" {
		t.Errorf("o evento novo saiu: %+v", ev[0])
	}
	if ev[len(ev)-1].ID == maisVelho {
		t.Error("o evento mais velho ficou")
	}
}

// O teto é por heartbeat: aparar o que estourou não toca nos eventos do
// vizinho, mesmo os mais velhos que o corte.
func TestEventCapIsPerHeartbeat(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	vizinho, _ := a.novoHeartbeat(t)
	cheio, token := a.novoHeartbeat(t)
	inserir := func(q *db.Queries, id uuid.UUID) error {
		return q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
			ID: uuid.Must(uuid.NewV7()), HeartbeatID: id, At: agora, Kind: "start", Source: "ping",
		})
	}
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		if err := inserir(q, vizinho); err != nil {
			return err
		}
		for range heartbeat.MaxEventsPerHeartbeat {
			if err := inserir(q, cheio); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.pingar(t, token, heartbeat.SignalSuccess)
	if n := len(a.eventos(t, vizinho)); n != 1 {
		t.Errorf("o heartbeat vizinho ficou com %d eventos, queria 1", n)
	}
	if n := len(a.eventos(t, cheio)); n != heartbeat.MaxEventsPerHeartbeat {
		t.Errorf("%d eventos no heartbeat cheio", n)
	}
}

// D13: ping em heartbeat pausado é aceito. Recusar quebraria a tarefa do dono.
func TestPingOnAPausedHeartbeatIsRecorded(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	id, token := a.novoHeartbeat(t)
	desligado := false
	if _, err := heartbeat.NewService(a.pool, a.clk).Update(ctx, a.dono, id, heartbeat.Patch{Enabled: &desligado}); err != nil {
		t.Fatal(err)
	}
	a.pingar(t, token, heartbeat.SignalSuccess)
	if n := len(a.eventos(t, id)); n != 1 {
		t.Errorf("%d eventos, queria 1", n)
	}
}

func (a ambiente) pingarRun(t *testing.T, token string, sig heartbeat.Signal, rid *uuid.UUID) {
	t.Helper()
	if err := a.svc.Ping(context.Background(), token, sig, rid); err != nil {
		t.Fatalf("ping %s: %v", sig, err)
	}
}

func eventosDoTipo(ev []db.HeartbeatEvent, kind string) []db.HeartbeatEvent {
	var out []db.HeartbeatEvent
	for _, e := range ev {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// O log só registra: prazo, último sinal, último ping e rid da linha ficam como
// o sucesso de antes os deixou.
func TestLogKeepsDeadlineAndLastSignal(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	ridSucesso := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-3a4b5c6d7e8f")
	ridLog := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-000000000002")
	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridSucesso)

	a.clk.Advance(2 * time.Minute)
	a.pingarRun(t, token, heartbeat.SignalLog, &ridLog)

	h := a.linha(t, id)
	if want := agora.Add(time.Hour + 5*time.Minute); !h.DeadlineAt.Equal(want) {
		t.Errorf("prazo %v, queria %v", h.DeadlineAt, want)
	}
	if h.LastSignal == nil || *h.LastSignal != "success" {
		t.Errorf("last_signal %v, queria success", h.LastSignal)
	}
	if h.LastPingAt == nil || !h.LastPingAt.Equal(agora) {
		t.Errorf("last_ping_at %v, queria %v", h.LastPingAt, agora)
	}
	if h.LastRunID == nil || *h.LastRunID != ridSucesso {
		t.Errorf("last_run_id %v, queria %v", h.LastRunID, ridSucesso)
	}
	logs := eventosDoTipo(a.eventos(t, id), "log")
	if len(logs) != 1 {
		t.Fatalf("%d eventos log, queria 1", len(logs))
	}
	if logs[0].Source != "ping" || !logs[0].At.Equal(agora.Add(2*time.Minute)) {
		t.Errorf("evento log: %+v", logs[0])
	}
	if logs[0].RunID == nil || *logs[0].RunID != ridLog {
		t.Errorf("run_id do log %v, queria %v", logs[0].RunID, ridLog)
	}
}

// O log divide o teto com os outros sinais: entra um, sai o mais velho.
func TestLogSharesTheEventCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	id, token := a.novoHeartbeat(t)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		for range heartbeat.MaxEventsPerHeartbeat {
			err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: uuid.Must(uuid.NewV7()), HeartbeatID: id, At: agora, Kind: "start", Source: "ping",
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	maisVelho := a.eventos(t, id)[heartbeat.MaxEventsPerHeartbeat-1].ID

	a.pingarRun(t, token, heartbeat.SignalLog, nil)

	ev := a.eventos(t, id)
	if len(ev) != 1000 {
		t.Fatalf("%d eventos, queria 1000", len(ev))
	}
	if ev[0].Kind != "log" {
		t.Errorf("o evento novo saiu: %+v", ev[0])
	}
	if ev[len(ev)-1].ID == maisVelho {
		t.Error("o evento mais velho ficou")
	}
}

// O log é aceito em qualquer heartbeat e não mexe em pausa, falha nem prazo,
// nem quando o dono ligou a retomada por ping: só o sucesso retoma.
func TestLogOnPausedFailingAndUnknownHeartbeats(t *testing.T) {
	a := novoAmbiente(t)
	semChecksDeAgendamento(t, a.pool)
	falhandoDesde := agora.Add(-30 * time.Minute)
	casos := []struct {
		nome         string
		preparar     func(t *testing.T) (uuid.UUID, string)
		enabled      bool
		failingSince *time.Time
		prazo        time.Time
	}{
		{"pausado", func(t *testing.T) (uuid.UUID, string) {
			id, token := a.novoHeartbeat(t)
			a.comoDono(t, `UPDATE heartbeats SET enabled = false WHERE id = $1`, id)
			return id, token
		}, false, nil, agora.Add(time.Hour)},
		{"em falha", func(t *testing.T) (uuid.UUID, string) {
			id, token := a.novoHeartbeat(t)
			a.comoDono(t, `UPDATE heartbeats SET failing_since = $2 WHERE id = $1`, id, falhandoDesde)
			return id, token
		}, true, &falhandoDesde, agora.Add(time.Hour)},
		{"agendamento desconhecido", a.heartbeatDesconhecido, true, nil, agora.Add(-2 * time.Hour)},
	}
	for _, retoma := range []bool{false, true} {
		for _, c := range casos {
			t.Run(fmt.Sprintf("%s, ping_resumes=%t", c.nome, retoma), func(t *testing.T) {
				id, token := c.preparar(t)
				a.comoDono(t, `UPDATE heartbeats SET ping_resumes = $2 WHERE id = $1`, id, retoma)

				if err := a.svc.Ping(context.Background(), token, heartbeat.SignalLog, nil); err != nil {
					t.Fatalf("log: %v", err)
				}

				if n := len(a.eventos(t, id)); n != 1 {
					t.Errorf("%d eventos, queria 1", n)
				}
				h := a.linha(t, id)
				if h.Enabled != c.enabled {
					t.Errorf("enabled %t, queria %t", h.Enabled, c.enabled)
				}
				if !mesmoInstante(h.FailingSince, c.failingSince) {
					t.Errorf("failing_since %v, queria %v", h.FailingSince, c.failingSince)
				}
				if !h.DeadlineAt.Equal(c.prazo) {
					t.Errorf("prazo %v, queria %v", h.DeadlineAt, c.prazo)
				}
				if h.ResumedAt != nil {
					t.Errorf("resumed_at %v, queria nulo", h.ResumedAt)
				}
			})
		}
	}
}

func mesmoInstante(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Duas execuções sobrepostas: cada término mede do início do próprio rid, e
// não do início aberto da linha nem de outro evento de mesmo rid.
func TestRunIDDurationsFromTheLog(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	ridA := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-00000000000a")
	ridB := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-00000000000b")

	a.pingarRun(t, token, heartbeat.SignalStart, &ridA)
	a.clk.Advance(5 * time.Minute)
	a.pingarRun(t, token, heartbeat.SignalStart, &ridB)
	a.clk.Advance(5 * time.Minute)
	a.pingarRun(t, token, heartbeat.SignalLog, &ridA)
	a.clk.Advance(10 * time.Minute)
	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridA) // agora + 20 min
	a.clk.Advance(15 * time.Minute)
	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridB) // 30 min desde o início de B

	ev := a.eventos(t, id)
	if len(ev) != 5 {
		t.Fatalf("%d eventos, queria 5", len(ev))
	}
	esperados := []struct {
		kind    string
		rid     uuid.UUID
		runtime *int64
	}{
		{"success", ridB, ptr(int64(1800000))},
		{"success", ridA, ptr(int64(1200000))},
		{"log", ridA, nil},
		{"start", ridB, nil},
		{"start", ridA, nil},
	}
	for i, e := range esperados {
		got := ev[i]
		if got.Kind != e.kind || got.RunID == nil || *got.RunID != e.rid {
			t.Errorf("evento %d: %s %v, queria %s %v", i, got.Kind, got.RunID, e.kind, e.rid)
		}
		if !mesmoNumero(got.RuntimeMS, e.runtime) {
			t.Errorf("evento %d: runtime_ms %s, queria %s", i, numero(got.RuntimeMS), numero(e.runtime))
		}
	}
	if h := a.linha(t, id); h.LastStartAt != nil {
		t.Errorf("last_start_at %v: o término de B devia consumir o início aberto", h.LastStartAt)
	}
}

func numero(p *int64) string {
	if p == nil {
		return "nulo"
	}
	return fmt.Sprint(*p)
}

func mesmoNumero(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// O teto aparou o início: o sucesso grava sem duração, sem erro, e a execução
// aberta da linha fica como estava.
func TestSuccessWhoseStartWasTrimmed(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	ridA := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-00000000000a")
	a.pingarRun(t, token, heartbeat.SignalStart, &ridA)
	for range heartbeat.MaxEventsPerHeartbeat {
		a.pingarRun(t, token, heartbeat.SignalLog, nil)
	}
	a.clk.Advance(20 * time.Minute)

	if err := a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess, &ridA); err != nil {
		t.Fatalf("sucesso: %v", err)
	}

	ev := a.eventos(t, id)
	if ev[0].Kind != "success" || ev[0].RuntimeMS != nil {
		t.Errorf("sucesso: %s, runtime_ms %s, queria sem duração", ev[0].Kind, numero(ev[0].RuntimeMS))
	}
	h := a.linha(t, id)
	if h.LastStartAt == nil || !h.LastStartAt.Equal(agora) {
		t.Errorf("last_start_at %v, queria %v intacto", h.LastStartAt, agora)
	}
	if h.LastRuntimeMS != nil {
		t.Errorf("last_runtime_ms %v, queria nulo", *h.LastRuntimeMS)
	}
}

// Só o sucesso retoma, só com ping_resumes, e a repetição não retoma de novo.
func TestPingResumesOnSuccess(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	a.comoDono(t, `UPDATE heartbeats SET enabled = false, ping_resumes = true WHERE id = $1`, id)

	a.pingar(t, token, heartbeat.SignalStart)
	if h := a.linha(t, id); h.Enabled || h.ResumedAt != nil {
		t.Fatalf("o início retomou: enabled %t, resumed_at %v", h.Enabled, h.ResumedAt)
	}

	a.clk.Advance(time.Minute)
	a.pingar(t, token, heartbeat.SignalSuccess)
	retomadaEm := agora.Add(time.Minute)
	h := a.linha(t, id)
	if !h.Enabled {
		t.Error("o sucesso não retomou")
	}
	if h.ResumedAt == nil || !h.ResumedAt.Equal(retomadaEm) {
		t.Errorf("resumed_at %v, queria %v", h.ResumedAt, retomadaEm)
	}
	if want := agora.Add(time.Minute + time.Hour + 5*time.Minute); !h.DeadlineAt.Equal(want) {
		t.Errorf("prazo %v, queria %v", h.DeadlineAt, want)
	}

	a.clk.Advance(5 * time.Second)
	a.pingar(t, token, heartbeat.SignalSuccess)
	if n := len(a.eventos(t, id)); n != 2 {
		t.Errorf("a repetição gravou: %d eventos, queria 2", n)
	}
	if h := a.linha(t, id); h.ResumedAt == nil || !h.ResumedAt.Equal(retomadaEm) {
		t.Errorf("a repetição mudou resumed_at para %v", h.ResumedAt)
	}

	semRetomada, token2 := a.novoHeartbeat(t)
	a.comoDono(t, `UPDATE heartbeats SET enabled = false WHERE id = $1`, semRetomada)
	a.pingar(t, token2, heartbeat.SignalSuccess)
	if h := a.linha(t, semRetomada); h.Enabled || h.ResumedAt != nil {
		t.Errorf("sem ping_resumes: enabled %t, resumed_at %v", h.Enabled, h.ResumedAt)
	}
}

// O rid da linha volta do banco para a guarda da repetição: o mesmo sucesso de
// mesmo rid em 5 s é repetição, e o de outro rid é outra execução.
func TestRepeatComparesTheStoredRunID(t *testing.T) {
	a := novoAmbiente(t)
	id, token := a.novoHeartbeat(t)
	ridA := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-00000000000a")
	ridB := uuid.MustParse("0195f3c2-8a4b-7c1d-9e2f-00000000000b")

	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridA)
	a.clk.Advance(5 * time.Second)
	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridA)
	if n := len(a.eventos(t, id)); n != 1 {
		t.Errorf("mesmo rid em 5 s: %d eventos, queria 1", n)
	}

	a.clk.Advance(time.Second)
	a.pingarRun(t, token, heartbeat.SignalSuccess, &ridB)
	if n := len(a.eventos(t, id)); n != 2 {
		t.Errorf("outro rid: %d eventos, queria 2", n)
	}
}
