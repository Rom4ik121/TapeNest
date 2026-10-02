// Command server runs the streaming-service HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/streaming-service/internal/app"
	"github.com/tapenest/tapenest/services/streaming-service/internal/catalog"
	"github.com/tapenest/tapenest/services/streaming-service/internal/config"
	"github.com/tapenest/tapenest/services/streaming-service/internal/preview"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
	"github.com/tapenest/tapenest/services/streaming-service/internal/service"
	"github.com/tapenest/tapenest/services/streaming-service/internal/sign"
	"github.com/tapenest/tapenest/services/streaming-service/internal/torr"
	"github.com/tapenest/tapenest/services/streaming-service/internal/transport/httpapi"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()
	if err := run(*migrateOnly); err != nil {
		slog.Error("streaming-service stopped", "err", err)
		os.Exit(1)
	}
}

func run(migrateOnly bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg.LogLevel, "api")
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	infra, err := app.Open(ctx, cfg, log, migrateOnly || cfg.MigrateOnStart)
	if err != nil {
		return err
	}
	defer infra.Close()
	if migrateOnly {
		return nil
	}
	store := repo.New(infra.Pool)
	if err := store.Seed(ctx, catalog.Build()); err != nil {
		return err
	}
	log.Info("catalog seeded", "titles", len(catalog.Build()))
	prev := preview.New(cfg.PreviewDir)
	if err := prev.Ensure(ctx); err != nil {
		log.Warn("licensed preview unavailable", "err", err)
	}
	ts, err := torr.New(cfg.TorrServerURL)
	if err != nil {
		return err
	}
	signer := sign.New(cfg.SigningKey, cfg.StreamTTL)
	cinema := &service.Cinema{Store: store, Sign: signer, Warmup: cfg.Warmup, P2P: cfg.P2P(), Torr: ts}
	handler := httpapi.NewRouter(httpapi.Deps{
		Log: log, Cinema: cinema, Store: store, Sign: signer, Preview: prev,
		InternalToken: cfg.InternalToken,
		Ready:         map[string]httpapi.Pinger{"postgres": infra.Ping},
	})
	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port), Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "sources", cfg.ContentSources, "p2p", cfg.P2P())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
