// Command worker renders queued video exports.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/config"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/download"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/ffmpeg"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/service"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("video-editor worker stopped", "err", err)
		os.Exit(1)
	}
}

type bins struct{ ff, fp string }

func (b bins) Probe(ctx context.Context, path string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.Probe(ctx, b.fp, path)
}
func (b bins) Run(ctx context.Context, steps []ffmpeg.Step) error {
	return ffmpeg.Run(ctx, b.ff, steps)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(cfg.LogLevel))); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "video-editor", "component", "worker")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	s3, err := storage.New(storage.Options{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		UseSSL: cfg.S3UseSSL, Region: cfg.S3Region, PublicURL: cfg.PublicURL,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o750); err != nil {
		return err
	}
	svc := &service.Service{
		Store: db.New(pool), Sources: download.New(cfg.DownloadURL, cfg.InternalToken), Blobs: s3,
		Editor: bins{ff: cfg.FFmpegBin, fp: cfg.FFprobeBin}, MediaBucket: cfg.MediaBucket, EditBucket: cfg.EditBucket,
		WorkDir: cfg.WorkDir, Font: cfg.FontFile, MaxBytes: cfg.MaxBytes, PresignTTL: cfg.PresignTTL,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.WorkerPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ListenAndServe() }()
	defer func() {
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	log.Info("worker polling", "health", cfg.WorkerPort)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		ok, err := svc.ProcessOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("export", "err", err)
		}
		if ok {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
