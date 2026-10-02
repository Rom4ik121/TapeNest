// Command worker expires idle playback sessions and trims the cache directory.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/streaming-service/internal/app"
	"github.com/tapenest/tapenest/services/streaming-service/internal/config"
	"github.com/tapenest/tapenest/services/streaming-service/internal/disk"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
)

func main() {
	if err := run(); err != nil {
		slog.Error("streaming-worker stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg.LogLevel, "worker")
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	infra, err := app.Open(ctx, cfg, log, cfg.MigrateOnStart)
	if err != nil {
		return err
	}
	defer infra.Close()
	store := repo.New(infra.Pool)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.WorkerPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health", "err", err)
		}
	}()
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(cctx)
	}()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		n, err := store.ExpireSessions(ctx, time.Now().Add(-cfg.SessionTTL))
		if err != nil {
			log.Warn("expire sessions", "err", err)
		} else if n > 0 {
			log.Info("expired sessions", "n", n)
		}
		if removed, err := disk.Evict(cfg.CacheDir, cfg.CacheMaxBytes); err != nil {
			log.Warn("cache evict", "err", err)
		} else if removed > 0 {
			log.Info("cache evict", "removed", removed)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
