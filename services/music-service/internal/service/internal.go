package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// ExportStore is the port for reco-service's internal exports.
type ExportStore interface {
	ExportCatalog(ctx context.Context, after *uuid.UUID, limit int) ([]repo.ExportTrack, error)
	ExportInteractions(ctx context.Context, since time.Time, emit func(repo.Interaction) error) error
}

// Internal serves the service-to-service exports used by reco-service
// (catalog copy + one-off interactions backfill, ADR 0010 §2).
type Internal struct{ store ExportStore }

// NewInternal creates the internal export service.
func NewInternal(store ExportStore) *Internal { return &Internal{store: store} }

// Catalog returns a page and the cursor of the next one (nil at the end).
func (i *Internal) Catalog(ctx context.Context, after *uuid.UUID, limit int) ([]repo.ExportTrack, *uuid.UUID, error) {
	items, err := i.store.ExportCatalog(ctx, after, limit+1)
	if err != nil {
		return nil, nil, err
	}
	if len(items) > limit {
		items = items[:limit]
		next := items[limit-1].ID
		return items, &next, nil
	}
	return items, nil, nil
}

// Interactions streams likes, playlist adds and play events since `since`.
func (i *Internal) Interactions(ctx context.Context, since time.Time, emit func(repo.Interaction) error) error {
	return i.store.ExportInteractions(ctx, since, emit)
}
