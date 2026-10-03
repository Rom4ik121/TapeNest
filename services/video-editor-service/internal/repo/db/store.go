package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

// Store is the Postgres implementation.
type Store struct{ pool *pgxpool.Pool }

// New returns a store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// FindBySource returns the user's project for a download.
func (s *Store) FindBySource(ctx context.Context, user, job uuid.UUID) (domain.Project, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT user_id, id, source_job_id, object_key, title, duration_sec, width, height, recipe, music_key, latest_export_id, created_at, updated_at
		FROM videoedit.projects WHERE user_id=$1 AND source_job_id=$2`, user, job)
	return s.scanProject(ctx, row)
}

// InsertProject stores a new project.
func (s *Store) InsertProject(ctx context.Context, p domain.Project) error {
	raw, err := json.Marshal(p.Recipe)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO videoedit.projects (user_id, id, source_job_id, object_key, title, duration_sec, width, height, recipe, music_key, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.UserID, p.ID, p.SourceJobID, p.ObjectKey, p.Title, p.Duration, p.Width, p.Height, raw, p.MusicKey, p.CreatedAt, p.UpdatedAt)
	return err
}

// GetProject returns one project of the user.
func (s *Store) GetProject(ctx context.Context, user, id uuid.UUID) (domain.Project, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT user_id, id, source_job_id, object_key, title, duration_sec, width, height, recipe, music_key, latest_export_id, created_at, updated_at
		FROM videoedit.projects WHERE user_id=$1 AND id=$2`, user, id)
	return s.scanProject(ctx, row)
}

// SaveRecipe replaces the recipe when no export is in flight.
func (s *Store) SaveRecipe(ctx context.Context, user, id uuid.UUID, r domain.Recipe) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE videoedit.projects p SET recipe=$3, updated_at=now()
		WHERE p.user_id=$1 AND p.id=$2
		  AND NOT EXISTS (
		    SELECT 1 FROM videoedit.exports e
		    WHERE e.id = p.latest_export_id AND e.user_id=p.user_id AND e.status IN ('queued','running'))`,
		user, id, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var n int
	err = s.pool.QueryRow(ctx, `SELECT 1 FROM videoedit.projects WHERE user_id=$1 AND id=$2`, user, id).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return domain.ErrBusy
}

// SetMusic stores or clears the music-bed object key.
func (s *Store) SetMusic(ctx context.Context, user, id uuid.UUID, key string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE videoedit.projects SET music_key=$3, updated_at=now() WHERE user_id=$1 AND id=$2`, user, id, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// InsertExport queues a render.
func (s *Store) InsertExport(ctx context.Context, e domain.Export) error {
	raw, err := json.Marshal(e.Recipe)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(e.status,'') FROM videoedit.projects p
		LEFT JOIN videoedit.exports e ON e.id = p.latest_export_id AND e.user_id = p.user_id
		WHERE p.user_id=$1 AND p.id=$2 FOR UPDATE OF p`, e.UserID, e.ProjectID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == string(domain.StatusQueued) || status == string(domain.StatusRunning) {
		return domain.ErrBusy
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO videoedit.exports (user_id, id, project_id, status, recipe, created_at, updated_at)
		VALUES ($1,$2,$3,'queued',$4,$5,$5)`, e.UserID, e.ID, e.ProjectID, raw, e.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE videoedit.projects SET latest_export_id=$3, updated_at=now() WHERE user_id=$1 AND id=$2`,
		e.UserID, e.ProjectID, e.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Claim marks the oldest queued export running.
func (s *Store) Claim(ctx context.Context) (domain.Export, domain.Project, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Export{}, domain.Project{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var e domain.Export
	var raw []byte
	err = tx.QueryRow(ctx, `
		UPDATE videoedit.exports SET status='running', updated_at=now()
		WHERE id = (
		  SELECT id FROM videoedit.exports WHERE status='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING user_id, id, project_id, recipe`).Scan(&e.UserID, &e.ID, &e.ProjectID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Export{}, domain.Project{}, false, nil
	}
	if err != nil {
		return domain.Export{}, domain.Project{}, false, err
	}
	if err := json.Unmarshal(raw, &e.Recipe); err != nil {
		return domain.Export{}, domain.Project{}, false, err
	}
	e.Status = domain.StatusRunning
	p, err := scanProjectRow(tx.QueryRow(ctx, `
		SELECT user_id, id, source_job_id, object_key, title, duration_sec, width, height, recipe, music_key, created_at, updated_at
		FROM videoedit.projects WHERE user_id=$1 AND id=$2`, e.UserID, e.ProjectID))
	if err != nil {
		return domain.Export{}, domain.Project{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Export{}, domain.Project{}, false, err
	}
	return e, p, true, nil
}

// Finish records the render result.
func (s *Store) Finish(ctx context.Context, user, id uuid.UUID, status domain.Status, outputKey, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE videoedit.exports SET status=$3, output_key=$4, error_message=$5, updated_at=now()
		WHERE user_id=$1 AND id=$2`, user, id, string(status), outputKey, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetExport returns one export of the user.
func (s *Store) GetExport(ctx context.Context, user, id uuid.UUID) (domain.Export, error) {
	var e domain.Export
	var raw []byte
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT user_id, id, project_id, status, recipe, output_key, error_message, created_at, updated_at
		FROM videoedit.exports WHERE user_id=$1 AND id=$2`, user, id).Scan(
		&e.UserID, &e.ID, &e.ProjectID, &status, &raw, &e.OutputKey, &e.Error, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Export{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Export{}, err
	}
	e.Status = domain.Status(status)
	if err := json.Unmarshal(raw, &e.Recipe); err != nil {
		return domain.Export{}, err
	}
	return e, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanProject(ctx context.Context, row pgx.Row) (domain.Project, error) {
	var p domain.Project
	var raw []byte
	var latest *uuid.UUID
	err := row.Scan(&p.UserID, &p.ID, &p.SourceJobID, &p.ObjectKey, &p.Title, &p.Duration, &p.Width, &p.Height, &raw, &p.MusicKey, &latest, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, err
	}
	if err := json.Unmarshal(raw, &p.Recipe); err != nil {
		return domain.Project{}, err
	}
	if latest != nil {
		e, err := s.GetExport(ctx, p.UserID, *latest)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return domain.Project{}, err
		}
		if err == nil {
			p.Latest = &e
		}
	}
	return p, nil
}

func scanProjectRow(row rowScanner) (domain.Project, error) {
	var p domain.Project
	var raw []byte
	err := row.Scan(&p.UserID, &p.ID, &p.SourceJobID, &p.ObjectKey, &p.Title, &p.Duration, &p.Width, &p.Height, &raw, &p.MusicKey, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, err
	}
	if err := json.Unmarshal(raw, &p.Recipe); err != nil {
		return domain.Project{}, err
	}
	return p, nil
}
