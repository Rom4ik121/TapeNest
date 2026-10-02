// Command worker runs reco-service's background jobs: event ingestion (Redis
// streams → long-term profile), one-off backfill, catalog copy + audio analysis
// (ffmpeg + Go DSP) and periodic training (co-occurrence, ALS, content kNN).
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/reco-service/internal/app"
	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/config"
	"github.com/tapenest/tapenest/services/reco-service/internal/ingest"
	"github.com/tapenest/tapenest/services/reco-service/internal/jobs"
	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reco-worker stopped", "err", err)
		os.Exit(1)
	}
}

type status struct {
	ingested   atomic.Int64
	lastTrain  atomic.Value // jobs.TrainResult
	lastSync   atomic.Int64
	analyzed   atomic.Int64
	backfilled atomic.Int64
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
	if err := ingest.EnsureGroups(ctx, infra.Redis); err != nil {
		return err
	}
	host, _ := os.Hostname()
	mc := musicclient.New(cfg.MusicURL, cfg.InternalToken)
	j := &jobs.Jobs{Music: mc, Store: infra.Store, Decoder: audio.FFmpeg{Bin: cfg.FFmpegBin}, Workers: cfg.AnalyzeWorkers, Log: log}
	st := &status{}
	cons := &ingest.Consumer{
		RDB: infra.Redis, Store: infra.Store, Snapshot: j.Snapshot, Name: fmt.Sprintf("%s-%d", host, os.Getpid()),
		Log: log, Applied: func(n int) { st.ingested.Add(int64(n)) },
	}

	train := func(ctx context.Context) error {
		res, err := j.Train(ctx)
		if err != nil {
			return err
		}
		st.lastTrain.Store(res)
		log.Info("model trained", "version", res.Version, "tracks", res.Tracks, "users", res.Users, "interactions", res.Interactions,
			"withFeatures", res.WithFeatures, "cfItems", res.CFItems, "factorItems", res.FactorItems, "took", res.Took.String())
		return nil
	}
	catalog := func(ctx context.Context) error {
		seen, deleted, err := j.SyncCatalog(ctx)
		if err != nil {
			return err
		}
		st.lastSync.Store(time.Now().Unix())
		log.Info("catalog synced", "tracks", seen, "deleted", deleted)
		total := 0
		for ctx.Err() == nil {
			n, err := j.AnalyzePending(ctx, 20)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			total += n
			st.analyzed.Add(int64(n))
			log.Info("audio analysed", "batch", n, "total", total)
		}
		if total > 0 || deleted > 0 || j.Snapshot() == nil {
			return train(ctx)
		}
		return nil
	}

	var wg sync.WaitGroup
	start := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }
	start(func() {
		// backfill first (idempotent, once), then keep consuming the streams
		n, err := ingest.Backfill(ctx, cons, mc, cfg.BackfillDays)
		if err != nil {
			log.Warn("backfill failed (will retry on next start)", "err", err)
		} else if n > 0 {
			st.backfilled.Store(int64(n))
			log.Info("backfill done", "signals", n)
		}
		cons.Run(ctx)
	})
	var catalogMu sync.Mutex
	catalogOnce := func(ctx context.Context) error {
		catalogMu.Lock()
		defer catalogMu.Unlock()
		return catalog(ctx)
	}
	start(func() { loop(ctx, "catalog+analysis", cfg.CatalogInterval, log, catalogOnce) })
	// music-service announces catalog changes (newly acquired tracks, ADR 0011):
	// copy + analyse them right away instead of waiting for the next interval
	start(func() {
		sub := infra.Redis.Subscribe(ctx, "music:catalog_updated")
		defer func() { _ = sub.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-sub.Channel():
				if !ok {
					return
				}
				sleepCtx(ctx, 2*time.Second) // coalesce bursts
				log.Info("catalog update announced, syncing now")
				if err := catalogOnce(ctx); err != nil && ctx.Err() == nil {
					log.Warn("on-demand catalog sync failed", "err", err)
				}
			}
		}
	})
	start(func() {
		sleepCtx(ctx, time.Minute) // let the first catalog pass train first
		loop(ctx, "training", cfg.TrainInterval, log, train)
	})
	start(func() {
		loop(ctx, "ingested cleanup", 24*time.Hour, log, func(ctx context.Context) error {
			_, err := infra.Store.CleanupIngested(ctx, time.Now().Add(-cfg.IngestedRetention))
			return err
		})
	})

	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.WorkerPort), Handler: health(infra, st), ReadHeaderTimeout: 5 * time.Second}
	start(func() {
		log.Info("worker health listening", "addr", srv.Addr)
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

func loop(ctx context.Context, name string, every time.Duration, log *slog.Logger, fn func(context.Context) error) {
	for {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			log.Warn(name+" failed", "err", err)
		}
		if !sleepCtx(ctx, every) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func health(infra *app.Infra, st *status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		out := map[string]any{
			"postgres": "ok", "redis": "ok", "ingested": st.ingested.Load(), "analyzed": st.analyzed.Load(),
			"backfilled": st.backfilled.Load(),
		}
		status := http.StatusOK
		if infra.Pool.Ping(ctx) != nil {
			out["postgres"], status = "down", http.StatusServiceUnavailable
		}
		if infra.Redis.Ping(ctx).Err() != nil {
			out["redis"], status = "down", http.StatusServiceUnavailable
		}
		if t, ok := st.lastTrain.Load().(jobs.TrainResult); ok {
			out["model"] = map[string]any{
				"version": t.Version, "tracks": t.Tracks, "users": t.Users, "interactions": t.Interactions,
				"withFeatures": t.WithFeatures, "cfItems": t.CFItems, "factorItems": t.FactorItems,
			}
		}
		if a, f, n, err := infra.Store.FeatureStats(ctx, audio.Version); err == nil {
			out["features"] = map[string]int64{"analyzed": a, "failed": f, "tracks": n}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}
