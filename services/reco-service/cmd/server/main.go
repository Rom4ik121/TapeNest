// Command server is reco-service's internal API: personalized My Wave batches
// for music-service (ADR 0010). The in-memory model snapshot is reloaded when
// the worker publishes a new model_version.
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

	"github.com/tapenest/tapenest/services/reco-service/internal/app"
	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/config"
	"github.com/tapenest/tapenest/services/reco-service/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reco-service stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate", false, "apply migrations and exit")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg.LogLevel, "api")
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	infra, err := app.Open(ctx, cfg, log, *migrateOnly || cfg.MigrateOnStart)
	if err != nil {
		return err
	}
	defer infra.Close()
	if *migrateOnly {
		return nil
	}
	models := &httpapi.Models{Src: infra.Store, FeatureVersion: audio.Version, Log: log}
	go models.Run(ctx, cfg.ModelPoll)

	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port),
		Handler: httpapi.NewRouter(httpapi.Deps{
			Models: models, Users: infra.Store, Weights: cfg.Weights,
			InternalToken: cfg.InternalToken, Log: log,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
