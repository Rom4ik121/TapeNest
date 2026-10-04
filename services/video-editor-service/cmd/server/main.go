// Command server is the video editor API.
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
	"github.com/tapenest/tapenest/services/video-editor-service/internal/transport/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("video-editor stopped", "err", err)
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
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "video-editor")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if cfg.MigrateOnStart {
		mctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := db.Migrate(mctx, pool)
		cancel()
		if err != nil {
			return err
		}
	}
	s3, err := storage.New(storage.Options{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		UseSSL: cfg.S3UseSSL, Region: cfg.S3Region, PublicURL: cfg.PublicURL,
	})
	if err != nil {
		return err
	}
	if err := s3.EnsureBucket(ctx, cfg.EditBucket); err != nil {
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
	h := httpapi.NewRouter(httpapi.Deps{
		API: svc, InternalToken: cfg.InternalToken, Log: log,
		Ready: map[string]httpapi.Pinger{
			"postgres": pool.Ping,
			"minio":    func(ctx context.Context) error { return s3.Ping(ctx, cfg.EditBucket) },
		},
	})
	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port), Handler: h,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("listening", "port", cfg.Port)
	select {
	case <-ctx.Done():
		sh, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sh)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
