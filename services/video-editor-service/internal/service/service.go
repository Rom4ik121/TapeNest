// Package service is the video editor: projects on the user's own downloads,
// and ffmpeg exports (trim, split, reorder, speed, crop, rotate, volume, text,
// music bed, transitions).
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/ffmpeg"
)

// Store persists projects and exports.
type Store interface {
	FindBySource(ctx context.Context, user, job uuid.UUID) (domain.Project, error)
	InsertProject(ctx context.Context, p domain.Project) error
	GetProject(ctx context.Context, user, id uuid.UUID) (domain.Project, error)
	SaveRecipe(ctx context.Context, user, id uuid.UUID, r domain.Recipe) error
	SetMusic(ctx context.Context, user, id uuid.UUID, key string) error
	InsertExport(ctx context.Context, e domain.Export) error
	Claim(ctx context.Context) (domain.Export, domain.Project, bool, error)
	Finish(ctx context.Context, user, id uuid.UUID, status domain.Status, outputKey, errMsg string) error
	GetExport(ctx context.Context, user, id uuid.UUID) (domain.Export, error)
}

// Sources resolves a download the user owns.
type Sources interface {
	Resolve(ctx context.Context, user, job uuid.UUID) (domain.SourceFile, error)
}

// Blobs is object storage.
type Blobs interface {
	Download(ctx context.Context, bucket, key, path string, max int64) error
	Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType, fileName string) error
	Presign(ctx context.Context, bucket, key, fileName string, ttl time.Duration) (string, error)
}

// Editor runs ffprobe/ffmpeg.
type Editor interface {
	Probe(ctx context.Context, path string) (ffmpeg.ProbeResult, error)
	Run(ctx context.Context, steps []ffmpeg.Step) error
}

// Service implements the use cases.
type Service struct {
	Store       Store
	Sources     Sources
	Blobs       Blobs
	Editor      Editor
	MediaBucket string
	EditBucket  string
	WorkDir     string
	Font        string
	MaxBytes    int64
	PresignTTL  time.Duration
	Now         func() time.Time
}

// Open creates a project for a finished download, or returns the existing one.
func (s *Service) Open(ctx context.Context, user, job uuid.UUID) (domain.Project, error) {
	if p, err := s.Store.FindBySource(ctx, user, job); err == nil {
		return p, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Project{}, err
	}
	src, err := s.Sources.Resolve(ctx, user, job)
	if err != nil {
		return domain.Project{}, err
	}
	if s.MaxBytes > 0 && src.SizeBytes > s.MaxBytes {
		return domain.Project{}, domain.ErrTooLarge
	}
	now := s.now()
	p := domain.Project{
		UserID: user, ID: uuid.New(), SourceJobID: job, ObjectKey: src.ObjectKey, Title: src.Title,
		Duration: src.DurationSec, Width: src.Width, Height: src.Height,
		Recipe: domain.DefaultRecipe(src.DurationSec), CreatedAt: now, UpdatedAt: now,
	}
	if err := p.Recipe.Validate(p.Duration); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	if err := s.Store.InsertProject(ctx, p); err != nil {
		if existing, findErr := s.Store.FindBySource(ctx, user, job); findErr == nil {
			return existing, nil
		}
		return domain.Project{}, err
	}
	return p, nil
}

// Project returns one project of the user.
func (s *Service) Project(ctx context.Context, user, id uuid.UUID) (domain.Project, error) {
	return s.Store.GetProject(ctx, user, id)
}

// UpdateRecipe saves a validated recipe.
func (s *Service) UpdateRecipe(ctx context.Context, user, id uuid.UUID, r domain.Recipe) (domain.Project, error) {
	p, err := s.Store.GetProject(ctx, user, id)
	if err != nil {
		return domain.Project{}, err
	}
	if err := r.Validate(p.Duration); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	if err := s.Store.SaveRecipe(ctx, user, id, r); err != nil {
		return domain.Project{}, err
	}
	return s.Store.GetProject(ctx, user, id)
}

// AttachMusic stores an audio bed for the project.
func (s *Service) AttachMusic(ctx context.Context, user, id uuid.UUID, r io.Reader, size int64) (domain.Project, error) {
	p, err := s.Store.GetProject(ctx, user, id)
	if err != nil {
		return domain.Project{}, err
	}
	key := fmt.Sprintf("videoedit/%s/%s/music", user, id)
	if err := s.Blobs.Put(ctx, s.EditBucket, key, r, size, "application/octet-stream", "music"); err != nil {
		return domain.Project{}, err
	}
	if err := s.Store.SetMusic(ctx, user, id, key); err != nil {
		return domain.Project{}, err
	}
	p.MusicKey = key
	return p, nil
}

// ClearMusic removes the bed.
func (s *Service) ClearMusic(ctx context.Context, user, id uuid.UUID) (domain.Project, error) {
	if err := s.Store.SetMusic(ctx, user, id, ""); err != nil {
		return domain.Project{}, err
	}
	return s.Store.GetProject(ctx, user, id)
}

// Export queues a render of the current recipe.
func (s *Service) Export(ctx context.Context, user, id uuid.UUID) (domain.Export, error) {
	p, err := s.Store.GetProject(ctx, user, id)
	if err != nil {
		return domain.Export{}, err
	}
	if err := p.Recipe.Validate(p.Duration); err != nil {
		return domain.Export{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	now := s.now()
	e := domain.Export{
		UserID: user, ID: uuid.New(), ProjectID: id, Status: domain.StatusQueued,
		Recipe: p.Recipe, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.InsertExport(ctx, e); err != nil {
		return domain.Export{}, err
	}
	return e, nil
}

// ExportOf returns one export.
func (s *Service) ExportOf(ctx context.Context, user, id uuid.UUID) (domain.Export, error) {
	return s.Store.GetExport(ctx, user, id)
}

// FileURL is a presigned link to a finished export.
func (s *Service) FileURL(ctx context.Context, user, id uuid.UUID) (string, error) {
	e, err := s.Store.GetExport(ctx, user, id)
	if err != nil {
		return "", err
	}
	if e.Status != domain.StatusDone || e.OutputKey == "" {
		return "", domain.ErrNotReady
	}
	u, err := s.Blobs.Presign(ctx, s.EditBucket, e.OutputKey, "edit.mp4", s.ttl())
	if err != nil {
		return "", err
	}
	if u == "" {
		return "", errors.New("public file links are not configured")
	}
	return u, nil
}

// ProcessOne renders a single queued export. ok is false when the queue is empty.
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	e, p, ok, err := s.Store.Claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	if rerr := s.render(ctx, e, p); rerr != nil {
		_ = s.Store.Finish(ctx, e.UserID, e.ID, domain.StatusFailed, "", safeErr(rerr))
		return true, nil
	}
	return true, nil
}

func (s *Service) render(ctx context.Context, e domain.Export, p domain.Project) error {
	dir, err := os.MkdirTemp(s.WorkDir, "export-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src := filepath.Join(dir, "source.mp4")
	if err := s.Blobs.Download(ctx, s.MediaBucket, p.ObjectKey, src, s.MaxBytes); err != nil {
		return err
	}
	info, err := s.Editor.Probe(ctx, src)
	if err != nil {
		return err
	}
	recipe := clamp(e.Recipe, info.Duration)
	if err := recipe.Validate(info.Duration); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	music := ""
	if p.MusicKey != "" {
		music = filepath.Join(dir, "music")
		if err := s.Blobs.Download(ctx, s.EditBucket, p.MusicKey, music, 20<<20); err != nil {
			return err
		}
	}
	out := filepath.Join(dir, "out.mp4")
	steps, err := ffmpeg.Build(ffmpeg.Input{
		Source: src, HasAudio: info.HasAudio, Music: music, Out: out, WorkDir: dir, Font: s.Font,
		Width: even(info.Width, 1280), Height: even(info.Height, 720), Recipe: recipe,
	})
	if err != nil {
		return err
	}
	if err := s.Editor.Run(ctx, steps); err != nil {
		return err
	}
	f, err := os.Open(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	key := fmt.Sprintf("videoedit/%s/%s.mp4", e.UserID, e.ID)
	if err := s.Blobs.Put(ctx, s.EditBucket, key, f, st.Size(), "video/mp4", "edit.mp4"); err != nil {
		return err
	}
	return s.Store.Finish(ctx, e.UserID, e.ID, domain.StatusDone, key, "")
}

func clamp(r domain.Recipe, duration float64) domain.Recipe {
	if duration <= 0 {
		return r
	}
	for i := range r.Clips {
		if r.Clips[i].EndSec > duration {
			r.Clips[i].EndSec = duration
		}
		if r.Clips[i].StartSec > r.Clips[i].EndSec-domain.MinClipSec {
			r.Clips[i].StartSec = 0
			if r.Clips[i].EndSec < domain.MinClipSec {
				r.Clips[i].EndSec = duration
			}
		}
	}
	return r
}

func even(v, fallback int) int {
	if v < 2 {
		return fallback
	}
	if v > 1280 {
		v = 1280
	}
	return v &^ 1
}

func safeErr(err error) string {
	s := err.Error()
	if len(s) > 180 {
		s = s[:180]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) ttl() time.Duration {
	if s.PresignTTL <= 0 {
		return time.Hour
	}
	return s.PresignTTL
}
