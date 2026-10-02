// Package repo is the PostgreSQL persistence of api-gateway (sqlc, schema "gateway").
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
	"github.com/tapenest/tapenest/services/api-gateway/internal/repo/db"
)

// Users implements auth.UserRepo on top of sqlc queries.
type Users struct {
	q *db.Queries
}

// NewUsers creates the repository.
func NewUsers(conn db.DBTX) *Users { return &Users{q: db.New(conn)} }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UpsertTelegramUser inserts a user by telegram_id or refreshes the profile.
// A new user gets a fresh UUID v4; an existing one keeps its id.
func (r *Users) UpsertTelegramUser(ctx context.Context, p domain.TelegramProfile, role domain.Role, loginAt *time.Time) (domain.User, error) {
	row, err := r.q.UpsertTelegramUser(ctx, db.UpsertTelegramUserParams{
		ID:           uuid.New(),
		TelegramID:   p.ID,
		FirstName:    p.FirstName,
		LastName:     optional(p.LastName),
		Username:     optional(p.Username),
		PhotoUrl:     optional(p.PhotoURL),
		LanguageCode: optional(p.LanguageCode),
		IsPremium:    p.IsPremium,
		Role:         string(role),
		LastLoginAt:  loginAt,
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return toDomain(row), nil
}

// GetByID loads a user or returns domain.ErrNotFound.
func (r *Users) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return toDomain(row), nil
}

// GetByTelegramID loads a user by Telegram id or returns domain.ErrNotFound.
func (r *Users) GetByTelegramID(ctx context.Context, telegramID int64) (domain.User, error) {
	row, err := r.q.GetUserByTelegramID(ctx, telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by telegram id: %w", err)
	}
	return toDomain(row), nil
}

func toDomain(u db.GatewayUser) domain.User {
	return domain.User{
		ID: u.ID, TelegramID: u.TelegramID, FirstName: u.FirstName, LastName: u.LastName,
		Username: u.Username, PhotoURL: u.PhotoUrl, LanguageCode: u.LanguageCode,
		IsPremium: u.IsPremium, Role: domain.Role(u.Role), CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt,
	}
}
