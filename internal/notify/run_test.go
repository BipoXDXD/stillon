package notify

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// Os dois laços de produção (Dispatcher.Run e Reminders.Run) rodam num
// errgroup, ao lado da sonda e da API. Se um deles devolvesse o primeiro erro
// do banco, o errgroup derrubaria o processo inteiro; se parasse em silêncio,
// os avisos deixariam de sair sem alarme nenhum. O contrato é: o erro de um
// ciclo vai para o log e o laço continua; só o cancelamento do ctx o encerra,
// com nil e sem ruído.

const (
	msgFalhaDoDespacho  = "despachar notificações"
	msgFalhaDoLembrete  = "enfileirar lembretes"
	esperaDoRegistro    = 10 * time.Second
	janelaDeSobrevivida = 300 * time.Millisecond
)

// poolSemBanco é um pool que nunca conecta: o pgxpool abre as conexões sob
// demanda, e a porta 1 do loopback recusa na hora. Todo ciclo dos laços falha
// rápido, sem Docker.
func poolSemBanco(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://stillon:x@127.0.0.1:1/stillon?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// capturaLog troca o slog padrão por um que repassa a mensagem de cada
// registro de erro para o canal, e devolve o canal. O padrão volta no fim.
func capturaLog(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 64)
	anterior := slog.Default()
	slog.SetDefault(slog.New(&espelhoDeErros{ch: ch}))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	return ch
}

type espelhoDeErros struct {
	mu sync.Mutex
	ch chan<- string
}

func (*espelhoDeErros) Enabled(context.Context, slog.Level) bool { return true }
func (e *espelhoDeErros) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelError {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case e.ch <- r.Message:
	default:
	}
	return nil
}
func (e *espelhoDeErros) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *espelhoDeErros) WithGroup(string) slog.Handler      { return e }

// iniciar roda laco numa goroutine e devolve o canal com o resultado dele.
func iniciar(ctx context.Context, laco func(context.Context) error) <-chan error {
	feito := make(chan error, 1)
	go func() { feito <- laco(ctx) }()
	return feito
}

// esperarRegistro bloqueia até o log trazer um erro com essa mensagem.
func esperarRegistro(t *testing.T, ch <-chan string, msg string) {
	t.Helper()
	limite := time.After(esperaDoRegistro)
	for {
		select {
		case m := <-ch:
			if m == msg {
				return
			}
		case <-limite:
			t.Fatalf("o erro %q não foi registrado em %v", msg, esperaDoRegistro)
		}
	}
}

// seguiuRodando reprova se o laço devolver algo dentro da janela.
func seguiuRodando(t *testing.T, feito <-chan error) {
	t.Helper()
	select {
	case err := <-feito:
		t.Fatalf("o laço terminou sozinho com %v depois do erro do ciclo; devia seguir rodando", err)
	case <-time.After(janelaDeSobrevivida):
	}
}

// encerrar cancela o ctx e exige que o laço devolva nil em tempo útil.
func encerrar(t *testing.T, cancel context.CancelFunc, feito <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-feito:
		if err != nil {
			t.Errorf("laço encerrado pelo ctx devolveu %v, queria nil", err)
		}
	case <-time.After(esperaDoRegistro):
		t.Fatal("o laço não encerrou depois do cancelamento do ctx")
	}
}

// semRegistro reprova se o log tiver o erro com essa mensagem. Outras
// mensagens são de goroutines de outros testes e não contam.
func semRegistro(t *testing.T, ch <-chan string, msg string) {
	t.Helper()
	for {
		select {
		case m := <-ch:
			if m == msg {
				t.Errorf("erro registrado no encerramento: %q", m)
			}
		default:
			return
		}
	}
}

func TestDispatcherRunLogsTheCycleErrorAndKeepsRunning(t *testing.T) {
	logs := capturaLog(t)
	d := NewDispatcher(poolSemBanco(t), nil, clock.Real{}, prometheus.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feito := iniciar(ctx, d.Run)

	esperarRegistro(t, logs, msgFalhaDoDespacho)
	seguiuRodando(t, feito)
	encerrar(t, cancel, feito)
}

func TestRemindersRunLogsTheCycleErrorAndKeepsRunning(t *testing.T) {
	logs := capturaLog(t)
	r := NewReminders(poolSemBanco(t), clock.Real{}, DefaultPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feito := iniciar(ctx, r.Run)

	// O primeiro ciclo roda na hora, e não só depois de remindEvery.
	esperarRegistro(t, logs, msgFalhaDoLembrete)
	seguiuRodando(t, feito)
	encerrar(t, cancel, feito)
}

// Com o ctx já cancelado, o ciclo de lembretes falha por causa do
// cancelamento, e isso não é erro do banco: o encerramento não pode sujar o
// log (e o alarme de erro) a cada deploy.
func TestRemindersRunStopsQuietlyWhenTheContextIsCancelled(t *testing.T) {
	logs := capturaLog(t)
	r := NewReminders(poolSemBanco(t), clock.Real{}, DefaultPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	encerrar(t, cancel, iniciar(ctx, r.Run))
	semRegistro(t, logs, msgFalhaDoLembrete)
}
