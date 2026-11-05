package heartbeat_test

import (
	"context"
	"errors"
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
	if err := a.svc.Ping(context.Background(), token, sig); err != nil {
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
		if err := a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess); !errors.Is(err, heartbeat.ErrUnknownToken) {
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
			erros <- a.svc.Ping(context.Background(), token, heartbeat.SignalSuccess)
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

// O teto vale na transação do ping, e o que sai é o mais velho.
func TestEventsStayUnderTheCap(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	id, token := a.novoHeartbeat(t)
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		for range heartbeat.MaxEventsPerHeartbeat {
			err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: uuid.Must(uuid.NewV7()), HeartbeatID: id, At: agora, Kind: "start",
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
			ID: uuid.Must(uuid.NewV7()), HeartbeatID: id, At: agora, Kind: "start",
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
	err := storage.InUserTx(ctx, a.pool, a.dono, func(q *db.Queries) error {
		return q.SetHeartbeatEnabled(ctx, db.SetHeartbeatEnabledParams{UserID: a.dono, ID: id, Enabled: false})
	})
	if err != nil {
		t.Fatal(err)
	}
	a.pingar(t, token, heartbeat.SignalSuccess)
	if n := len(a.eventos(t, id)); n != 1 {
		t.Errorf("%d eventos, queria 1", n)
	}
}
