package auth

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

const testBotToken = "123456:TEST-token-for-unit-tests"

var testNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return testNow }

// signInitData builds initData signed like Telegram does.
func signInitData(t *testing.T, v *InitDataValidator, authDate time.Time, user map[string]any, extra map[string]string) string {
	t.Helper()
	uj, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	vals := url.Values{}
	vals.Set("user", string(uj))
	vals.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	vals.Set("query_id", "AAHdF6IQAAAAAN0XohDhrOrc")
	for k, x := range extra {
		vals.Set(k, x)
	}
	vals.Set("hash", v.Sign(vals))
	return vals.Encode()
}

func defaultUser() map[string]any {
	return map[string]any{"id": 42, "first_name": "Рома", "last_name": "П", "username": "roma", "language_code": "ru", "is_premium": true}
}

type fakeRepo struct {
	mu    sync.Mutex
	byTG  map[int64]domain.User
	calls int
	err   error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byTG: map[int64]domain.User{}} }

func (f *fakeRepo) UpsertTelegramUser(_ context.Context, p domain.TelegramProfile, role domain.Role, loginAt *time.Time) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return domain.User{}, f.err
	}
	u, ok := f.byTG[p.ID]
	if !ok {
		u = domain.User{ID: uuid.New(), TelegramID: p.ID, CreatedAt: testNow}
	}
	u.FirstName, u.Role, u.IsPremium = p.FirstName, role, p.IsPremium
	if p.Username != "" {
		u.Username = &p.Username
	}
	if loginAt != nil {
		u.LastLoginAt = loginAt
	}
	f.byTG[p.ID] = u
	return u, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byTG {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (f *fakeRepo) delete(tgID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byTG, tgID)
}

func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}
