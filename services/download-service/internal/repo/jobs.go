// Package repo is the PostgreSQL access layer (sqlc, schema "download").
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/repo/db"
)

// ErrNotFound is returned when a row does not exist (or a state transition did not apply).
var ErrNotFound = errors.New("not found")

// Store implements job and media persistence.
type Store struct {
	q *db.Queries
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: db.New(pool)} }

func wrap(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func toJob(r db.DownloadJob) domain.Job {
	j := domain.Job{
		ID: r.ID, UserID: r.UserID, URL: r.Url, Normalized: r.NormalizedUrl, URLHash: r.UrlHash,
		Source: domain.Source(r.Source), Status: domain.Status(r.Status), Stage: domain.Stage(r.Stage),
		Priority: domain.Priority(r.Priority), Attempts: int(r.Attempts), ErrorKind: domain.ErrorKind(r.ErrorKind),
		ErrorMessage: r.ErrorMessage, MediaID: r.MediaID, Title: r.Title, DisplayTitle: r.DisplayTitle,
		DeletedAt: r.DeletedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, FinishedAt: r.FinishedAt,
	}
	if r.ChatID != nil {
		j.Chat = &domain.Chat{ChatID: *r.ChatID, Lang: r.Lang}
		if r.StatusMessageID != nil {
			j.Chat.StatusMessageID = *r.StatusMessageID
		}
		if r.ReplyToMessageID != nil {
			j.Chat.ReplyToMessageID = *r.ReplyToMessageID
		}
	}
	return j
}

func toMedia(r db.DownloadMedium) domain.Media {
	return domain.Media{
		ID: r.ID, URLHash: r.UrlHash, URL: r.NormalizedUrl, Source: domain.Source(r.Source), ExternalID: r.ExternalID,
		Title: r.Title, DurationSec: int(r.DurationSec), Width: int(r.Width), Height: int(r.Height), FormatID: r.FormatID,
		ObjectKey: r.ObjectKey, SizeBytes: r.SizeBytes, MimeType: r.MimeType, Thumbnail: r.ThumbnailUrl,
		PosterKey: r.PosterKey, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
}

func nz(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// InsertJob stores a new job (queued, or done when served from the dedup cache).
func (s *Store) InsertJob(ctx context.Context, j domain.Job) (domain.Job, error) {
	p := db.InsertJobParams{
		UserID: j.UserID, ID: j.ID, Url: j.URL, NormalizedUrl: j.Normalized, UrlHash: j.URLHash, Source: string(j.Source),
		Status: string(j.Status), Priority: string(j.Priority), MediaID: j.MediaID, Title: j.Title, FinishedAt: j.FinishedAt,
	}
	if j.Chat != nil {
		p.ChatID, p.StatusMessageID, p.ReplyToMessageID, p.Lang = &j.Chat.ChatID, nz(j.Chat.StatusMessageID), nz(j.Chat.ReplyToMessageID), j.Chat.Lang
	}
	r, err := s.q.InsertJob(ctx, p)
	if err != nil {
		return domain.Job{}, wrap("insert job", err)
	}
	return toJob(r), nil
}

// GetJob loads a job by id (worker side).
func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (domain.Job, error) {
	r, err := s.q.GetJob(ctx, id)
	if err != nil {
		return domain.Job{}, wrap("get job", err)
	}
	return toJob(r), nil
}

// GetUserJob loads a job owned by the user (owner check, spec §9).
func (s *Store) GetUserJob(ctx context.Context, userID, id uuid.UUID) (domain.Job, error) {
	r, err := s.q.GetUserJob(ctx, db.GetUserJobParams{UserID: userID, ID: id})
	if err != nil {
		return domain.Job{}, wrap("get user job", err)
	}
	return toJob(r), nil
}

// Cursor is a keyset position in a user's history.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListUserJobs returns up to limit jobs older than the cursor, newest first.
func (s *Store) ListUserJobs(ctx context.Context, userID uuid.UUID, after *Cursor, limit int) ([]domain.Job, error) {
	p := db.ListUserJobsParams{UserID: userID, Lim: int32(limit)} //nolint:gosec // limit is clamped by the caller
	if after != nil {
		p.BeforeCreated, p.BeforeID = &after.CreatedAt, &after.ID
	}
	rows, err := s.q.ListUserJobs(ctx, p)
	if err != nil {
		return nil, wrap("list jobs", err)
	}
	out := make([]domain.Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, toJob(r))
	}
	return out, nil
}

// FindActiveUserJob returns the user's queued/running job for the same URL.
func (s *Store) FindActiveUserJob(ctx context.Context, userID uuid.UUID, hash string) (domain.Job, error) {
	r, err := s.q.FindActiveUserJob(ctx, db.FindActiveUserJobParams{UserID: userID, UrlHash: hash})
	if err != nil {
		return domain.Job{}, wrap("find active job", err)
	}
	return toJob(r), nil
}

// CountUserJobs returns active jobs and jobs created since `since` (quotas).
func (s *Store) CountUserJobs(ctx context.Context, userID uuid.UUID, since time.Time) (active, recent int, err error) {
	r, err := s.q.CountUserJobs(ctx, db.CountUserJobsParams{UserID: userID, Since: since})
	if err != nil {
		return 0, 0, wrap("count jobs", err)
	}
	return int(r.Active), int(r.Recent), nil
}

// MarkRunning moves a job to running and counts the attempt (also for a job
// reclaimed from a crashed worker, still "running"). ErrNotFound if terminal.
func (s *Store) MarkRunning(ctx context.Context, id uuid.UUID, stage domain.Stage) (domain.Job, error) {
	r, err := s.q.MarkRunning(ctx, db.MarkRunningParams{ID: id, Stage: string(stage)})
	if err != nil {
		return domain.Job{}, wrap("mark running", err)
	}
	return toJob(r), nil
}

// SetStage updates the stage (and title once known) of a running job.
func (s *Store) SetStage(ctx context.Context, id uuid.UUID, stage domain.Stage, title string) error {
	if err := s.q.SetStage(ctx, db.SetStageParams{ID: id, Stage: string(stage), Title: title}); err != nil {
		return wrap("set stage", err)
	}
	return nil
}

// Requeue puts a job back to queued (retry), remembering the last error.
func (s *Store) Requeue(ctx context.Context, id uuid.UUID, prio domain.Priority, kind domain.ErrorKind, msg string) (domain.Job, error) {
	r, err := s.q.Requeue(ctx, db.RequeueParams{ID: id, Priority: string(prio), ErrorKind: string(kind), ErrorMessage: msg})
	if err != nil {
		return domain.Job{}, wrap("requeue", err)
	}
	return toJob(r), nil
}

// FinishDone marks the job done with its media.
func (s *Store) FinishDone(ctx context.Context, id, mediaID uuid.UUID, title string) (domain.Job, error) {
	r, err := s.q.FinishDone(ctx, db.FinishDoneParams{ID: id, MediaID: &mediaID, Title: title})
	if err != nil {
		return domain.Job{}, wrap("finish done", err)
	}
	return toJob(r), nil
}

// FinishFailed marks the job failed.
func (s *Store) FinishFailed(ctx context.Context, id uuid.UUID, kind domain.ErrorKind, msg string) (domain.Job, error) {
	r, err := s.q.FinishFailed(ctx, db.FinishFailedParams{ID: id, ErrorKind: string(kind), ErrorMessage: msg})
	if err != nil {
		return domain.Job{}, wrap("finish failed", err)
	}
	return toJob(r), nil
}

// GetMedia loads media by id.
func (s *Store) GetMedia(ctx context.Context, id uuid.UUID) (domain.Media, error) {
	r, err := s.q.GetMedia(ctx, id)
	if err != nil {
		return domain.Media{}, wrap("get media", err)
	}
	return toMedia(r), nil
}

// GetMediaByHash is dedup layer 2 (non-expired only).
func (s *Store) GetMediaByHash(ctx context.Context, hash string) (domain.Media, error) {
	r, err := s.q.GetMediaByHash(ctx, hash)
	if err != nil {
		return domain.Media{}, wrap("get media by hash", err)
	}
	return toMedia(r), nil
}

// UpsertMedia stores (or refreshes) the media row for a canonical URL. The id
// of an existing row is kept, so the returned id may differ from m.ID.
func (s *Store) UpsertMedia(ctx context.Context, m domain.Media) (domain.Media, error) {
	r, err := s.q.UpsertMedia(ctx, db.UpsertMediaParams{
		ID: m.ID, UrlHash: m.URLHash, NormalizedUrl: m.URL, Source: string(m.Source), ExternalID: m.ExternalID,
		Title: m.Title, DurationSec: int32(m.DurationSec), Width: int32(m.Width), Height: int32(m.Height), //nolint:gosec // small values
		FormatID: m.FormatID, ObjectKey: m.ObjectKey, SizeBytes: m.SizeBytes, MimeType: m.MimeType,
		ThumbnailUrl: m.Thumbnail, PosterKey: m.PosterKey, ExpiresAt: m.ExpiresAt,
	})
	if err != nil {
		return domain.Media{}, wrap("upsert media", err)
	}
	return toMedia(r), nil
}

// RenameJob sets the owner's display title on a finished, visible job.
func (s *Store) RenameJob(ctx context.Context, userID, id uuid.UUID, title string) (domain.Job, error) {
	r, err := s.q.RenameJob(ctx, db.RenameJobParams{UserID: userID, ID: id, DisplayTitle: title})
	if err != nil {
		return domain.Job{}, wrap("rename job", err)
	}
	return toJob(r), nil
}

// SoftDeleteJob hides a job from the owner's library.
func (s *Store) SoftDeleteJob(ctx context.Context, userID, id uuid.UUID) (domain.Job, error) {
	r, err := s.q.SoftDeleteJob(ctx, db.SoftDeleteJobParams{UserID: userID, ID: id})
	if err != nil {
		return domain.Job{}, wrap("delete job", err)
	}
	return toJob(r), nil
}

// CountLiveMediaRefs counts library rows that still point at the file.
func (s *Store) CountLiveMediaRefs(ctx context.Context, mediaID uuid.UUID) (int, error) {
	n, err := s.q.CountLiveMediaRefs(ctx, &mediaID)
	if err != nil {
		return 0, wrap("count media refs", err)
	}
	return int(n), nil
}

// SetPosterKey stores the jpeg object key for a media row.
func (s *Store) SetPosterKey(ctx context.Context, mediaID uuid.UUID, key string) error {
	if err := s.q.SetPosterKey(ctx, db.SetPosterKeyParams{ID: mediaID, PosterKey: key}); err != nil {
		return wrap("set poster", err)
	}
	return nil
}
