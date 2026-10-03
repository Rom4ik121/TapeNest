package repo

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

// Mem is an in-memory store used by tests and as the behavior reference.
type Mem struct {
	mu       sync.Mutex
	projects map[uuid.UUID]domain.Project
	exports  map[uuid.UUID]domain.Export
	order    []uuid.UUID
}

// NewMem returns an empty store.
func NewMem() *Mem {
	return &Mem{projects: map[uuid.UUID]domain.Project{}, exports: map[uuid.UUID]domain.Export{}}
}

// FindBySource returns the user's project for a download.
func (m *Mem) FindBySource(_ context.Context, user, job uuid.UUID) (domain.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		if p.UserID == user && p.SourceJobID == job {
			return m.withLatest(p), nil
		}
	}
	return domain.Project{}, domain.ErrNotFound
}

// InsertProject stores a new project.
func (m *Mem) InsertProject(_ context.Context, p domain.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projects[p.ID] = p
	return nil
}

// GetProject returns one project of the user.
func (m *Mem) GetProject(_ context.Context, user, id uuid.UUID) (domain.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok || p.UserID != user {
		return domain.Project{}, domain.ErrNotFound
	}
	return m.withLatest(p), nil
}

// SaveRecipe replaces the recipe when no export is in flight.
func (m *Mem) SaveRecipe(_ context.Context, user, id uuid.UUID, r domain.Recipe) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok || p.UserID != user {
		return domain.ErrNotFound
	}
	if p.Latest != nil && p.Latest.Status.Busy() {
		return domain.ErrBusy
	}
	p.Recipe = r
	p.UpdatedAt = time.Now().UTC()
	m.projects[id] = p
	return nil
}

// SetMusic stores or clears the music-bed object key.
func (m *Mem) SetMusic(_ context.Context, user, id uuid.UUID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok || p.UserID != user {
		return domain.ErrNotFound
	}
	p.MusicKey = key
	p.UpdatedAt = time.Now().UTC()
	m.projects[id] = p
	return nil
}

// InsertExport queues a render and remembers it on the project.
func (m *Mem) InsertExport(_ context.Context, e domain.Export) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[e.ProjectID]
	if !ok || p.UserID != e.UserID {
		return domain.ErrNotFound
	}
	if p.Latest != nil && p.Latest.Status.Busy() {
		return domain.ErrBusy
	}
	m.exports[e.ID] = e
	m.order = append(m.order, e.ID)
	p.Latest = &e
	m.projects[e.ProjectID] = p
	return nil
}

// Claim marks the oldest queued export running.
func (m *Mem) Claim(_ context.Context) (domain.Export, domain.Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.order {
		e := m.exports[id]
		if e.Status != domain.StatusQueued {
			continue
		}
		e.Status = domain.StatusRunning
		e.UpdatedAt = time.Now().UTC()
		m.exports[id] = e
		p := m.projects[e.ProjectID]
		p.Latest = &e
		m.projects[e.ProjectID] = p
		return e, p, true, nil
	}
	return domain.Export{}, domain.Project{}, false, nil
}

// Finish records the render result.
func (m *Mem) Finish(_ context.Context, user, id uuid.UUID, status domain.Status, outputKey, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.exports[id]
	if !ok || e.UserID != user {
		return domain.ErrNotFound
	}
	e.Status = status
	e.OutputKey = outputKey
	e.Error = errMsg
	e.UpdatedAt = time.Now().UTC()
	m.exports[id] = e
	p := m.projects[e.ProjectID]
	p.Latest = &e
	m.projects[e.ProjectID] = p
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

func (m *Mem) withLatest(p domain.Project) domain.Project {
	if p.Latest == nil {
		return p
	}
	if e, ok := m.exports[p.Latest.ID]; ok {
		cp := e
		p.Latest = &cp
	}
	return p
}
