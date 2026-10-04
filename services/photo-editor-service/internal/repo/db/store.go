// Package db is the Postgres adapter. Unit coverage excludes this package
// (it needs a database); behavior is covered through repo.Mem.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/photo-editor-service/migrations"
)

// Schema owned by this service.
const Schema = "photo"

// Store is Postgres.
type Store struct{ pool *pgxpool.Pool }

// New returns a store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Migrate applies the photo schema.
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

// InsertPhoto stores a photo.
func (s *Store) InsertPhoto(ctx context.Context, p domain.Photo) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO photo.photos (user_id, id, object_key, title, width, height, mime_type, size_bytes, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.UserID, p.ID, p.ObjectKey, p.Title, p.Width, p.Height, p.MimeType, p.SizeBytes, p.CreatedAt)
	return err
}

// GetPhoto returns one photo of the user.
func (s *Store) GetPhoto(ctx context.Context, user, id uuid.UUID) (domain.Photo, error) {
	var p domain.Photo
	err := s.pool.QueryRow(ctx, `
		SELECT user_id, id, object_key, title, width, height, mime_type, size_bytes, created_at
		FROM photo.photos WHERE user_id=$1 AND id=$2`, user, id).Scan(
		&p.UserID, &p.ID, &p.ObjectKey, &p.Title, &p.Width, &p.Height, &p.MimeType, &p.SizeBytes, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Photo{}, domain.ErrNotFound
	}
	return p, err
}

// ListPhotos returns a page newest first.
func (s *Store) ListPhotos(ctx context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, limit int) ([]domain.Photo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, id, object_key, title, width, height, mime_type, size_bytes, created_at
		FROM photo.photos
		WHERE user_id=$1 AND ($2 = '0001-01-01T00:00:00Z'::timestamptz OR (created_at, id) < ($2, $3))
		ORDER BY created_at DESC, id DESC
		LIMIT $4`, user, after.UTC(), afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Photo
	for rows.Next() {
		var p domain.Photo
		if err := rows.Scan(&p.UserID, &p.ID, &p.ObjectKey, &p.Title, &p.Width, &p.Height, &p.MimeType, &p.SizeBytes, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePhoto removes a photo of the user.
func (s *Store) DeletePhoto(ctx context.Context, user, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM photo.photos WHERE user_id=$1 AND id=$2`, user, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// InsertExport stores a render.
func (s *Store) InsertExport(ctx context.Context, e domain.Export) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO photo.exports (user_id, id, photo_id, object_key, mime_type, width, height, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.UserID, e.ID, e.PhotoID, e.ObjectKey, e.MimeType, e.Width, e.Height, e.CreatedAt)
	return err
}

// GetExport returns one export of the user.
func (s *Store) GetExport(ctx context.Context, user, id uuid.UUID) (domain.Export, error) {
	var e domain.Export
	err := s.pool.QueryRow(ctx, `
		SELECT user_id, id, photo_id, object_key, mime_type, width, height, created_at
		FROM photo.exports WHERE user_id=$1 AND id=$2`, user, id).Scan(
		&e.UserID, &e.ID, &e.PhotoID, &e.ObjectKey, &e.MimeType, &e.Width, &e.Height, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Export{}, domain.ErrNotFound
	}
	return e, err
}
