package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

// Access-token errors.
var (
	ErrTokenInvalid = errors.New("token invalid")
	ErrTokenExpired = errors.New("token expired")
)

const (
	issuer   = "tapenest-api-gateway"
	audience = "tapenest"
)

// Claims of an access token. sub = user UUID, sid = refresh family (session) id.
type Claims struct {
	jwt.RegisteredClaims
	TelegramID int64       `json:"tid"`
	Role       domain.Role `json:"role"`
	SessionID  string      `json:"sid"`
}

// Principal is the authenticated caller extracted from a valid access token.
type Principal struct {
	UserID     uuid.UUID
	TelegramID int64
	Role       domain.Role
	SessionID  string
}

// JWTIssuer signs and verifies HS256 access tokens.
type JWTIssuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewJWTIssuer creates an issuer; now is injectable for tests.
func NewJWTIssuer(secret string, ttl time.Duration, now func() time.Time) *JWTIssuer {
	if now == nil {
		now = time.Now
	}
	return &JWTIssuer{secret: []byte(secret), ttl: ttl, now: now}
}

// TTL returns the access-token lifetime.
func (j *JWTIssuer) TTL() time.Duration { return j.ttl }

// Issue signs an access token for p.
func (j *JWTIssuer) Issue(p Principal) (string, time.Time, error) {
	now := j.now()
	exp := now.Add(j.ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   p.UserID.String(),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-clockSkew)),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
		TelegramID: p.TelegramID,
		Role:       p.Role,
		SessionID:  p.SessionID,
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return s, exp, nil
}

// Parse validates signature, algorithm, issuer, audience and expiry.
func (j *JWTIssuer) Parse(token string) (Principal, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return j.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer), jwt.WithAudience(audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(),
		jwt.WithLeeway(5*time.Second), jwt.WithTimeFunc(j.now))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Principal{}, ErrTokenExpired
		}
		return Principal{}, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil || c.SessionID == "" || c.TelegramID <= 0 {
		return Principal{}, ErrTokenInvalid
	}
	return Principal{UserID: uid, TelegramID: c.TelegramID, Role: c.Role, SessionID: c.SessionID}, nil
}
