package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func clients(t *testing.T) map[string]redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	out := map[string]redis.UniversalClient{"miniredis": redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	// Integration: the real Redis 7.4 (TEST_REDIS_URL, e.g. redis://127.0.0.1:6379/15).
	if u := os.Getenv("TEST_REDIS_URL"); u != "" {
		opt, err := redis.ParseURL(u)
		if err != nil {
			t.Fatal(err)
		}
		out["redis"] = redis.NewClient(opt)
	}
	return out
}

func TestTokenBucket(t *testing.T) {
	for name, rdb := range clients(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
			l := New(rdb, "test:rl:"+uuid.NewString(), 2, 3, func() time.Time { return now })

			for i := 0; i < 3; i++ {
				r, err := l.Allow(ctx, "u1")
				if err != nil || !r.Allowed || r.Remaining != 2-i {
					t.Fatalf("burst %d: %+v %v", i, r, err)
				}
			}
			r, _ := l.Allow(ctx, "u1")
			if r.Allowed || r.RetryAfter != 500*time.Millisecond {
				t.Fatalf("must be limited with retry 500ms (rate 2/s): %+v", r)
			}
			if r2, _ := l.Allow(ctx, "u2"); !r2.Allowed {
				t.Fatal("keys are independent")
			}
			now = now.Add(500 * time.Millisecond) // one token refilled
			if r, _ = l.Allow(ctx, "u1"); !r.Allowed {
				t.Fatalf("refill: %+v", r)
			}
			if r, _ = l.Allow(ctx, "u1"); r.Allowed {
				t.Fatal("only one token was refilled")
			}
			now = now.Add(time.Hour) // capped at burst
			for i := 0; i < 3; i++ {
				if r, _ = l.Allow(ctx, "u1"); !r.Allowed {
					t.Fatal("bucket must refill up to burst")
				}
			}
			if r, _ = l.Allow(ctx, "u1"); r.Allowed {
				t.Fatal("never above burst")
			}
			now = now.Add(-time.Minute) // clock going backwards must not mint tokens
			if r, _ = l.Allow(ctx, "u1"); r.Allowed {
				t.Fatal("backwards clock")
			}
		})
	}
}

func TestRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	mr.Close()
	if _, err := New(rdb, "x", 1, 1, nil).Allow(context.Background(), "k"); err == nil {
		t.Fatal("error expected")
	}
}
