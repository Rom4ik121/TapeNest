// Package domain holds api-gateway entities shared by auth, repo and transport.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Role is a coarse RBAC role (spec §5.2: admin role is an RBAC placeholder).
type Role string

// Known roles.
const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// ErrNotFound is returned by repositories when an entity does not exist.
var ErrNotFound = errors.New("not found")

// TelegramProfile is the user data Telegram vouches for (initData.user or a bot update).
type TelegramProfile struct {
	ID           int64
	FirstName    string
	LastName     string
	Username     string
	LanguageCode string
	PhotoURL     string
	IsPremium    bool
}

// User is a TapeNest account. ID is a UUID v4 (never the Telegram id).
type User struct {
	ID           uuid.UUID
	TelegramID   int64
	FirstName    string
	LastName     *string
	Username     *string
	PhotoURL     *string
	LanguageCode *string
	IsPremium    bool
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}
