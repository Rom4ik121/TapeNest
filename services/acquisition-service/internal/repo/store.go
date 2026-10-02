// Package repo is the PostgreSQL layer (pgx + sqlc) of acquisition-service.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/migrations"
)

// Schema owned by this service (spec §3.1 #12).
const Schema = "acquisition"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Migrate applies migrations; the version table lives in acquisition.schema_migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
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
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Store embeds the generated queries and adds transactions.
type Store struct {
	*db.Queries
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{Queries: db.New(pool), pool: pool} }

// Ping checks the database.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// NotFound maps pgx.ErrNoRows to ErrNotFound.
func NotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// NewRequest is a request with its tracklist.
type NewRequest struct {
	Request db.InsertRequestParams
	Tracks  []db.InsertRequestTrackParams
	UserID  uuid.UUID
	Reason  string
}

// CreateRequest inserts request + tracks + user + "created" event atomically.
// Returns false when another request for the same album won the race.
func (s *Store) CreateRequest(ctx context.Context, n NewRequest) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.WithTx(tx)
	if err := q.InsertRequest(ctx, n.Request); err != nil {
		if isUnique(err) {
			return false, nil
		}
		return false, err
	}
	for _, t := range n.Tracks {
		t.RequestID = n.Request.ID
		if err := q.InsertRequestTrack(ctx, t); err != nil {
			return false, err
		}
	}
	if _, err := q.AddRequestUser(ctx, db.AddRequestUserParams{RequestID: n.Request.ID, UserID: n.UserID, Reason: n.Reason}); err != nil {
		return false, err
	}
	detail, _ := json.Marshal(map[string]any{"reason": n.Reason, "tracks": len(n.Tracks)})
	uid := n.UserID
	if err := q.InsertEvent(ctx, db.InsertEventParams{RequestID: n.Request.ID, Kind: "created", UserID: &uid, Detail: detail}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Event appends to the audit trail (best effort for callers).
func (s *Store) Event(ctx context.Context, id uuid.UUID, kind string, user *uuid.UUID, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return s.InsertEvent(ctx, db.InsertEventParams{RequestID: id, Kind: kind, UserID: user, Detail: b})
}

func isUnique(err error) bool {
	var pe interface{ SQLState() string }
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}
