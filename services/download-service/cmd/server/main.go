// Command server runs the download-service HTTP API (behind api-gateway).
// `server -migrate` applies migrations and exits.
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
	"strconv"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/download-service/internal/app"
	"github.com/tapenest/tapenest/services/download-service/internal/config"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/service"
	"github.com/tapenest/tapenest/services/download-service/internal/transport/httpapi"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()
	if err := run(*migrateOnly); err != nil {
		slog.Error("download-service stopped", "err", err)
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
	bctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err = infra.S3.EnsureBucket(bctx)
	cancel()
	if err != nil {
		return fmt.Errorf("s3 bucket: %w", err)
	}
	queue, err := mq.NewQueue(ctx, infra.Redis, "api")
	if err != nil {
		return err
	}
	bus := mq.NewBus(infra.Redis)
	api := service.NewAPI(service.APIDeps{
		Store: repo.NewStore(infra.Pool), Queue: queue, Bus: bus, Files: infra.S3, Guard: netguard.New(),
		Quotas: service.Quotas{Active: cfg.QuotaActive, Daily: cfg.QuotaDaily}, PresignTTL: cfg.PresignTTL, Log: log,
	})
	handler := httpapi.NewRouter(httpapi.Deps{
		API: api, Bus: bus, InternalToken: cfg.InternalToken, Log: log,
		Ready: map[string]httpapi.Pinger{"postgres": infra.PingPG, "redis": infra.PingRedis, "minio": infra.S3.Ping},
	})
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
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
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
