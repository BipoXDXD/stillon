package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/incident/history"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/targets"
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

	role := flag.String("role", "", "all | api | prober (sobrepõe a env ROLE)")
	flag.Parse()

	getenv := os.Getenv
	if *role != "" {
		getenv = func(key string) string {
			if key == "ROLE" {
				return *role
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

// manutencaoACada é o intervalo do job de manutenção (spec §12): partições,
// rollups horários e retenção. Cada rodada é idempotente, e 15 minutos deixam
// o rollup da última hora cheia disponível logo depois de ela fechar.
const manutencaoACada = 15 * time.Minute

// run é onde o wiring acontece. Mantido separado de main para que main tenha
// apenas responsabilidade de processo (flags, sinais, código de saída).
//
// Cada subsistema roda no mesmo errgroup. Se um falha — a porta ocupada, por
// exemplo —, o contexto do grupo cancela, os outros encerram, e o processo
// sai com erro, em vez de seguir meio vivo.
func run(ctx context.Context, cfg *config.Config) error {
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Com um papel que ignora RLS, a segunda barreira de isolamento estaria
	// desligada em silêncio (D8). A checagem vem ANTES da migration: CheckRole
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
		sched := prober.New(pool, box, clk, prober.NewMetrics(reg))
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
		slog.Info("sondador ativo", "max_em_voo", prober.MaxInFlight, "email", cfg.EmailEnabled())
	}

	if cfg.Role.RunsAPI() {
		store := auth.NewPGStore(pool)
		segundoFator := auth.NewTOTP(pool, box, totpIssuer)
		deps := httpapi.Deps{
			Store:       store,
			Sessions:    auth.NewManager(store, clk, cfg.IPHashKey),
			TOTP:        segundoFator,
			Accounts:    account.NewService(pool, segundoFator, clk, cfg.IPHashKey),
			Targets:     targets.NewService(pool, box, clk),
			Incidents:   history.NewService(pool, clk),
			Channels:    notify.NewChannelService(pool, box, clk, notify.NewDiscord(), verificador(cfg), cfg.PublicBaseURL),
			StatusPages: statuspage.NewService(pool, clk),
			Clock:       clk,
			Ready:       pool.Ping,
		}
		servir(gctx, g, httpapi.NewServer(cfg, httpapi.Routes(gctx, cfg, deps)), "api", cfg.ShutdownGrace)
	}

	return g.Wait()
}

// notificadores são os canais que o dispatcher sabe usar. O e-mail só
// entra com o Resend configurado; sem ele, uma notificação de e-mail falha
// de vez e vai para a auditoria.
func notificadores(cfg *config.Config) []notify.Notifier {
	ns := []notify.Notifier{notify.NewDiscord()}
	if cfg.EmailEnabled() {
		ns = append(ns, notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom))
	}
	return ns
}

// verificador devolve a interface nil, e não um *Resend nil, quando o
// e-mail está desligado: um *Resend nil numa interface não é nil, e o
// cadastro acharia que o e-mail está disponível.
func verificador(cfg *config.Config) notify.VerificationMailer {
	if !cfg.EmailEnabled() {
		return nil
	}
	return notify.NewResend(crypto.Secret(cfg.ResendAPIKey), cfg.EmailFrom)
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
// ocupada ganha a saída na mensagem (M7, Plano 9 §3.4): o caso comum é outro
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
// Uma falha ao subir volta como erro do grupo e derruba o resto.
func servir(ctx context.Context, g *errgroup.Group, srv *http.Server, nome string, grace time.Duration) {
	g.Go(func() error {
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
		return srv.Shutdown(shutdownCtx)
	})
}
