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

	"github.com/tapenest/tapenest/services/reco-service/internal/config"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
)

// Infra holds connections.
type Infra struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
	Store *repo.Store
}

// NewLogger builds the JSON logger.
func NewLogger(level, component string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "reco-service", "component", component)
}

// Open connects to PostgreSQL (+ migrations) and Redis.
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
	return &Infra{Pool: pool, Redis: redis.NewClient(ropt), Store: repo.NewStore(pool)}, nil
}

// Close releases connections.
func (i *Infra) Close() {
	i.Pool.Close()
	_ = i.Redis.Close()
}
