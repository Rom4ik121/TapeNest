package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Refresh-token errors.
var (
	ErrRefreshInvalid = errors.New("refresh token invalid or expired")
	// ErrRefreshReused means an already-rotated token was presented again: the
	// whole session family is revoked (token theft detection).
	ErrRefreshReused = errors.New("refresh token reuse detected")
)

// RefreshRecord is what a refresh token resolves to.
type RefreshRecord struct {
	UserID     uuid.UUID `json:"u"`
	TelegramID int64     `json:"t"`
	Family     string    `json:"f"`
}

// RefreshStore keeps opaque refresh tokens in Redis (spec §5.2: refresh 30 days in Redis).
//
// Keys (only SHA-256 of the token is stored, never the token itself):
//
//	gw:rt:<hash>        → JSON RefreshRecord, TTL = refresh TTL
//	gw:rt:used:<hash>   → family id of a rotated token (reuse detection), TTL = refresh TTL
//	gw:rtfam:<family>   → SET of live token hashes of the session family
//	gw:sid:revoked:<f>  → marker that kills access tokens of a revoked session, TTL = access TTL
type RefreshStore struct {
	rdb        redis.UniversalClient
	refreshTTL time.Duration
	accessTTL  time.Duration
}

// NewRefreshStore creates the store.
func NewRefreshStore(rdb redis.UniversalClient, refreshTTL, accessTTL time.Duration) *RefreshStore {
	return &RefreshStore{rdb: rdb, refreshTTL: refreshTTL, accessTTL: accessTTL}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Issue creates a new refresh token in family (a new session when family is empty).
func (s *RefreshStore) Issue(ctx context.Context, rec RefreshRecord) (string, RefreshRecord, error) {
	if rec.Family == "" {
		rec.Family = uuid.NewString()
	}
	token, err := newOpaqueToken()
	if err != nil {
		return "", rec, err
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return "", rec, fmt.Errorf("marshal refresh record: %w", err)
	}
	h := hashToken(token)
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, "gw:rt:"+h, payload, s.refreshTTL)
	pipe.SAdd(ctx, "gw:rtfam:"+rec.Family, h)
	pipe.Expire(ctx, "gw:rtfam:"+rec.Family, s.refreshTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", rec, fmt.Errorf("store refresh token: %w", err)
	}
	return token, rec, nil
}

// consumeScript atomically takes a live token (one-time use) and remembers it
// as used; for an unknown token it reports whether it was used before.
var consumeScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then
  redis.call('DEL', KEYS[1])
  local rec = cjson.decode(v)
  redis.call('SET', KEYS[2], rec['f'], 'PX', ARGV[1])
  redis.call('SREM', 'gw:rtfam:' .. rec['f'], ARGV[2])
  return {'ok', v}
end
local fam = redis.call('GET', KEYS[2])
if fam then return {'reused', fam} end
return {'invalid', ''}
`)

// Consume validates and invalidates token (rotation step 1). On reuse of a
// rotated token the family is revoked and ErrRefreshReused returned.
func (s *RefreshStore) Consume(ctx context.Context, token string) (RefreshRecord, error) {
	if token == "" || len(token) > 256 {
		return RefreshRecord{}, ErrRefreshInvalid
	}
	h := hashToken(token)
	res, err := consumeScript.Run(ctx, s.rdb, []string{"gw:rt:" + h, "gw:rt:used:" + h},
		s.refreshTTL.Milliseconds(), h).StringSlice()
	if err != nil {
		return RefreshRecord{}, fmt.Errorf("consume refresh token: %w", err)
	}
	if len(res) != 2 {
		return RefreshRecord{}, fmt.Errorf("consume refresh token: unexpected reply %v", res)
	}
	switch res[0] {
	case "ok":
		var rec RefreshRecord
		if err := json.Unmarshal([]byte(res[1]), &rec); err != nil {
			return RefreshRecord{}, fmt.Errorf("decode refresh record: %w", err)
		}
		return rec, nil
	case "reused":
		if err := s.RevokeFamily(ctx, res[1]); err != nil {
			return RefreshRecord{}, err
		}
		return RefreshRecord{}, ErrRefreshReused
	default:
		return RefreshRecord{}, ErrRefreshInvalid
	}
}

// Lookup returns the record of a live token without consuming it.
func (s *RefreshStore) Lookup(ctx context.Context, token string) (RefreshRecord, error) {
	v, err := s.rdb.Get(ctx, "gw:rt:"+hashToken(token)).Bytes()
	if errors.Is(err, redis.Nil) {
		return RefreshRecord{}, ErrRefreshInvalid
	}
	if err != nil {
		return RefreshRecord{}, fmt.Errorf("lookup refresh token: %w", err)
	}
	var rec RefreshRecord
	if err := json.Unmarshal(v, &rec); err != nil {
		return RefreshRecord{}, fmt.Errorf("decode refresh record: %w", err)
	}
	return rec, nil
}

// RevokeFamily deletes all live tokens of a session and blocks its access tokens.
func (s *RefreshStore) RevokeFamily(ctx context.Context, family string) error {
	famKey := "gw:rtfam:" + family
	hashes, err := s.rdb.SMembers(ctx, famKey).Result()
	if err != nil {
		return fmt.Errorf("revoke family: %w", err)
	}
	pipe := s.rdb.TxPipeline()
	for _, h := range hashes {
		pipe.Del(ctx, "gw:rt:"+h)
	}
	pipe.Del(ctx, famKey)
	pipe.Set(ctx, "gw:sid:revoked:"+family, 1, s.accessTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("revoke family: %w", err)
	}
	return nil
}

// IsSessionRevoked reports whether access tokens of family must be rejected.
func (s *RefreshStore) IsSessionRevoked(ctx context.Context, family string) (bool, error) {
	n, err := s.rdb.Exists(ctx, "gw:sid:revoked:"+family).Result()
	if err != nil {
		return false, fmt.Errorf("check session revocation: %w", err)
	}
	return n > 0, nil
}
