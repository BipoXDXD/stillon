package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/config"
	"github.com/BipoXDXD/sentinela/internal/httpapi"
	"github.com/BipoXDXD/sentinela/internal/storage"
)

func main() {
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
	log.Info("iniciando", "cfg", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Error("encerrou com erro", "erro", err)
		os.Exit(1)
	}
	log.Info("encerrado")
}

// run é onde o wiring acontece. Mantido separado de main para que main tenha
// apenas responsabilidade de processo (flags, sinais, código de saída).
func run(ctx context.Context, cfg *config.Config) error {
	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	slog.Info("migrations aplicadas")

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if !cfg.Role.RunsAPI() {
		// O papel prober ganha seu loop no Plano 2. Por ora só mantém o
		// processo vivo até o sinal de encerramento.
		<-ctx.Done()
		return nil
	}

	clk := clock.Real{}
	store := auth.NewPGStore(pool)
	sessions := auth.NewManager(store, clk, cfg.IPHashKey)

	srv := httpapi.NewServer(cfg, httpapi.Routes(ctx, cfg, store, sessions, clk))

	errCh := make(chan error, 1)
	go func() {
		slog.Info("servindo HTTP", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Shutdown gracioso com prazo (spec §6.2): conexões em voo terminam, e o
	// processo não fica preso caso alguma não termine.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
