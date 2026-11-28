package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/contaemail"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/exportacao"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
	"github.com/BipoXDXD/stillon/internal/tenancy/convitemail"
	"github.com/BipoXDXD/stillon/internal/tenancy/pgstore"
	"github.com/BipoXDXD/stillon/internal/turnstile"
)

// totpIssuer é o nome que aparece no app autenticador do usuário.
const totpIssuer = "StillOn"

// subcomandos são os modos do binário que não sobem o servidor. O primeiro
// argumento escolhe um deles; sem subcomando, o processo sobe o servidor
// (--role).
var subcomandos = map[string]func(ctx context.Context, args []string) int{
	"user": func(ctx context.Context, args []string) int {
		return runUser(ctx, args, stdinDoProcesso(), os.Stdout, os.Stderr, os.Getenv)
	},
	"healthcheck": func(ctx context.Context, args []string) int {
		return runHealthcheck(ctx, args, os.Stderr, os.Getenv)
	},
	"backup-snapshot": func(ctx context.Context, args []string) int {
		return runBackupSnapshot(ctx, args, os.Stdout, os.Stderr, os.Getenv)
	},
	"version": func(_ context.Context, args []string) int {
		return runVersion(args, os.Stdout, os.Stderr)
	},
}

func main() {
	if len(os.Args) > 1 {
		if sub, ok := subcomandos[os.Args[1]]; ok {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			codigo := sub(ctx, os.Args[2:])
			stop()
			os.Exit(codigo)
		}
	}

	role, codigo, seguir := lerRole(os.Args[1:], os.Stderr)
	if !seguir {
		os.Exit(codigo)
	}

	getenv := os.Getenv
	if role != "" {
		getenv = func(key string) string {
			if key == "ROLE" {
				return role
			}
			return os.Getenv(key)
		}
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		// Antes de a config existir não há logger configurado; stderr basta.
		slog.Error("configuração inválida", "erro", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("iniciando", "versao", version, "cfg", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Error("encerrou com erro", "erro", err)
		os.Exit(1)
	}
	log.Info("encerrado")
}

// usoDoServidor lista o que o binário aceita; sai quando o argumento não é
// subcomando nem flag do servidor.
const usoDoServidor = "uso: stillon [user|healthcheck|backup-snapshot|version] ou stillon --role=all|api|prober"

// lerRole lê as flags do modo servidor. seguir é falso quando o processo deve
// sair com codigo sem subir o servidor: -h (0), flag desconhecida ou um
// argumento solto (saidaUso). Sem esta checagem, um subcomando digitado errado
// (`stillon migarte`) subia o servidor.
func lerRole(args []string, stderr io.Writer) (role string, codigo int, seguir bool) {
	fs := flag.NewFlagSet("stillon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	roleFlag := fs.String("role", "", "all | api | prober (sobrepõe a env ROLE)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", saidaOK, false
		}
		return "", saidaUso, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "argumento inesperado: %q\n%s\n", fs.Arg(0), usoDoServidor)
		return "", saidaUso, false
	}
	return *roleFlag, saidaOK, true
}

// manutencaoACada é o intervalo do job de manutenção (spec §12): partições,
// rollups horários e retenção. Cada rodada é idempotente, e 15 minutos deixam
// o rollup da última hora cheia disponível logo depois de ela fechar.
const manutencaoACada = 15 * time.Minute

// avisoDeBloqueioACada é o intervalo da fila de avisos de bloqueio do segundo
// fator: o bloqueio mais curto dura 15 min, então um minuto de atraso no
// e-mail não muda o que o dono pode fazer.
const avisoDeBloqueioACada = time.Minute

// run é onde o wiring acontece. Mantido separado de main para que main tenha
// apenas responsabilidade de processo (flags, sinais, código de saída).
//
// Cada subsistema roda no mesmo errgroup. Se um falha — a porta ocupada, por
// exemplo —, o contexto do grupo cancela, os outros encerram, e o processo
// sai com erro, em vez de seguir meio vivo.
func run(ctx context.Context, cfg *config.Config) error {
	pool, err := storage.NewPoolNamed(ctx, cfg.DatabaseURL, "stillon-"+string(cfg.Role))
	if err != nil {
		return err
	}
	defer pool.Close()

	// Com um papel que ignora RLS, a segunda barreira de isolamento estaria
	// desligada em silêncio. A checagem vem ANTES da migration: CheckRole
	// só lê pg_roles, e migrar primeiro faria o boot recusado criar todas as
	// tabelas com o dono errado, que o papel comum depois não conseguiria usar.
	if err := storage.CheckRole(ctx, pool); err != nil {
		return err
	}

	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations aplicadas")

	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	// Os dois papéis decifram destinos de canal, então os dois conferem a
	// chave antes de servir qualquer coisa.
	if err := notify.CheckMasterKey(ctx, pool, box); err != nil {
		return err
	}
	clk := clock.Real{}

	// A partição do mês corrente tem de existir antes da primeira sonda.
	// Fica antes do errgroup: um retorno daqui não deixa servidor nenhum
	// rodando sem ninguém para desligá-lo.
	if cfg.Role.RunsProber() {
		if err := storage.EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil {
			return err
		}
	}

	reg := observability.NewRegistry()
	reg.MustRegister(storage.NewPoolCollector(pool))

	g, gctx := errgroup.WithContext(ctx)

	if cfg.MetricsAddr != "" {
		servir(gctx, g, observability.NewMetricsServer(cfg.MetricsAddr, reg), "métricas", cfg.ShutdownGrace)
	}

	if cfg.Role.RunsProber() {
		sched := prober.New(pool, box, clk, prober.NewMetrics(reg), cfg.ProbeBlockedCIDRs)
		grace := graceDoSondador(cfg.ShutdownGrace)
		g.Go(func() error { return sched.Run(gctx, grace) })
		g.Go(func() error {
			return storage.RunMaintenance(gctx, pool, clk, manutencaoACada)
		})
		// O dispatcher roda com o sondador (spec §8: fora do caminho
		// crítico da sonda, em worker próprio). A outbox no banco é o que
		// deixa --role=api e --role=prober separados sem perder nada.
		disp := notify.NewDispatcher(pool, box, clk, reg, notificadores(cfg)...)
		g.Go(func() error { return disp.Run(gctx) })
		// Os lembretes de incidente aberto entram na mesma outbox, e o
		// dispatcher os entrega como qualquer outro aviso.
		lembretes := notify.NewReminders(pool, clk, notify.DefaultPolicy())
		g.Go(func() error { return lembretes.Run(gctx) })
		// Quem abre e fecha incidente de heartbeat é este papel, e não quem
		// recebe o ping: com --role=api e --role=prober separados, o ping só
		// registra.
		avaliador := prober.NewHeartbeatEvaluator(pool, clk)
		reg.MustRegister(avaliador.Collector())
		g.Go(func() error { return avaliador.Run(gctx) })
		slog.Info("sondador ativo", "max_em_voo", prober.MaxInFlight, "email", cfg.EmailEnabled())
	}

	if cfg.Role.RunsAPI() {
		store := auth.NewPGStore(pool)
		segundoFator := auth.NewTOTP(pool, box, totpIssuer)
		provas := auth.NewFamilyProofs(pool, clk)
		reg.MustRegister(provas.Collector())
		contas := account.NewService(pool, segundoFator, clk, cfg.IPHashKey)
		limitesIP := storage.NewOrcamentosPorIP(pool, clk)
		// Os e-mails de conta (confirmação, redefinição, verificação de
		// canal) passam pelo balde do destinatário e pelo teto diário; os
		// avisos (notificadores) e o do 2FA bloqueado, nunca.
		// O teto da conta que causou o e-mail vale junto com o global.
		teto := storage.NewTetoDeEmailsDeConta(pool, clk, contaemail.TetoDiario, contaemail.TetoDiarioPorConta)
		enviador := enviadorDeConta(cfg, teto, limitesIP)
		links := account.Links{Base: cfg.PublicBaseURL}
		deps := httpapi.Deps{
			Store:    store,
			Sessions: auth.NewManager(store, clk, cfg.IPHashKey),
			TOTP:     segundoFator,
			Accounts: contas,
			// A trava de criação confere a conta do token a cada pedido.
			EmailConfirmado: contas,
			Proofs:          provas,
			Targets:         targets.NewService(pool, box, clk, targets.ComTetoDeRegrasPorHost(cfg.ContentRulesPerHost)),
			Incidents:       history.NewService(pool, clk),
			Channels:        notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg, enviador), cfg.PublicBaseURL),
			StatusPages:     statuspage.NewService(pool, clk),
			Pings:           heartbeat.NewPingService(pool, clk),
			Heartbeats:      heartbeat.NewService(pool, clk),
			Projects:        tenancy.NewService(pgstore.New(pool, pgstore.ComTetoDeRegrasPorHost(cfg.ContentRulesPerHost)), clk),
			// O e-mail do convite sai pelo mesmo enviador dos e-mails de conta
			// (balde do destinatário e teto diário); sem envio, SemEnvio.
			Invitations: tenancy.NewInvitations(pgstore.NewInvitations(pool),
				convitemail.New(pool, clk, mailerDeConta(enviador), links), clk),
			Clock:       clk,
			LimitesIP:   limitesIP,
			Exportacao:  exportacao.Novo(pool, box, clk),
			Ready:       pool.Ping,
			Cadastro:    account.NewCadastro(pool, clk, mailerDeConta(enviador), links),
			Recuperacao: account.NewRecuperacao(pool, clk, mailerDeConta(enviador), links),
			Turnstile:   novoTurnstile(crypto.Secret(cfg.TurnstileSecretKey), cfg.HostDoTurnstile()),
			// Desligado (sem o par do Turnstile ou sem e-mail), signup e
			// forgot dão 404; a confirmação e a redefinição seguem no ar.
			CadastroPublico: httpapi.ConfigCadastro{Habilitado: cfg.SignupEnabled(), SiteKey: cfg.TurnstileSiteKey},
		}
		apiParou := servir(gctx, g, httpapi.NewServer(cfg, httpapi.Routes(gctx, cfg, deps)), "api", cfg.ShutdownGrace)
		if enviador != nil {
			rodarFilaDeConta(gctx, g, enviador, apiParou, cfg.ShutdownGrace)
		}
		slog.Info("cadastro público", "ligado", cfg.SignupEnabled())
		// O aviso do bloqueio do segundo fator sai deste papel, que é quem
		// lê users. Sem o Resend, os avisos ficam na fila (totp_lock_alert_at)
		// até o e-mail ser ligado.
		if cfg.EmailEnabled() {
			mailer := novoResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)
			g.Go(func() error { return auth.RunTOTPLockAlerts(gctx, store, mailer, avisoDeBloqueioACada) })
		} else {
			slog.Warn("e-mail desligado: avisos de bloqueio do segundo fator ficam na fila")
		}
	}

	return g.Wait()
}

// novoResend e novoTurnstile montam os clientes dos serviços externos, de
// endpoint fixo. São variáveis só para os testes do pacote rodarem o processo
// inteiro com imitações em httptest; nada em produção as troca.
var (
	novoResend    = notify.NewResend
	novoTurnstile = turnstile.New
)

// notificadores são os canais que o dispatcher sabe usar. O e-mail só
// entra com o Resend configurado; sem ele, uma notificação de e-mail falha
// de vez e vai para a auditoria.
func notificadores(cfg *config.Config) []notify.Notifier {
	ns := []notify.Notifier{notify.NewDiscord()}
	if cfg.EmailEnabled() {
		ns = append(ns, novoResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom))
	}
	return ns
}

// enviadorDeConta monta a fila dos e-mails de conta, que sai pelo remetente
// de conta (ACCOUNT_EMAIL_FROM, ou o EMAIL_FROM). Nil sem e-mail.
func enviadorDeConta(cfg *config.Config, teto contaemail.Teto, orcamentos contaemail.Orcamentos) *contaemail.Enviador {
	if !cfg.EmailEnabled() {
		return nil
	}
	transporte := novoResend(crypto.Secret(cfg.ResendAPIKey), cfg.RemetenteDeConta())
	return contaemail.New(transporte, teto, orcamentos, cfg.IPHashKey, slog.Default())
}

// mailerDeConta é a porta dos e-mails de conta dos serviços: o enviador ou,
// sem e-mail, o SemEnvio, que só registra no log. Os serviços nunca
// devolvem erro do envio.
func mailerDeConta(e *contaemail.Enviador) account.AccountMailer {
	if e == nil {
		return contaemail.SemEnvio{Log: slog.Default()}
	}
	return e
}

// verificador é o envio do link de verificação do canal de e-mail: e-mail
// de conta, com balde, teto e remetente de conta. Devolve a interface nil, e
// não um ponteiro nil, quando o e-mail está desligado: um ponteiro nil numa
// interface não é nil, e o cadastro do canal acharia que há e-mail.
func verificador(cfg *config.Config, e *contaemail.Enviador) notify.VerificationMailer {
	if e == nil {
		return nil
	}
	return contaemail.NovaVerificacaoComTeto(e, novoResend(crypto.Secret(cfg.ResendAPIKey), cfg.RemetenteDeConta()))
}

// rodarFilaDeConta roda a fila dos e-mails de conta no grupo e a encerra
// depois da API: um pedido respondido no fim do encerramento ainda tem o
// e-mail drenado. Se a API demorar, a fila encerra assim mesmo a tempo de o
// dreno (contaemail.PrazoDreno) caber no grace contado do sinal.
func rodarFilaDeConta(ctx context.Context, g *errgroup.Group, fila interface{ Rodar(context.Context) }, apiParou <-chan struct{}, grace time.Duration) {
	filaCtx, parar := context.WithCancel(context.WithoutCancel(ctx))
	g.Go(func() error {
		defer parar()
		feito := make(chan struct{})
		go func() { defer close(feito); fila.Rodar(filaCtx) }()
		select {
		case <-ctx.Done():
		case <-feito:
			return nil
		}
		folga := grace - contaemail.PrazoDreno
		espera := time.NewTimer(max(folga, 0))
		defer espera.Stop()
		select {
		case <-apiParou:
		case <-espera.C:
			// Com grace ≤ PrazoDreno (só em teste), não há folga a esperar.
			if folga > 0 {
				slog.Warn("a API não parou a tempo; a fila dos e-mails de conta encerra assim mesmo")
			}
		}
		parar()
		<-feito
		return nil
	})
}

// graceDoSondador é o prazo que o sondador dá às sondas em voo no shutdown.
//
// Stop pode levar até grace + PersistTimeout: uma sonda que termina no limite
// ainda grava o check com prazo próprio. Descontar PersistTimeout faz o
// sondador inteiro caber no ShutdownGrace (15s, spec §6.2). Quando não cabe
// (grace ≤ PersistTimeout, só em teste), vai o grace inteiro: um prazo zero
// ou negativo abortaria as sondas na hora.
func graceDoSondador(total time.Duration) time.Duration {
	if g := total - prober.PersistTimeout; g > 0 {
		return g
	}
	return total
}

// erroDeServidor é a falha de um servidor ao subir. A porta de métricas
// ocupada ganha a saída na mensagem: o caso comum é outro
// processo do StillOn no mesmo host (--role=api e --role=prober), e quem lê
// o log precisa saber qual variável mudar.
func erroDeServidor(nome string, err error) error {
	if nome == "métricas" && errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("servidor %s: %w; a porta já está em uso (outro processo do StillOn no mesmo host?): "+
			"defina METRICS_ADDR com outra porta de loopback, como 127.0.0.1:9092, ou METRICS_ADDR=off", nome, err)
	}
	return fmt.Errorf("servidor %s: %w", nome, err)
}

// servir roda srv até ctx acabar e então o desliga com prazo (spec §6.2).
// Uma falha ao subir volta como erro do grupo e derruba o resto. Pedido que
// passa do prazo não faz o encerramento falhar: as conexões que sobraram são
// cortadas e o processo sai normalmente. O canal devolvido fecha quando o
// servidor terminou (de encerrar ou de falhar).
func servir(ctx context.Context, g *errgroup.Group, srv *http.Server, nome string, grace time.Duration) <-chan struct{} {
	encerrou := make(chan struct{})
	g.Go(func() error {
		defer close(encerrou)
		falhou := make(chan error, 1)
		go func() {
			slog.Info("servindo", "servidor", nome, "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				falhou <- erroDeServidor(nome, err)
			}
		}()
		select {
		case err := <-falhou:
			return err
		case <-ctx.Done():
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("prazo de encerramento estourou; conexões cortadas", "servidor", nome, "grace", grace)
			return srv.Close()
		}
		return err
	})
	return encerrou
}
