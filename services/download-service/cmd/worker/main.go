// Command worker processes download jobs from the Redis Streams queue:
// yt-dlp (two passes) → MinIO → video.downloaded event (spec §5.3).
// `worker -import-cookies <source> <file>` stores a Netscape cookies file in
// Redis (manual stand-in for the Playwright sidecar).
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
	"github.com/tapenest/tapenest/services/download-service/internal/cookies"
	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/ffmpeg"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
	"github.com/tapenest/tapenest/services/download-service/internal/proxy"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/service"
	"github.com/tapenest/tapenest/services/download-service/internal/ytdlp"
)

func editorOrNil(log *slog.Logger, loc string) service.Editor {
	ff, err := ffmpeg.New(loc)
	if err != nil {
		log.Warn("ffmpeg unavailable, frame posters are off", "err", err)
		return nil
	}
	return ff
}

func main() {
	importCookies := flag.Bool("import-cookies", false, "store cookies: -import-cookies <youtube|vk|rutube> <cookies.txt>")
	flag.Parse()
	var err error
	if *importCookies {
		err = runImport(flag.Args())
	} else {
		err = run()
	}
	if err != nil {
		slog.Error("download worker stopped", "err", err)
		os.Exit(1)
	}
}

func runImport(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: worker -import-cookies <source> <file>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(args[1])
	if err != nil {
		return fmt.Errorf("read cookies: %w", err)
	}
	infra, err := app.Open(context.Background(), cfg, app.NewLogger(cfg.LogLevel, "cli"), false)
	if err != nil {
		return err
	}
	defer infra.Close()
	return cookies.New(infra.Redis, cfg.CookiesMaxAge).Put(context.Background(), domain.Source(args[0]), string(body), time.Now())
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
	queue, err := mq.NewQueue(ctx, infra.Redis, consumer)
	if err != nil {
		return err
	}
	bus := mq.NewBus(infra.Redis)
	pools := proxy.New(cfg.ProxyDatacenter, cfg.ProxyResidential, cfg.ProxyMobile)
	proc := service.NewProcessor(service.ProcessorDeps{
		Config: service.ProcessorConfig{
			Limits: cfg.Limits(), MaxFilesize: cfg.MaxFilesize, MaxDuration: cfg.MaxDuration, JobTimeout: cfg.JobTimeout,
			Retention: cfg.Retention, PresignTTL: cfg.PresignTTL, TmpDir: cfg.TmpDir,
		},
		Store: repo.NewStore(infra.Pool), Queue: queue, Bus: bus, Files: infra.S3,
		Fetch:   &ytdlp.Runner{Bin: cfg.YtDlpPath, JSRuntimes: cfg.YtDlpJSRuntimes, FFmpeg: cfg.FFmpegLocation},
		Limiter: mq.NewSemaphores(infra.Redis, cfg.DomainLimits(), cfg.JobTimeout+time.Minute),
		Locker:  mq.NewLocks(infra.Redis), Plans: mq.NewPlans(infra.Redis), Guard: netguard.New(),
		Cookies: cookies.New(infra.Redis, cfg.CookiesMaxAge), Proxies: pools,
		Editor: editorOrNil(log, cfg.FFmpegLocation), Log: log,
	})
	go pools.Run(ctx, cfg.ProxyHealthURL, cfg.ProxyHealthEvery, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if infra.PingRedis(r.Context()) != nil || infra.PingPG(r.Context()) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		depth, _ := queue.Depth(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","queue":{"high":%d,"normal":%d,"low":%d,"delayed":%d}}`, depth["high"], depth["normal"], depth["low"], depth["delayed"])
	})
	srv := &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(cfg.WorkerPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if cfg.AppEnv != "dev" {
		srv.Addr = ":" + strconv.Itoa(cfg.WorkerPort)
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server failed", "err", err)
		}
	}()
	defer func() { _ = srv.Close() }()

	log.Info("worker started", "consumer", consumer, "concurrency", cfg.Concurrency, "proxies", pools.Healthy())
	w := &service.Worker{Queue: queue, Proc: proc, Concurrency: cfg.Concurrency, ReclaimIdle: cfg.JobTimeout + 5*time.Minute, Log: log}
	w.Run(ctx, cfg.DrainTimeout)
	log.Info("worker stopped")
	return nil
}
