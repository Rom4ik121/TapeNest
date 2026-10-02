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

	"github.com/tapenest/tapenest/services/acquisition-service/internal/arr"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/config"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/mb"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
)

// Infra holds connections and API clients.
type Infra struct {
	Pool     *pgxpool.Pool
	Redis    *redis.Client
	Store    *repo.Store
	MB       *mb.Client
	Lidarr   *arr.Lidarr
	Prowlarr *arr.Prowlarr
	QBT      *qbt.Client
}

// NewLogger builds the JSON logger.
func NewLogger(level, component string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "acquisition-service", "component", component)
}

// Open connects to PostgreSQL (+ migrations) and Redis and builds the clients.
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
		return nil, fmt.Errorf("redis url: invalid")
	}
	in := &Infra{Pool: pool, Redis: redis.NewClient(ropt), Store: repo.NewStore(pool)}
	in.MB = mb.New(cfg.MBURL, cfg.MBUserAgent, in.Redis)
	if in.Lidarr, err = arr.NewLidarr(cfg.LidarrURL, cfg.LidarrAPIKey); err != nil {
		in.Close()
		return nil, err
	}
	if in.Prowlarr, err = arr.NewProwlarr(cfg.ProwlarrURL, cfg.ProwlarrAPIKey); err != nil {
		in.Close()
		return nil, err
	}
	if in.QBT, err = qbt.New(cfg.QbtURL, cfg.QbtUser, cfg.QbtPassword); err != nil {
		in.Close()
		return nil, err
	}
	return in, nil
}

// Close releases connections.
func (i *Infra) Close() {
	i.Pool.Close()
	_ = i.Redis.Close()
}
