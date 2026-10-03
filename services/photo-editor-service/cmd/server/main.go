// Command server is the photo editor API.
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

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/config"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/service"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/storage"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/transport/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("photo-editor stopped", "err", err)
		os.Exit(1)
	}
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
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "photo-editor")
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
	if err := s3.EnsureBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	svc := &service.Service{
		Store: db.New(pool), Blobs: s3, Bucket: cfg.Bucket, MaxBytes: cfg.MaxBytes, PresignTTL: cfg.PresignTTL,
	}
	h := httpapi.NewRouter(httpapi.Deps{
		API: svc, InternalToken: cfg.InternalToken, MaxBytes: cfg.MaxBytes, Log: log,
		Ready: map[string]httpapi.Pinger{
			"postgres": pool.Ping,
			"minio":    func(ctx context.Context) error { return s3.Ping(ctx, cfg.Bucket) },
		},
	})
	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port), Handler: h,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
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
