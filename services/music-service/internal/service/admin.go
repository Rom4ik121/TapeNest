package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// AdminStore is the read model for the admin mini app.
type AdminStore interface {
	CatalogHealth(ctx context.Context) (repo.CatalogHealth, error)
	UserMusic(ctx context.Context, user uuid.UUID) (repo.UserMusic, error)
}

// Admin serves catalog health and per-user listening data. It does not add a music source.
type Admin struct {
	store AdminStore
}

// NewAdmin builds the admin read API.
func NewAdmin(store AdminStore) *Admin { return &Admin{store: store} }

// Catalog returns catalog counts.
func (a *Admin) Catalog(ctx context.Context) (repo.CatalogHealth, error) {
	return a.store.CatalogHealth(ctx)
}

// User returns likes and listening preferences for one user.
func (a *Admin) User(ctx context.Context, id uuid.UUID) (repo.UserMusic, error) {
	return a.store.UserMusic(ctx, id)
}
