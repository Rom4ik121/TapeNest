// Package app wires the process infrastructure.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/streaming-service/internal/config"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
)

// Infra is a database pool.
type Infra struct{ Pool *pgxpool.Pool }

// NewLogger builds the JSON logger.
func NewLogger(level, component string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "streaming-service", "component", component)
}

// Open connects and optionally migrates.
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
	return &Infra{Pool: pool}, nil
}

// Close releases the pool.
func (i *Infra) Close() { i.Pool.Close() }

// Ping checks PostgreSQL.
func (i *Infra) Ping(ctx context.Context) error { return i.Pool.Ping(ctx) }
