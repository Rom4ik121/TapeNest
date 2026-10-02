// Package testutil holds in-memory fakes of the persistence and storage ports
// shared by service and transport tests (excluded from coverage).
package testutil

import (
	"bytes"
	"context"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
)

// MemStore is an in-memory Store with the same state-transition rules as SQL.
type MemStore struct {
	mu    sync.Mutex
	Jobs  map[uuid.UUID]domain.Job
	Media map[uuid.UUID]domain.Media
	Fail  map[string]error // method → forced error
	Now   func() time.Time
}

// NewMemStore creates an empty store.
func NewMemStore() *MemStore {
	return &MemStore{Jobs: map[uuid.UUID]domain.Job{}, Media: map[uuid.UUID]domain.Media{}, Fail: map[string]error{}, Now: time.Now}
}

func (s *MemStore) err(m string) error { return s.Fail[m] }

// InsertJob implements the port.
func (s *MemStore) InsertJob(_ context.Context, j domain.Job) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("InsertJob"); err != nil {
		return domain.Job{}, err
	}
	j.CreatedAt, j.UpdatedAt = s.Now(), s.Now()
	s.Jobs[j.ID] = j
	return j, nil
}

// GetJob implements the port.
func (s *MemStore) GetJob(_ context.Context, id uuid.UUID) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("GetJob"); err != nil {
		return domain.Job{}, err
	}
	j, ok := s.Jobs[id]
	if !ok {
		return domain.Job{}, repo.ErrNotFound
	}
	return j, nil
}

// GetUserJob implements the port.
func (s *MemStore) GetUserJob(ctx context.Context, userID, id uuid.UUID) (domain.Job, error) {
	j, err := s.GetJob(ctx, id)
	if err == nil && j.UserID != userID {
		return domain.Job{}, repo.ErrNotFound
	}
	return j, err
}

// ListUserJobs implements the port.
func (s *MemStore) ListUserJobs(_ context.Context, userID uuid.UUID, after *repo.Cursor, limit int) ([]domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("ListUserJobs"); err != nil {
		return nil, err
	}
	var out []domain.Job
	for _, j := range s.Jobs {
		if j.UserID == userID && (after == nil || j.CreatedAt.Before(after.CreatedAt)) {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// FindActiveUserJob implements the port.
func (s *MemStore) FindActiveUserJob(_ context.Context, userID uuid.UUID, hash string) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("FindActiveUserJob"); err != nil {
		return domain.Job{}, err
	}
	for _, j := range s.Jobs {
		if j.UserID == userID && j.URLHash == hash && !j.Status.Terminal() {
			return j, nil
		}
	}
	return domain.Job{}, repo.ErrNotFound
}

// CountUserJobs implements the port.
func (s *MemStore) CountUserJobs(_ context.Context, userID uuid.UUID, since time.Time) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("CountUserJobs"); err != nil {
		return 0, 0, err
	}
	active, recent := 0, 0
	for _, j := range s.Jobs {
		if j.UserID != userID {
			continue
		}
		if !j.Status.Terminal() {
			active++
		}
		if !j.CreatedAt.Before(since) {
			recent++
		}
	}
	return active, recent, nil
}

func (s *MemStore) update(id uuid.UUID, method string, fn func(j *domain.Job) bool) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err(method); err != nil {
		return domain.Job{}, err
	}
	j, ok := s.Jobs[id]
	if !ok || !fn(&j) {
		return domain.Job{}, repo.ErrNotFound
	}
	j.UpdatedAt = s.Now()
	s.Jobs[id] = j
	return j, nil
}

// MarkRunning implements the port.
func (s *MemStore) MarkRunning(_ context.Context, id uuid.UUID, stage domain.Stage) (domain.Job, error) {
	return s.update(id, "MarkRunning", func(j *domain.Job) bool {
		if j.Status.Terminal() { // queued, or running when reclaimed from a dead worker
			return false
		}
		j.Status, j.Stage, j.Attempts = domain.StatusRunning, stage, j.Attempts+1
		return true
	})
}

// SetStage implements the port.
func (s *MemStore) SetStage(_ context.Context, id uuid.UUID, stage domain.Stage, title string) error {
	_, err := s.update(id, "SetStage", func(j *domain.Job) bool {
		j.Stage = stage
		if title != "" {
			j.Title = title
		}
		return true
	})
	return err
}

// Requeue implements the port.
func (s *MemStore) Requeue(_ context.Context, id uuid.UUID, prio domain.Priority, kind domain.ErrorKind, msg string) (domain.Job, error) {
	return s.update(id, "Requeue", func(j *domain.Job) bool {
		j.Status, j.Stage, j.Priority, j.ErrorKind, j.ErrorMessage = domain.StatusQueued, "", prio, kind, msg
		return true
	})
}

// FinishDone implements the port.
func (s *MemStore) FinishDone(_ context.Context, id, mediaID uuid.UUID, title string) (domain.Job, error) {
	return s.update(id, "FinishDone", func(j *domain.Job) bool {
		if j.Status.Terminal() {
			return false
		}
		now := s.Now()
		j.Status, j.MediaID, j.Title, j.FinishedAt = domain.StatusDone, &mediaID, title, &now
		return true
	})
}

// FinishFailed implements the port.
func (s *MemStore) FinishFailed(_ context.Context, id uuid.UUID, kind domain.ErrorKind, msg string) (domain.Job, error) {
	return s.update(id, "FinishFailed", func(j *domain.Job) bool {
		if j.Status.Terminal() {
			return false
		}
		now := s.Now()
		j.Status, j.ErrorKind, j.ErrorMessage, j.FinishedAt = domain.StatusFailed, kind, msg, &now
		return true
	})
}

// GetMedia implements the port.
func (s *MemStore) GetMedia(_ context.Context, id uuid.UUID) (domain.Media, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("GetMedia"); err != nil {
		return domain.Media{}, err
	}
	m, ok := s.Media[id]
	if !ok {
		return domain.Media{}, repo.ErrNotFound
	}
	return m, nil
}

// GetMediaByHash implements the port.
func (s *MemStore) GetMediaByHash(_ context.Context, hash string) (domain.Media, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("GetMediaByHash"); err != nil {
		return domain.Media{}, err
	}
	for _, m := range s.Media {
		if m.URLHash == hash && m.ExpiresAt.After(s.Now()) {
			return m, nil
		}
	}
	return domain.Media{}, repo.ErrNotFound
}

// UpsertMedia implements the port.
func (s *MemStore) UpsertMedia(_ context.Context, m domain.Media) (domain.Media, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.err("UpsertMedia"); err != nil {
		return domain.Media{}, err
	}
	for id, old := range s.Media {
		if old.URLHash == m.URLHash {
			m.ID = id
		}
	}
	m.CreatedAt = s.Now()
	s.Media[m.ID] = m
	return m, nil
}

// Job returns a job snapshot.
// Job implements the port.
func (s *MemStore) Job(id uuid.UUID) domain.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Jobs[id]
}

// MemFiles is in-memory object storage.
type MemFiles struct {
	mu        sync.Mutex
	Objects   map[string][]byte
	PutErr    error
	ExistsErr error
	NoPublic  bool
}

// NewMemFiles creates empty storage.
func NewMemFiles() *MemFiles { return &MemFiles{Objects: map[string][]byte{}} }

// Put implements the port.
func (f *MemFiles) Put(_ context.Context, key string, r io.Reader, _ int64, _, _ string) (int64, error) {
	if f.PutErr != nil {
		return 0, f.PutErr
	}
	var b bytes.Buffer
	n, err := io.Copy(&b, r)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.Objects[key] = b.Bytes()
	f.mu.Unlock()
	return n, nil
}

// Exists implements the port.
func (f *MemFiles) Exists(_ context.Context, key string) (bool, error) {
	if f.ExistsErr != nil {
		return false, f.ExistsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Objects[key]
	return ok, nil
}

// PresignInternal implements the port.
func (f *MemFiles) PresignInternal(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "http://minio.internal/media/" + key + "?sig=x", nil
}

// PresignPublic implements the port.
func (f *MemFiles) PresignPublic(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	if f.NoPublic {
		return "", nil
	}
	return "https://app.example/media/" + key + "?sig=y", nil
}
