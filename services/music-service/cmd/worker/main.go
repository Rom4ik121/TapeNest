// Command worker runs music-service background jobs: the play_events batcher
// (Redis Streams → PostgreSQL batches), the Navidrome catalog sync and the
// partition/aggregate maintenance (spec §3.1 #8, #11; §5.4).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/music-service/internal/app"
	"github.com/tapenest/tapenest/services/music-service/internal/config"
	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("music-worker stopped", "err", err)
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
	host, _ := os.Hostname()
	consumer := fmt.Sprintf("%s-%d", host, os.Getpid())

	if created, err := infra.Navidrome.EnsureAdmin(ctx); err != nil {
		log.Warn("navidrome admin check failed (will rely on existing users)", "err", err)
	} else if created {
		log.Info("navidrome admin user created", "user", cfg.NavidromeUser)
	}
	maint := &worker.Maintainer{Store: infra.Store, Log: log}
	if err := maint.EnsurePartitions(ctx); err != nil {
		return fmt.Errorf("partitions: %w", err)
	}
	syncer := &worker.Syncer{Lib: infra.Navidrome, Store: infra.Store, Log: log}
	batcher := &worker.Batcher{RDB: infra.Redis, Store: infra.Store, Consumer: consumer, Log: log}

	var wg sync.WaitGroup
	start := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }
	start(func() { infra.Navidrome.RunHealth(ctx, cfg.NavidromePing) })
	start(func() { batcher.Run(ctx) })
	var syncMu sync.Mutex
	syncOnce := func(ctx context.Context) error {
		syncMu.Lock()
		defer syncMu.Unlock()
		res, err := syncer.Sync(ctx)
		if err == nil {
			log.Info("catalog synced", "tracks", res.Seen, "deleted", res.Deleted)
			err = infra.Store.RefreshPopularity(ctx) // new tracks appear in the aggregate at once
		}
		return err
	}
	start(func() {
		worker.Loop(ctx, "catalog sync", cfg.SyncInterval, log, func(ctx context.Context) error {
			if err := infra.Navidrome.StartScan(ctx); err != nil {
				log.Warn("navidrome scan request failed", "err", err)
			}
			return syncOnce(ctx)
		})
	})
	// on-demand Navidrome rescan when something publishes music:catalog_refresh
	refresher := &worker.Refresher{
		Scan: infra.Navidrome, Sync: syncOnce, Log: log,
		Publish: func(ctx context.Context) error {
			return infra.Redis.Publish(ctx, mq.CatalogUpdatedChannel, "1").Err()
		},
	}
	sub := infra.Redis.Subscribe(ctx, mq.CatalogRefreshChannel)
	defer func() { _ = sub.Close() }()
	signals := make(chan struct{}, 16)
	start(func() {
		defer close(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-sub.Channel():
				if !ok {
					return
				}
				select {
				case signals <- struct{}{}:
				default:
				}
			}
		}
	})
	start(func() { refresher.Listen(ctx, signals) })
	start(func() {
		worker.Loop(ctx, "popularity refresh", cfg.PopularityInterval, log, infra.Store.RefreshPopularity)
	})
	start(func() { worker.Loop(ctx, "partitions", 6*time.Hour, log, maint.EnsurePartitions) })

	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.WorkerPort), Handler: health(infra), ReadHeaderTimeout: 5 * time.Second}
	start(func() {
		log.Info("worker health listening", "addr", srv.Addr, "consumer", consumer)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "err", err)
		}
	})
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	wg.Wait()
	return nil
}

func health(infra *app.Infra) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		out := map[string]any{"postgres": "ok", "redis": "ok", "navidrome": "ok"}
		status := http.StatusOK
		if infra.PingPG(ctx) != nil {
			out["postgres"], status = "down", http.StatusServiceUnavailable
		}
		if infra.PingRedis(ctx) != nil {
			out["redis"], status = "down", http.StatusServiceUnavailable
		}
		if !infra.Navidrome.Healthy() {
			out["navidrome"] = "degraded"
		}
		if n, err := infra.Redis.XLen(ctx, mq.PlayEventsStream).Result(); err == nil {
			out["playEventsStreamLen"] = n
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}
