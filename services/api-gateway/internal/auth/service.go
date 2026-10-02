package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

// UserRepo is the persistence the auth service needs.
type UserRepo interface {
	UpsertTelegramUser(ctx context.Context, p domain.TelegramProfile, role domain.Role, loginAt *time.Time) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// Tokens is the result of a successful login/refresh (contract type AuthTokens).
type Tokens struct {
	AccessToken     string
	AccessExpiresAt time.Time
	RefreshToken    string
	User            domain.User
}

// Service orchestrates login, refresh rotation and logout.
type Service struct {
	validator *InitDataValidator
	jwt       *JWTIssuer
	refresh   *RefreshStore
	users     UserRepo
	admins    map[int64]bool
	now       func() time.Time
}

// NewService wires the auth service.
func NewService(v *InitDataValidator, j *JWTIssuer, r *RefreshStore, users UserRepo, admins map[int64]bool) *Service {
	return &Service{validator: v, jwt: j, refresh: r, users: users, admins: admins, now: time.Now}
}

func (s *Service) roleFor(telegramID int64) domain.Role {
	if s.admins[telegramID] {
		return domain.RoleAdmin
	}
	return domain.RoleUser
}

// LoginTelegram validates initData, upserts the user and opens a new session.
func (s *Service) LoginTelegram(ctx context.Context, rawInitData string) (Tokens, error) {
	data, err := s.validator.Validate(rawInitData)
	if err != nil {
		return Tokens{}, err
	}
	now := s.now()
	user, err := s.users.UpsertTelegramUser(ctx, data.User, s.roleFor(data.User.ID), &now)
	if err != nil {
		return Tokens{}, fmt.Errorf("upsert user: %w", err)
	}
	return s.issue(ctx, user, "")
}

// Refresh rotates a refresh token: the presented token is invalidated and a new
// pair is issued within the same session family.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	rec, err := s.refresh.Consume(ctx, refreshToken)
	if err != nil {
		return Tokens{}, err
	}
	user, err := s.users.GetByID(ctx, rec.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		_ = s.refresh.RevokeFamily(ctx, rec.Family)
		return Tokens{}, ErrRefreshInvalid
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("load user: %w", err)
	}
	return s.issue(ctx, user, rec.Family)
}

// Logout revokes the whole session of refreshToken. Unknown tokens are a no-op.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	rec, err := s.refresh.Lookup(ctx, refreshToken)
	if errors.Is(err, ErrRefreshInvalid) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.refresh.RevokeFamily(ctx, rec.Family)
}

// Authenticate parses an access token and rejects revoked sessions.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	p, err := s.jwt.Parse(accessToken)
	if err != nil {
		return Principal{}, err
	}
	revoked, err := s.refresh.IsSessionRevoked(ctx, p.SessionID)
	if err != nil {
		return Principal{}, err
	}
	if revoked {
		return Principal{}, ErrTokenInvalid
	}
	return p, nil
}

// EnsureUser upserts a user known from a trusted source (bot update) without
// opening a session. Used by internal endpoints.
func (s *Service) EnsureUser(ctx context.Context, p domain.TelegramProfile) (domain.User, error) {
	u, err := s.users.UpsertTelegramUser(ctx, p, s.roleFor(p.ID), nil)
	if err != nil {
		return domain.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

// User loads a user by id.
func (s *Service) User(ctx context.Context, id uuid.UUID) (domain.User, error) {
	return s.users.GetByID(ctx, id)
}

func (s *Service) issue(ctx context.Context, user domain.User, family string) (Tokens, error) {
	refresh, rec, err := s.refresh.Issue(ctx, RefreshRecord{UserID: user.ID, TelegramID: user.TelegramID, Family: family})
	if err != nil {
		return Tokens{}, err
	}
	access, exp, err := s.jwt.Issue(Principal{UserID: user.ID, TelegramID: user.TelegramID, Role: user.Role, SessionID: rec.Family})
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, AccessExpiresAt: exp, RefreshToken: refresh, User: user}, nil
}
