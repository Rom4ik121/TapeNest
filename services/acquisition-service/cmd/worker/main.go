// Command worker drives acquisitions: bootstrap wiring (qBittorrent, Lidarr,
// Prowlarr), search → grab → wanted-file-first download → import → catalog
// refresh, and torrent cleanup / disk quota.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/app"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/config"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/musicclient"
)

func main() {
	if err := run(); err != nil {
		slog.Error("acquisition-worker stopped", "err", err)
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

	var bootstrapped atomic.Bool
	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.WorkerPort), Handler: health(infra, cfg, &bootstrapped), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("worker health listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "err", err)
		}
	}()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if !cfg.Enabled() {
		log.Info("acquisition disabled (CONTENT_SOURCES=licensed); idling")
		<-ctx.Done()
		return nil
	}

	notify := core.RedisNotifier{Redis: infra.Redis}
	w := &core.Worker{
		Cfg: core.WorkerConfig{
			Category: cfg.QbtCategory, MusicDir: cfg.MusicDir, LibraryRoot: cfg.LibraryRoot(), TorrentDir: cfg.TorrentDir,
			MaxActive: cfg.MaxActive, MaxReleaseByte: int64(cfg.MaxReleaseGB * (1 << 30)), StallTimeout: cfg.StallTimeout,
			ImportTimeout: cfg.ImportTimeout, TorrentMaxByte: int64(cfg.TorrentMaxGB * (1 << 30)),
		},
		Store: infra.Store, Indexers: infra.Prowlarr, Torrents: infra.QBT, Library: infra.Lidarr,
		Catalog: musicclient.New(cfg.MusicURL, cfg.InternalToken), Log: log,
	}
	bc := core.BootstrapConfig{
		Category: cfg.QbtCategory, TorrentDir: cfg.TorrentDir, LibraryRoot: cfg.LibraryRoot(), MaxActive: cfg.MaxActive,
		SeedRatio: cfg.SeedRatio, SeedMinutes: cfg.SeedMinutes, QbtURL: cfg.QbtURL, QbtUser: cfg.QbtUser, QbtPassword: cfg.QbtPassword,
		LidarrQbtHost: cfg.LidarrQbtHost, LidarrSelfURL: cfg.LidarrSelfURL, ProwlarrSelfURL: cfg.ProwlarrSelfURL, LidarrAPIKey: cfg.LidarrAPIKey,
	}
	if err := os.MkdirAll(cfg.LibraryRoot(), 0o750); err != nil {
		log.Warn("library root", "err", err)
	}
	go func() { // bootstrap until everything is wired (arr apps may start after us)
		for ctx.Err() == nil {
			if !cfg.Bootstrap {
				bootstrapped.Store(true)
				return
			}
			bctx, cancel := context.WithTimeout(ctx, time.Minute)
			profile, err := core.Bootstrap(bctx, bc, infra.QBT, infra.Lidarr, infra.Prowlarr, log)
			cancel()
			if profile > 0 {
				w.SetProfile(profile)
			}
			if err == nil {
				bootstrapped.Store(true)
				log.Info("bootstrap complete", "qualityProfile", profile)
				return
			}
			if !sleep(ctx, 30*time.Second) {
				return
			}
		}
	}()

	wake := infra.Redis.Subscribe(ctx, core.WakeChannel)
	defer func() { _ = wake.Close() }()
	cleanup := func() {
		d := w.Cleanup(ctx)
		if err := notify.StoreDisk(ctx, d); err != nil {
			log.Warn("store disk stats", "err", err)
		}
		log.Info("cleanup", "torrentBytes", d.TorrentBytes, "libraryBytes", d.LibraryBytes)
	}
	cleanup()
	tick := time.NewTicker(cfg.Tick)
	defer tick.Stop()
	clean := time.NewTicker(10 * time.Minute)
	defer clean.Stop()
	for {
		select {
		case <-ctx.Done():
			w.Wait()
			return nil
		case <-tick.C:
		case <-wake.Channel():
		case <-clean.C:
			cleanup()
			continue
		}
		w.Tick(ctx)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func health(infra *app.Infra, cfg *config.Config, boot *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		out := map[string]any{"postgres": "ok", "redis": "ok", "bootstrapped": boot.Load(), "contentSources": cfg.ContentSources}
		status := http.StatusOK
		if infra.Pool.Ping(ctx) != nil {
			out["postgres"], status = "down", http.StatusServiceUnavailable
		}
		if infra.Redis.Ping(ctx).Err() != nil {
			out["redis"], status = "down", http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}
