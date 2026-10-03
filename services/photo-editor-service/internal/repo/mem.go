// Package repo stores the user's photos. Postgres lives in repo/db.
package repo

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
)

// Mem is the in-memory store used by tests.
type Mem struct {
	mu      sync.Mutex
	photos  map[uuid.UUID]domain.Photo
	exports map[uuid.UUID]domain.Export
}

// NewMem returns an empty store.
func NewMem() *Mem {
	return &Mem{photos: map[uuid.UUID]domain.Photo{}, exports: map[uuid.UUID]domain.Export{}}
}

// InsertPhoto stores a photo.
func (m *Mem) InsertPhoto(_ context.Context, p domain.Photo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.photos[p.ID] = p
	return nil
}

// GetPhoto returns one photo of the user.
func (m *Mem) GetPhoto(_ context.Context, user, id uuid.UUID) (domain.Photo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.photos[id]
	if !ok || p.UserID != user {
		return domain.Photo{}, domain.ErrNotFound
	}
	return p, nil
}

// ListPhotos returns a page, newest first. after is exclusive.
func (m *Mem) ListPhotos(_ context.Context, user uuid.UUID, after time.Time, afterID uuid.UUID, limit int) ([]domain.Photo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []domain.Photo
	for _, p := range m.photos {
		if p.UserID != user {
			continue
		}
		if !after.IsZero() && (p.CreatedAt.After(after) || (p.CreatedAt.Equal(after) && p.ID.String() >= afterID.String())) {
			continue
		}
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID.String() > all[j].ID.String()
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// DeletePhoto removes a photo of the user.
func (m *Mem) DeletePhoto(_ context.Context, user, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.photos[id]
	if !ok || p.UserID != user {
		return domain.ErrNotFound
	}
	delete(m.photos, id)
	return nil
}

// InsertExport stores a render.
func (m *Mem) InsertExport(_ context.Context, e domain.Export) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.photos[e.PhotoID]; !ok || p.UserID != e.UserID {
		return domain.ErrNotFound
	}
	m.exports[e.ID] = e
	return nil
}

// GetExport returns one export of the user.
func (m *Mem) GetExport(_ context.Context, user, id uuid.UUID) (domain.Export, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.exports[id]
	if !ok || e.UserID != user {
		return domain.Export{}, domain.ErrNotFound
	}
	return e, nil
}
