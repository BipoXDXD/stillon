package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// Os laços de produção (Dispatcher.Run e Reminders.Run) rodam num errgroup,
// ao lado da sonda e da API. Se um deles devolvesse o primeiro erro do banco,
// o errgroup derrubaria o processo inteiro; se parasse em silêncio, os avisos
// deixariam de sair sem alarme. O contrato: o erro de um ciclo vai para o log
// e o laço continua; só o cancelamento do ctx o encerra, com nil e sem ruído.
//
// Os tiques vêm de um ritmo manual: cada tique é um envio sem buffer, que só
// completa quando o laço está de volta ao select. O teste não espera o
// período real de nenhum dos laços.
//
// capturaErrosDoLog troca o slog padrão: nenhum teste deste pacote pode usar
// t.Parallel, senão um teste leria o log do outro.

const (
	msgFalhaDoDespacho = "despachar notificações"
	msgFalhaDoLembrete = "enfileirar lembretes"
	// guardaDoLaco só limita um teste que falhou; com o laço correto, cada
	// passo acontece em milissegundos.
	guardaDoLaco = 10 * time.Second
	// ciclosAposOErro é quantos tiques o laço tem de atender depois do
	// primeiro erro: um laço que tenta de novo uma vez e para não passa.
	ciclosAposOErro = 2
)

// ritmoManual é o ritmo dos testes: guarda o período pedido, entrega os
// tiques que o teste mandar e registra a chamada do parar (o Stop do ticker
// real).
type ritmoManual struct {
	tiques  chan time.Time
	periodo chan time.Duration
	parou   chan struct{}
}

func novoRitmoManual() *ritmoManual {
	return &ritmoManual{
		tiques:  make(chan time.Time),
		periodo: make(chan time.Duration, 1),
		parou:   make(chan struct{}, 1),
	}
}

func (m *ritmoManual) criar(periodo time.Duration) (<-chan time.Time, func()) {
	m.periodo <- periodo
	return m.tiques, func() {
		select {
		case m.parou <- struct{}{}:
		default:
		}
	}
}

// periodoPedido exige que o laço tenha criado o ritmo com o período de
// produção.
func (m *ritmoManual) periodoPedido(t *testing.T, queria time.Duration) {
	t.Helper()
	select {
	case p := <-m.periodo:
		if p != queria {
			t.Errorf("período do ritmo = %v, queria %v", p, queria)
		}
	case <-time.After(guardaDoLaco):
		t.Fatal("o laço não criou o ritmo")
	}
}

// tique entrega um tique e reprova se o laço tiver terminado antes de recebê-lo.
func (m *ritmoManual) tique(t *testing.T, feito <-chan error) {
	t.Helper()
	select {
	case m.tiques <- time.Time{}:
	case err := <-feito:
		t.Fatalf("o laço terminou sozinho com %v; devia seguir esperando o tique", err)
	case <-time.After(guardaDoLaco):
		t.Fatal("o laço não voltou a esperar o tique")
	}
}

// poolSemBanco é um pool que nunca conecta: o pgxpool abre as conexões sob
// demanda, e a porta 1 do loopback recusa na hora. Todo ciclo falha rápido,
// sem Docker.
func poolSemBanco(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://stillon:x@127.0.0.1:1/stillon?connect_timeout=1&sslmode=disable")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// capturaErrosDoLog troca o slog padrão por um que repassa a mensagem de cada
// registro de erro para o canal. O padrão volta no fim.
func capturaErrosDoLog(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 64)
	anterior := slog.Default()
	slog.SetDefault(slog.New(&espelhoDeErros{ch: ch}))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	return ch
}

type espelhoDeErros struct{ ch chan<- string }

func (*espelhoDeErros) Enabled(context.Context, slog.Level) bool { return true }
func (e *espelhoDeErros) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelError {
		return nil
	}
	select {
	case e.ch <- r.Message:
	default:
	}
	return nil
}
func (e *espelhoDeErros) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *espelhoDeErros) WithGroup(string) slog.Handler      { return e }

func iniciarLaco(ctx context.Context, laco func(context.Context) error) <-chan error {
	feito := make(chan error, 1)
	go func() { feito <- laco(ctx) }()
	return feito
}

// esperarErroDoCiclo bloqueia até o log trazer um erro com essa mensagem.
func esperarErroDoCiclo(t *testing.T, logs <-chan string, feito <-chan error, msg string) {
	t.Helper()
	limite := time.After(guardaDoLaco)
	for {
		select {
		case m := <-logs:
			if m == msg {
				return
			}
		case err := <-feito:
			t.Fatalf("o laço terminou sozinho com %v antes de registrar %q", err, msg)
		case <-limite:
			t.Fatalf("o erro %q não foi registrado", msg)
		}
	}
}

// encerrarLaco cancela o ctx e exige que o laço devolva nil e que, ao
// voltar, já tenha parado o ritmo: um ticker esquecido seguiria disparando.
func (m *ritmoManual) encerrarLaco(t *testing.T, cancel context.CancelFunc, feito <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-feito:
		if err != nil {
			t.Errorf("laço encerrado pelo ctx devolveu %v, queria nil", err)
		}
	case <-time.After(guardaDoLaco):
		t.Fatal("o laço não encerrou depois do cancelamento do ctx")
	}
	select {
	case <-m.parou:
	default:
		t.Error("o laço voltou sem parar o ritmo")
	}
}

// semErroNoLog reprova se o log tiver o erro com essa mensagem.
func semErroNoLog(t *testing.T, logs <-chan string, msg string) {
	t.Helper()
	for {
		select {
		case m := <-logs:
			if m == msg {
				t.Errorf("erro registrado no encerramento: %q", m)
			}
		default:
			return
		}
	}
}

func TestDispatcherRunLogsTheCycleErrorAndKeepsRunning(t *testing.T) {
	logs := capturaErrosDoLog(t)
	ritmo := novoRitmoManual()
	d := NewDispatcher(poolSemBanco(t), nil, clock.Real{}, prometheus.NewRegistry())
	d.ritmo = ritmo.criar
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feito := iniciarLaco(ctx, d.Run)

	ritmo.periodoPedido(t, dispatchEvery)
	for range 1 + ciclosAposOErro {
		ritmo.tique(t, feito)
		esperarErroDoCiclo(t, logs, feito, msgFalhaDoDespacho)
	}
	ritmo.tique(t, feito)
	ritmo.encerrarLaco(t, cancel, feito)
}

// O sinal de encerramento chega com Dispatch preso no banco: a reivindicação
// falha por causa do cancelamento, e isso não é erro do banco. O encerramento
// não pode sujar o log (e o alarme de erro) a cada deploy.
func TestDispatcherRunStopsQuietlyWhenCancelledDuringDispatch(t *testing.T) {
	logs := capturaErrosDoLog(t)
	ritmo := novoRitmoManual()
	pool, conectou := poolQueNaoResponde(t)
	d := NewDispatcher(pool, nil, clock.Real{}, prometheus.NewRegistry())
	d.ritmo = ritmo.criar
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feito := iniciarLaco(ctx, d.Run)

	ritmo.periodoPedido(t, dispatchEvery)
	ritmo.tique(t, feito)
	select {
	case <-conectou:
	case err := <-feito:
		t.Fatalf("o laço terminou antes de chegar ao banco com %v", err)
	case <-time.After(guardaDoLaco):
		t.Fatal("o ciclo não chegou ao banco")
	}
	ritmo.encerrarLaco(t, cancel, feito)
	semErroNoLog(t, logs, msgFalhaDoDespacho)
}

// poolQueNaoResponde aponta para um servidor que aceita a conexão e nunca
// responde, e devolve o canal avisado a cada conexão aceita: quando ele
// dispara, um Dispatch está bloqueado no banco, à espera do ctx.
func poolQueNaoResponde(t *testing.T) (*pgxpool.Pool, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	conectou := make(chan struct{}, 1)
	var mu sync.Mutex
	var abertas []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			abertas = append(abertas, c)
			mu.Unlock()
			select {
			case conectou <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range abertas {
			_ = c.Close()
		}
	})
	dsn := fmt.Sprintf("postgres://stillon:x@%s/stillon?connect_timeout=30&sslmode=disable", ln.Addr())
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, conectou
}

// O primeiro ciclo dos lembretes roda na subida, sem esperar tique; os
// seguintes, a cada tique.
func TestRemindersRunLogsTheCycleErrorAndKeepsRunning(t *testing.T) {
	logs := capturaErrosDoLog(t)
	ritmo := novoRitmoManual()
	r := NewReminders(poolSemBanco(t), clock.Real{}, DefaultPolicy())
	r.ritmo = ritmo.criar
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feito := iniciarLaco(ctx, r.Run)

	ritmo.periodoPedido(t, remindEvery)
	esperarErroDoCiclo(t, logs, feito, msgFalhaDoLembrete)
	for range ciclosAposOErro {
		ritmo.tique(t, feito)
		esperarErroDoCiclo(t, logs, feito, msgFalhaDoLembrete)
	}
	ritmo.tique(t, feito)
	ritmo.encerrarLaco(t, cancel, feito)
}

// Com o ctx já cancelado, o ciclo de lembretes falha por causa do
// cancelamento, e isso não é erro do banco.
func TestRemindersRunStopsQuietlyWhenTheContextIsCancelled(t *testing.T) {
	logs := capturaErrosDoLog(t)
	ritmo := novoRitmoManual()
	r := NewReminders(poolSemBanco(t), clock.Real{}, DefaultPolicy())
	r.ritmo = ritmo.criar
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ritmo.encerrarLaco(t, cancel, iniciarLaco(ctx, r.Run))
	semErroNoLog(t, logs, msgFalhaDoLembrete)
}
