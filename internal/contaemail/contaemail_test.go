package contaemail_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/notify"
)

var chaveDeTeste = bytes.Repeat([]byte{7}, crypto.KeySize)

type envio struct {
	para, assunto, corpo string
	comPrazo             bool
}

// transporteFalso entrega cada envio no canal chegou. Com o ctx encerrado,
// falha como o transporte real falharia.
type transporteFalso struct {
	chegou chan envio
	erro   error
}

func novoTransporte() *transporteFalso { return &transporteFalso{chegou: make(chan envio, 256)} }

func (t *transporteFalso) SendAccount(ctx context.Context, to crypto.Secret, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, comPrazo := ctx.Deadline()
	t.chegou <- envio{string(to.Reveal()), subject, body, comPrazo}
	return t.erro
}

func (t *transporteFalso) SendVerification(ctx context.Context, dest crypto.Secret, link string) error {
	return t.SendAccount(ctx, dest, "verificação", link)
}

func (t *transporteFalso) total() int { return len(t.chegou) }

type tetoFalso struct {
	mu       sync.Mutex
	reservas int
	libera   bool
	erro     error
}

func (t *tetoFalso) Reservar(context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reservas++
	return t.libera, t.erro
}

func (t *tetoFalso) total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reservas
}

type gasto struct {
	balde  string
	chave  []byte
	aCada  time.Duration
	rajada int
}

type orcamentosFalsos struct {
	mu     sync.Mutex
	gastos []gasto
	libera bool
	erro   error
}

func (o *orcamentosFalsos) Gastar(_ context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gastos = append(o.gastos, gasto{balde, bytes.Clone(chave), aCada, rajada})
	if o.erro != nil {
		return 0, false, o.erro
	}
	if !o.libera {
		return time.Minute, false, nil
	}
	return 0, true, nil
}

func (o *orcamentosFalsos) lista() []gasto {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]gasto(nil), o.gastos...)
}

// logTravado protege o buffer: o consumidor escreve numa goroutine e o
// teste lê na outra.
type logTravado struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logTravado) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logTravado) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type cenario struct {
	transporte *transporteFalso
	teto       *tetoFalso
	orcamentos *orcamentosFalsos
	log        *logTravado
	enviador   *contaemail.Enviador
}

func novoCenario(baldeLibera, tetoLibera bool) *cenario {
	c := &cenario{
		transporte: novoTransporte(),
		teto:       &tetoFalso{libera: tetoLibera},
		orcamentos: &orcamentosFalsos{libera: baldeLibera},
		log:        &logTravado{},
	}
	logger := slog.New(slog.NewTextHandler(c.log, nil))
	c.enviador = contaemail.New(c.transporte, c.teto, c.orcamentos, chaveDeTeste, logger)
	return c
}

// rodar sobe o consumidor e devolve a função que o encerra e espera ele voltar.
func (c *cenario) rodar(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	feito := make(chan struct{})
	go func() {
		c.enviador.Rodar(ctx)
		close(feito)
	}()
	parar := func() {
		cancel()
		select {
		case <-feito:
		case <-time.After(15 * time.Second):
			t.Fatal("Rodar não voltou depois do cancel")
		}
	}
	t.Cleanup(func() { cancel(); <-feito })
	return parar
}

func enfileirar(t *testing.T, e *contaemail.Enviador, para string) {
	t.Helper()
	if err := e.SendAccountEmail(context.Background(), para, "Assunto do teste", "Corpo do teste\n", account.EntregaDeEmail{}); err != nil {
		t.Fatalf("SendAccountEmail = %v, queria nil", err)
	}
}

func TestEnviaQuandoHaBaldeETeto(t *testing.T) {
	c := novoCenario(true, true)
	c.rodar(t)

	enfileirar(t, c.enviador, "pessoa@exemplo.com")

	select {
	case got := <-c.transporte.chegou:
		if got.para != "pessoa@exemplo.com" || got.assunto != "Assunto do teste" || got.corpo != "Corpo do teste\n" {
			t.Errorf("envio = %+v, queria destinatário, assunto e corpo intactos", got)
		}
		if !got.comPrazo {
			t.Error("o envio saiu sem prazo próprio")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o e-mail não chegou ao transporte")
	}
}

func TestBaldeVazioNaoGastaOTeto(t *testing.T) {
	c := novoCenario(false, true)
	parar := c.rodar(t)

	enfileirar(t, c.enviador, "pessoa@exemplo.com")
	parar()

	if n := len(c.orcamentos.lista()); n != 1 {
		t.Fatalf("Gastar recebeu %d chamadas, queria 1", n)
	}
	if n := c.teto.total(); n != 0 {
		t.Errorf("Reservar recebeu %d chamadas com o balde vazio, queria 0", n)
	}
	if n := c.transporte.total(); n != 0 {
		t.Errorf("o transporte recebeu %d e-mails com o balde vazio, queria 0", n)
	}
	if !strings.Contains(c.log.String(), "motivo=destino") {
		t.Errorf("log sem motivo=destino: %s", c.log.String())
	}
}

func TestTetoEsgotadoSuprime(t *testing.T) {
	c := novoCenario(true, false)
	parar := c.rodar(t)

	enfileirar(t, c.enviador, "pessoa@exemplo.com")
	parar()

	if n := c.teto.total(); n != 1 {
		t.Fatalf("Reservar recebeu %d chamadas, queria 1", n)
	}
	if n := c.transporte.total(); n != 0 {
		t.Errorf("o transporte recebeu %d e-mails com o teto esgotado, queria 0", n)
	}
	log := c.log.String()
	if !strings.Contains(log, "motivo=teto") {
		t.Errorf("log sem motivo=teto: %s", log)
	}
	if strings.Contains(log, "pessoa@exemplo.com") {
		t.Errorf("o log traz o endereço: %s", log)
	}
}

func TestFilaCheiaNaoBloqueia(t *testing.T) {
	c := novoCenario(true, true) // consumidor parado: Rodar não sobe

	// As chamadas rodam numa goroutine: uma fila que bloqueia reprova aqui,
	// e não pelo timeout do go test.
	inicio := time.Now()
	erros := make(chan error, 1)
	go func() {
		for range 101 {
			if err := c.enviador.SendAccountEmail(context.Background(), "pessoa@exemplo.com", "a", "b", account.EntregaDeEmail{}); err != nil {
				erros <- err
				return
			}
		}
		erros <- nil
	}()
	select {
	case err := <-erros:
		if !errors.Is(err, account.ErrEmailSuprimido) {
			t.Fatalf("fila cheia: %v, queria ErrEmailSuprimido", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SendAccountEmail bloqueou com a fila cheia")
	}
	if d := time.Since(inicio); d >= 100*time.Millisecond {
		t.Errorf("101 chamadas levaram %v com a fila cheia, queria < 100ms", d)
	}
	log := c.log.String()
	if !strings.Contains(log, "motivo=fila") {
		t.Errorf("log sem motivo=fila: %s", log)
	}
	if strings.Contains(log, "pessoa@exemplo.com") {
		t.Errorf("o log traz o endereço: %s", log)
	}
}

func TestRodarDrenaAoEncerrar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := novoCenario(true, true)
		for range 3 {
			enfileirar(t, c.enviador, "pessoa@exemplo.com")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // encerrado antes de o consumidor ver qualquer item

		inicio := time.Now()
		c.enviador.Rodar(ctx)

		if n := c.transporte.total(); n != 3 {
			t.Errorf("o transporte recebeu %d e-mails antes de Rodar voltar, queria 3", n)
		}
		if d := time.Since(inicio); d != 0 {
			t.Errorf("o dreno imediato avançou %v no relógio virtual", d)
		}
	})
}

func TestVerificacaoComTetoPassaPeloTeto(t *testing.T) {
	t.Run("teto esgotado sinaliza supressão", func(t *testing.T) {
		c := novoCenario(true, false)
		m := novoTransporte()
		v := contaemail.NovaVerificacaoComTeto(c.enviador, m)

		if err := v.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/l"); !errors.Is(err, notify.ErrVerificationSuppressed) {
			t.Errorf("SendVerification com teto esgotado = %v, queria ErrVerificationSuppressed", err)
		}
		if n := m.total(); n != 0 {
			t.Errorf("o mailer recebeu %d envios com o teto esgotado, queria 0", n)
		}
	})
	t.Run("balde vazio suprime sem gastar o teto", func(t *testing.T) {
		c := novoCenario(false, true)
		m := novoTransporte()
		v := contaemail.NovaVerificacaoComTeto(c.enviador, m)

		if err := v.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/l"); !errors.Is(err, notify.ErrVerificationSuppressed) {
			t.Errorf("SendVerification sem ficha no balde = %v, queria ErrVerificationSuppressed", err)
		}
		if m.total() != 0 || c.teto.total() != 0 {
			t.Errorf("mailer = %d, Reservar = %d; queria 0 e 0", m.total(), c.teto.total())
		}
	})
	t.Run("erro do transporte volta", func(t *testing.T) {
		c := novoCenario(true, true)
		m := novoTransporte()
		boom := errors.New("resend fora do ar")
		m.erro = boom
		v := contaemail.NovaVerificacaoComTeto(c.enviador, m)

		err := v.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/l")
		if !errors.Is(err, boom) {
			t.Errorf("SendVerification = %v, queria o erro do transporte", err)
		}
		if got := <-m.chegou; got.para != "p@x.com" || got.corpo != "https://v.dev/l" {
			t.Errorf("envio = %+v, queria destino e link intactos", got)
		}
	})
}

func TestChaveDoBaldeIgnoraCaixa(t *testing.T) {
	c := novoCenario(true, true)
	parar := c.rodar(t)

	enfileirar(t, c.enviador, "A@X.com")
	enfileirar(t, c.enviador, "a@x.com")
	parar()

	gastos := c.orcamentos.lista()
	if len(gastos) != 2 {
		t.Fatalf("Gastar recebeu %d chamadas, queria 2", len(gastos))
	}
	if !bytes.Equal(gastos[0].chave, gastos[1].chave) {
		t.Error("A@X.com e a@x.com gastaram chaves diferentes")
	}
	quer := crypto.HashIP(chaveDeTeste, "conta_destino:a@x.com")
	for _, g := range gastos {
		if g.balde != "conta_destino" || g.aCada != 20*time.Minute || g.rajada != 3 || !bytes.Equal(g.chave, quer) {
			t.Errorf("gasto = {%s %x %v %d}, queria {conta_destino HMAC 20m 3}", g.balde, g.chave, g.aCada, g.rajada)
		}
	}
}

// transporteParado segura cada envio até o ctx dele acabar, como um provedor
// que não responde.
type transporteParado struct{ comecou chan struct{} }

func (t *transporteParado) SendAccount(ctx context.Context, _ crypto.Secret, _, _ string) error {
	t.comecou <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

// O encerramento inteiro (envio em curso mais dreno) cabe no orçamento do
// dreno, contado do cancelamento: um envio parado não ganha os 15 s dele.
func TestEncerramentoCabeNoPrazoDoDreno(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transporte := &transporteParado{comecou: make(chan struct{}, 4)}
		log := &logTravado{}
		e := contaemail.New(transporte, &tetoFalso{libera: true}, &orcamentosFalsos{libera: true},
			chaveDeTeste, slog.New(slog.NewTextHandler(log, nil)))
		const orcamento = 10 * time.Second
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		feito := make(chan struct{})
		go func() { e.Rodar(ctx); close(feito) }()

		resultados := make(chan account.ResultadoDoEnvio, 2)
		concluir := func(_ context.Context, resultado account.ResultadoDoEnvio) error {
			resultados <- resultado
			return nil
		}
		for _, destino := range []string{"pessoa@exemplo.com", "outra@exemplo.com"} {
			if err := e.SendAccountEmail(t.Context(), destino, "Assunto", "Corpo", account.EntregaDeEmail{Concluir: concluir}); err != nil {
				t.Fatal(err)
			}
		}
		<-transporte.comecou
		inicio := time.Now()
		cancel()
		<-feito

		if len(resultados) != 2 {
			t.Fatalf("%d conclusões no encerramento, queria duas", len(resultados))
		}
		if emVoo, naFila := <-resultados, <-resultados; emVoo != account.EnvioIncerto || naFila != account.EnvioDescartado {
			t.Errorf("conclusões no encerramento: %v, %v. Queria envio em voo incerto e enfileirado descartado", emVoo, naFila)
		}
		if d := time.Since(inicio); d != orcamento {
			t.Errorf("Rodar levou %v no relógio virtual depois do cancel, queria %v", d, orcamento)
		}
		if registro := log.String(); !strings.Contains(registro, "e-mails de conta descartados no encerramento") ||
			!strings.Contains(registro, "quantidade=1") {
			t.Errorf("faltou registrar o descarte de um e-mail: %s", registro)
		}
	})
}

func TestVerificacaoComBaldeOuTetoComErroDevolveOErro(t *testing.T) {
	boom := errors.New("banco fora do ar")
	casos := map[string]func(*cenario){
		"Gastar":   func(c *cenario) { c.orcamentos.erro = boom },
		"Reservar": func(c *cenario) { c.teto.erro = boom },
	}
	for nome, quebrar := range casos {
		t.Run(nome, func(t *testing.T) {
			c := novoCenario(true, true)
			quebrar(c)
			m := novoTransporte()
			v := contaemail.NovaVerificacaoComTeto(c.enviador, m)

			if err := v.SendVerification(context.Background(), crypto.Secret("p@x.com"), "https://v.dev/l"); !errors.Is(err, boom) {
				t.Errorf("SendVerification = %v, queria o erro de %s", err, nome)
			}
			if n := m.total(); n != 0 {
				t.Errorf("o transporte recebeu %d envios, queria 0", n)
			}
		})
	}
}

// Depois de Rodar voltar, ninguém consome a fila: um e-mail enfileirado ali
// sumiria sem rastro. Ele é recusado e registrado no log, sem o endereço.
func TestEnvioDepoisDoEncerramentoVaiParaOLog(t *testing.T) {
	c := novoCenario(true, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.enviador.Rodar(ctx)

	if err := c.enviador.SendAccountEmail(t.Context(), "tarde@exemplo.com", "a", "b", account.EntregaDeEmail{}); !errors.Is(err, account.ErrEmailSuprimido) {
		t.Fatalf("fila encerrada: %v, queria ErrEmailSuprimido", err)
	}

	log := c.log.String()
	if !strings.Contains(log, "motivo=encerrado") {
		t.Errorf("log sem motivo=encerrado: %s", log)
	}
	if strings.Contains(log, "tarde@exemplo.com") {
		t.Errorf("o log traz o endereço: %s", log)
	}
}

func TestFalhaDoTransporteOmiteDadosDaMensagem(t *testing.T) {
	c := novoCenario(true, true)
	c.transporte.erro = errors.New("pessoa@exemplo.test https://painel.exemplo.test/#token=segredo-sintetico assunto-sintetico corpo-sintetico")
	parar := c.rodar(t)
	if err := c.enviador.SendAccountEmail(t.Context(), "pessoa@exemplo.test", "assunto-sintetico", "corpo-sintetico", account.EntregaDeEmail{}); err != nil {
		t.Fatal(err)
	}
	parar()

	registro := c.log.String()
	if !strings.Contains(registro, "e-mail de conta falhou") {
		t.Fatal("falha do transporte ausente no log")
	}
	for _, dado := range []string{"pessoa@exemplo.test", "https://painel.exemplo.test", "segredo-sintetico", "assunto-sintetico", "corpo-sintetico"} {
		if strings.Contains(registro, dado) {
			t.Error("o log expôs dados da mensagem")
		}
	}
}

func TestRepeteConclusaoSemReenviarMensagem(t *testing.T) {
	c := novoCenario(true, true)
	parar := c.rodar(t)
	var resultados []account.ResultadoDoEnvio
	concluir := func(ctx context.Context, resultado account.ResultadoDoEnvio) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		resultados = append(resultados, resultado)
		if len(resultados) == 1 {
			return errors.New("resposta do commit perdida")
		}
		return nil
	}
	if err := c.enviador.SendAccountEmail(t.Context(), "pessoa@exemplo.test", "Assunto", "Corpo", account.EntregaDeEmail{Concluir: concluir}); err != nil {
		t.Fatal(err)
	}
	parar()
	if n := c.transporte.total(); n != 1 {
		t.Errorf("transporte recebeu %d envios, queria um", n)
	}
	if len(resultados) != 2 || resultados[0] != account.AceitoPeloTransporte || resultados[1] != account.AceitoPeloTransporte {
		t.Errorf("conclusões = %v, queria duas conclusões de aceite", resultados)
	}
}

func TestFilaCheiaDelegaConclusaoAoTrabalhador(t *testing.T) {
	c := novoCenario(true, true)
	for range 100 {
		enfileirar(t, c.enviador, "destino@exemplo.test")
	}
	resultados := make(chan account.ResultadoDoEnvio, 1)
	concluir := func(_ context.Context, resultado account.ResultadoDoEnvio) error {
		resultados <- resultado
		return nil
	}
	err := c.enviador.SendAccountEmail(t.Context(), "suprimido@exemplo.test", "Assunto", "Corpo", account.EntregaDeEmail{Concluir: concluir})
	if !errors.Is(err, account.ErrEmailSuprimido) {
		t.Fatalf("fila cheia: %v, queria ErrEmailSuprimido", err)
	}
	if len(resultados) != 0 {
		t.Fatal("a submissão executou a conclusão sem o trabalhador")
	}
	ctx, parar := context.WithCancel(t.Context())
	parar()
	c.enviador.Rodar(ctx)
	if len(resultados) != 1 || <-resultados != account.EnvioDescartado {
		t.Fatal("o trabalhador deveria concluir o descarte da mensagem recusada")
	}
	if n := c.transporte.total(); n != 100 {
		t.Errorf("transporte recebeu %d mensagens, queria as 100 aceitas pela fila", n)
	}
}
