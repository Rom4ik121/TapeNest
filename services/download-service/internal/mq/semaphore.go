package mq

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// acquire: drop expired holders, then take a slot if below the limit.
var acquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZSCORE', KEYS[1], ARGV[4]) then
  redis.call('ZADD', KEYS[1], ARGV[2], ARGV[4])
  return 1
end
if redis.call('ZCARD', KEYS[1]) < tonumber(ARGV[3]) then
  redis.call('ZADD', KEYS[1], ARGV[2], ARGV[4])
  return 1
end
return 0`)

// Semaphores limit parallel jobs per source across all workers (spec §5.3:
// YouTube ≤ 50, VK ≤ 100, RuTube ≤ 200). Holders expire after ttl, so a crashed
// worker cannot leak slots.
type Semaphores struct {
	rdb    redis.UniversalClient
	limits map[domain.Source]int
	ttl    time.Duration
}

// NewSemaphores creates the limiter.
func NewSemaphores(rdb redis.UniversalClient, limits map[domain.Source]int, ttl time.Duration) *Semaphores {
	return &Semaphores{rdb: rdb, limits: limits, ttl: ttl}
}

func semKey(s domain.Source) string { return "download:sem:" + string(s) }

// Acquire takes a slot for holder; false when the source is at its limit.
func (s *Semaphores) Acquire(ctx context.Context, src domain.Source, holder string) (bool, error) {
	limit, ok := s.limits[src]
	if !ok || limit <= 0 {
		return true, nil
	}
	now := time.Now()
	n, err := acquireScript.Run(ctx, s.rdb, []string{semKey(src)},
		now.UnixMilli(), now.Add(s.ttl).UnixMilli(), limit, holder).Int()
	if err != nil {
		return false, fmt.Errorf("semaphore acquire: %w", err)
	}
	return n == 1, nil
}

// Release frees the slot.
func (s *Semaphores) Release(ctx context.Context, src domain.Source, holder string) error {
	if err := s.rdb.ZRem(ctx, semKey(src), holder).Err(); err != nil {
		return fmt.Errorf("semaphore release: %w", err)
	}
	return nil
}
