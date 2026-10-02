package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/tapenest/tapenest/services/api-gateway/migrations"
)

// Schema owned by this service (spec §3.1 #12).
const Schema = "gateway"

// Migrate applies golang-migrate migrations; the version table lives in the
// service schema (gateway.schema_migrations) so services never collide.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool) // closing it does not close the pool
	drv, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{SchemaName: Schema, MigrationsTable: "schema_migrations"})
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("migrate driver: %w", err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		_ = drv.Close()
		return fmt.Errorf("migrate init: %w", err)
	}
	// Close releases the dedicated connection the driver holds (and its *sql.DB).
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
