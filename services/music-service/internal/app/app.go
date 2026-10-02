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

	"github.com/tapenest/tapenest/services/music-service/internal/config"
	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// Infra holds connections.
type Infra struct {
	Pool      *pgxpool.Pool
	Replica   *pgxpool.Pool // nil when DB_REPLICA_URL is empty
	Redis     *redis.Client
	Navidrome *navidrome.Client
	Store     *repo.Store
}

// NewLogger builds the JSON logger.
func NewLogger(level, component string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "music-service", "component", component)
}

// Open connects to PostgreSQL (+ replica, migrations), Redis and builds the Navidrome client.
func Open(ctx context.Context, cfg *config.Config, log *slog.Logger, migrate bool) (*Infra, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	inf := &Infra{Pool: pool}
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
	if cfg.ReplicaURL != "" {
		if inf.Replica, err = pgxpool.New(ctx, cfg.ReplicaURL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres replica: %w", err)
		}
		log.Info("read replica enabled for catalog reads")
	}
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		inf.Close()
		return nil, fmt.Errorf("redis url: %w", err)
	}
	inf.Redis = redis.NewClient(ropt)
	if inf.Navidrome, err = navidrome.New(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword, log); err != nil {
		inf.Close()
		return nil, err
	}
	if inf.Replica != nil {
		inf.Store = repo.NewStore(pool, inf.Replica)
	} else {
		inf.Store = repo.NewStore(pool, nil)
	}
	return inf, nil
}

// Close releases connections.
func (i *Infra) Close() {
	i.Pool.Close()
	if i.Replica != nil {
		i.Replica.Close()
	}
	if i.Redis != nil {
		_ = i.Redis.Close()
	}
}

// PingPG checks PostgreSQL (readyz).
func (i *Infra) PingPG(ctx context.Context) error { return i.Pool.Ping(ctx) }

// PingRedis checks Redis (readyz).
func (i *Infra) PingRedis(ctx context.Context) error { return i.Redis.Ping(ctx).Err() }
