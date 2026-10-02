// Package app wires shared infrastructure for cmd/server and cmd/worker.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/config"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/storage"
)

// Infra holds connections.
type Infra struct {
	Cfg   *config.Config
	Log   *slog.Logger
	Pool  *pgxpool.Pool
	Redis *redis.Client
	S3    *storage.S3
}

// NewLogger builds the JSON logger.
func NewLogger(level, component string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "download-service", "component", component)
}

// Open connects to PostgreSQL (and migrates when enabled), Redis and MinIO.
func Open(ctx context.Context, cfg *config.Config, log *slog.Logger, migrate bool) (*Infra, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if migrate {
		mctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := repo.Migrate(mctx, pool)
		cancel()
		if err != nil {
			pool.Close()
			return nil, err
		}
		log.Info("migrations applied", "schema", repo.Schema)
	}
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(ropt)
	s3, err := storage.New(storage.Config{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, UseSSL: cfg.S3UseSSL,
		Region: cfg.S3Region, Bucket: cfg.S3Bucket, PublicURL: cfg.S3PublicURL, Retention: cfg.Retention,
		MultipartThreshold: cfg.MultipartThreshold,
	})
	if err != nil {
		pool.Close()
		_ = rdb.Close()
		return nil, err
	}
	return &Infra{Cfg: cfg, Log: log, Pool: pool, Redis: rdb, S3: s3}, nil
}

// Close releases connections.
func (i *Infra) Close() {
	i.Pool.Close()
	_ = i.Redis.Close()
}

// PingPG checks PostgreSQL (readyz).
func (i *Infra) PingPG(ctx context.Context) error { return i.Pool.Ping(ctx) }

// PingRedis checks Redis.
func (i *Infra) PingRedis(ctx context.Context) error { return i.Redis.Ping(ctx).Err() }
