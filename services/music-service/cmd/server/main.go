// Command server runs the music-service HTTP API (behind api-gateway).
// `server -migrate` applies migrations and exits.
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

	"github.com/tapenest/tapenest/services/music-service/internal/app"
	"github.com/tapenest/tapenest/services/music-service/internal/config"
	"github.com/tapenest/tapenest/services/music-service/internal/reco"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
	"github.com/tapenest/tapenest/services/music-service/internal/transport/httpapi"
	"github.com/tapenest/tapenest/services/music-service/internal/ytdlp"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()
	if err := run(*migrateOnly); err != nil {
		slog.Error("music-service stopped", "err", err)
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
	if created, err := infra.Navidrome.EnsureAdmin(ctx); err != nil {
		log.Warn("navidrome admin check failed (will rely on existing users)", "err", err)
	} else if created {
		log.Info("navidrome admin user created", "user", cfg.NavidromeUser)
	}
	go infra.Navidrome.RunHealth(ctx, cfg.NavidromePing)

	recoClient, err := reco.New(reco.Options{
		BaseURL: cfg.RecoURL, Token: cfg.InternalToken, Timeout: cfg.RecoTimeout,
		FailuresTrip: cfg.RecoBreakerFailures, OpenFor: cfg.RecoBreakerOpen,
	})
	if err != nil {
		return err
	}
	publish := service.UserEventPublisher(infra.Redis, log)
	wave := service.NewWave(infra.Store, infra.Redis).WithEvents(publish, log)
	optional := map[string]httpapi.Pinger{"navidrome": infra.Navidrome.Ping}
	if recoClient != nil {
		wave.WithReco(recoClient)
		optional["reco"] = func(context.Context) error {
			if recoClient.State() == "open" {
				return errors.New("reco breaker open")
			}
			return nil
		}
		log.Info("my wave: reco-service enabled", "timeout", cfg.RecoTimeout.String())
	} else {
		log.Info("my wave: RECO_SERVICE_URL not set, heuristic only")
	}
	streamer := service.NewStreamer(infra.Store, infra.Navidrome, signer.New(cfg.StreamSigningKey, cfg.StreamPublicBase), cfg.StreamURLTTL, log).
		WithCoverArchive(cfg.CoverArtURL)
	library := service.NewLibrary(infra.Store).WithEvents(publish)
	var discovery *service.Discovery
	if cfg.SourceEnabled("youtube") {
		streamer.WithYouTubeAudio(ytdlp.New(cfg.YTDLPBin))
		discovery = service.NewDiscovery(infra.Store, log).WithYouTube(ytm.New())
		log.Info("catalog: youtube music")
	} else {
		log.Info("catalog: local library only")
	}
	handler := httpapi.NewRouter(httpapi.Deps{
		Library:       library,
		Discovery:     discovery,
		Wave:          wave,
		Internal:      service.NewInternal(infra.Store),
		Events:        service.NewEvents(infra.Redis),
		Streamer:      streamer,
		InternalToken: cfg.InternalToken,
		Log:           log,
		Ready:         map[string]httpapi.Pinger{"postgres": infra.PingPG, "redis": infra.PingRedis},
		Optional:      optional,
	})
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// no WriteTimeout: audio responses stream for the length of the track
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
