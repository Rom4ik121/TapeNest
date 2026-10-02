package httpapi

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
)

// ModelSource loads snapshots.
type ModelSource interface {
	ModelVersion(ctx context.Context) (int64, error)
	LoadModel(ctx context.Context, featureVersion int) (*model.Model, error)
}

// Models keeps the current snapshot and reloads it when the worker publishes
// a new model_version (or periodically, to pick up catalog changes).
type Models struct {
	Src            ModelSource
	FeatureVersion int
	Log            *slog.Logger
	cur            atomic.Pointer[model.Model]
	loadedAt       atomic.Int64
}

// Current returns the snapshot (nil before the first load).
func (m *Models) Current() *model.Model { return m.cur.Load() }

// Set replaces the snapshot (tests).
func (m *Models) Set(s *model.Model) { m.cur.Store(s); m.loadedAt.Store(time.Now().UnixNano()) }

// Refresh reloads when the version changed or the snapshot is older than maxAge.
func (m *Models) Refresh(ctx context.Context, maxAge time.Duration) error {
	v, err := m.Src.ModelVersion(ctx)
	if err != nil {
		return err
	}
	cur := m.cur.Load()
	age := time.Duration(time.Now().UnixNano() - m.loadedAt.Load())
	if cur != nil && cur.Version == v && age < maxAge {
		return nil
	}
	s, err := m.Src.LoadModel(ctx, m.FeatureVersion)
	if err != nil {
		return err
	}
	m.Set(s)
	if cur == nil || cur.Version != v {
		m.Log.Info("model loaded", "version", s.Version, "tracks", len(s.Tracks))
	}
	return nil
}

// Run polls until ctx is done.
func (m *Models) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := m.Refresh(ctx, 10*time.Minute); err != nil && ctx.Err() == nil {
			m.Log.Warn("model refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
