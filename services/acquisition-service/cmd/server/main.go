// Command server runs acquisition-service's internal API: unified MusicBrainz
// search, invisible acquire-on-play/like, partial-file streaming and admin status.
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

	"github.com/tapenest/tapenest/services/acquisition-service/internal/app"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/config"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/httpapi"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/streamer"
)

func main() {
	if err := run(); err != nil {
		slog.Error("acquisition-service stopped", "err", err)
		os.Exit(1)
	}
}

type info struct {
	infra  *app.Infra
	notify core.RedisNotifier
}

func (i info) IndexerCount(ctx context.Context) (int, error) {
	idx, err := i.infra.Prowlarr.Indexers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range idx {
		if x.Enable {
			n++
		}
	}
	return n, nil
}

func (i info) Disk(ctx context.Context) (core.DiskStats, error) { return i.notify.Disk(ctx) }

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg.LogLevel, "server")
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	infra, err := app.Open(ctx, cfg, log, cfg.MigrateOnStart)
	if err != nil {
		return err
	}
	defer infra.Close()
	notify := core.RedisNotifier{Redis: infra.Redis}
	svc := &core.Service{
		Enabled: cfg.Enabled(), Store: infra.Store, Meta: infra.MB, Pieces: infra.QBT, Notify: notify,
		Limits: core.Limits{
			UserDaily: cfg.UserDaily, UserActive: cfg.UserActive, RetryAfter: cfg.RetryAfter,
			StreamStartBytes: cfg.StreamStartBytes, LibraryMaxBytes: int64(cfg.LibraryMaxGB * (1 << 30)),
		},
	}
	st := streamer.New(infra.QBT)
	st.Stall = 60 * time.Second
	h := httpapi.NewRouter(httpapi.Deps{
		Service: svc, Admin: infra.Store, Streamer: st, MusicDir: cfg.MusicDir, InternalToken: cfg.InternalToken, Log: log,
		Required: httpapi.Health{
			"postgres": infra.Store.Ping,
			"redis":    func(ctx context.Context) error { return infra.Redis.Ping(ctx).Err() },
		},
		Optional: httpapi.Health{
			"lidarr": infra.Lidarr.Ping, "prowlarr": infra.Prowlarr.Ping,
			"qbittorrent": func(ctx context.Context) error { _, err := infra.QBT.Version(ctx); return err },
		},
		Info: info{infra: infra, notify: notify},
		Limits: httpapi.Limits{
			ContentSources: cfg.ContentSources, TorrentMaxGB: cfg.TorrentMaxGB, LibraryMaxGB: cfg.LibraryMaxGB,
			MaxActive: cfg.MaxActive, UserDaily: cfg.UserDaily, UserActive: cfg.UserActive,
			SeedRatio: cfg.SeedRatio, SeedMinutes: cfg.SeedMinutes, MaxReleaseGB: cfg.MaxReleaseGB,
		},
	})
	// no WriteTimeout: partial-file streams are long-lived
	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 120 * time.Second}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "contentSources", cfg.ContentSources)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
