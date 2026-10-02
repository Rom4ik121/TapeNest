package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

type env struct {
	svc   *Service
	repo  *fakeRepo
	store *RefreshStore
	v     *InitDataValidator
}

func newEnv(t *testing.T) (*env, func(time.Duration)) {
	t.Helper()
	mr, rdb := newMiniRedis(t)
	v := NewInitDataValidator(testBotToken, 24*time.Hour, fixedNow)
	store := NewRefreshStore(rdb, 720*time.Hour, 15*time.Minute)
	repo := newFakeRepo()
	svc := NewService(v, NewJWTIssuer(testSecret, 15*time.Minute, fixedNow), store, repo, map[int64]bool{7: true})
	svc.now = fixedNow
	return &env{svc: svc, repo: repo, store: store, v: v}, mr.FastForward
}

func (e *env) login(t *testing.T, user map[string]any) Tokens {
	t.Helper()
	tok, err := e.svc.LoginTelegram(context.Background(), signInitData(t, e.v, testNow, user, nil))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return tok
}

func TestLoginCreatesUserAndSession(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	tok := e.login(t, defaultUser())
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.User.TelegramID != 42 || tok.User.Role != domain.RoleUser {
		t.Fatalf("bad tokens: %+v", tok)
	}
	if tok.User.LastLoginAt == nil || !tok.User.LastLoginAt.Equal(testNow) {
		t.Fatal("last login must be set")
	}
	p, err := e.svc.Authenticate(ctx, tok.AccessToken)
	if err != nil || p.UserID != tok.User.ID {
		t.Fatalf("authenticate: %v %+v", err, p)
	}
	again := e.login(t, defaultUser())
	if again.User.ID != tok.User.ID {
		t.Fatal("same telegram user must keep its UUID")
	}
	if again.RefreshToken == tok.RefreshToken {
		t.Fatal("each login opens a new session")
	}
	admin := e.login(t, map[string]any{"id": 7, "first_name": "Admin"})
	if admin.User.Role != domain.RoleAdmin {
		t.Fatal("ADMIN_TELEGRAM_IDS must grant admin")
	}
	if _, err := e.svc.LoginTelegram(ctx, "hash=00"); !errors.Is(err, ErrInitDataMalformed) {
		t.Fatalf("bad initData: %v", err)
	}
	e.repo.err = errors.New("db down")
	if _, err := e.svc.LoginTelegram(ctx, signInitData(t, e.v, testNow, defaultUser(), nil)); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	first := e.login(t, defaultUser())

	second, err := e.svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.User.ID != first.User.ID {
		t.Fatal("refresh must rotate the token and keep the user")
	}
	p1, _ := e.svc.Authenticate(ctx, first.AccessToken)
	p2, _ := e.svc.Authenticate(ctx, second.AccessToken)
	if p1.SessionID != p2.SessionID {
		t.Fatal("rotation stays in the same session family")
	}

	// Presenting the rotated token again = theft: whole family revoked.
	if _, err := e.svc.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, second.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("family must be revoked: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, second.AccessToken); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("access tokens of a revoked session must die: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, "definitely-not-a-real-refresh-token"); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, ""); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("empty: %v", err)
	}
}

func TestRefreshExpiresAndDeletedUser(t *testing.T) {
	e, ff := newEnv(t)
	ctx := context.Background()
	tok := e.login(t, defaultUser())
	ff(721 * time.Hour)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("expired refresh: %v", err)
	}
	tok = e.login(t, defaultUser())
	e.repo.delete(42)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("deleted user: %v", err)
	}
}

func TestLogoutRevokesSessionOnly(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	a := e.login(t, defaultUser())
	b := e.login(t, defaultUser()) // second device
	if err := e.svc.Logout(ctx, a.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, a.AccessToken); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("logged-out access token: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, a.RefreshToken); err == nil {
		t.Fatal("logged-out refresh must fail")
	}
	if _, err := e.svc.Refresh(ctx, b.RefreshToken); err != nil {
		t.Fatalf("other session must survive: %v", err)
	}
	if err := e.svc.Logout(ctx, "unknown-token-unknown-token"); err != nil {
		t.Fatalf("unknown logout is a no-op: %v", err)
	}
}

func TestEnsureUserAndUser(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	u, err := e.svc.EnsureUser(ctx, domain.TelegramProfile{ID: 7, FirstName: "Bot"})
	if err != nil || u.Role != domain.RoleAdmin || u.LastLoginAt != nil {
		t.Fatalf("ensure: %v %+v", err, u)
	}
	got, err := e.svc.User(ctx, u.ID)
	if err != nil || got.ID != u.ID {
		t.Fatalf("user: %v", err)
	}
	e.repo.err = errors.New("boom")
	if _, err := e.svc.EnsureUser(ctx, domain.TelegramProfile{ID: 8}); err == nil {
		t.Fatal("error expected")
	}
}

func TestRedisFailuresSurface(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	store := NewRefreshStore(rdb, time.Hour, time.Minute)
	mr.Close()
	ctx := context.Background()
	if _, _, err := store.Issue(ctx, RefreshRecord{TelegramID: 1}); err == nil {
		t.Fatal("issue must fail")
	}
	if _, err := store.Consume(ctx, "some-token-some-token"); err == nil || errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("consume must surface infra error, got %v", err)
	}
	if _, err := store.Lookup(ctx, "x"); err == nil || errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := store.IsSessionRevoked(ctx, "f"); err == nil {
		t.Fatal("revoked check must fail")
	}
	if err := store.RevokeFamily(ctx, "f"); err == nil {
		t.Fatal("revoke must fail")
	}
}
