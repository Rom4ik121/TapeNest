// Package ratelimit is a Redis token bucket (spec §7.5: gateway — token bucket per user).
package ratelimit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result of one Allow call.
type Result struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// Limiter is a distributed token bucket: capacity Burst, refill Rate tokens/s.
type Limiter struct {
	rdb    redis.UniversalClient
	prefix string
	rate   float64
	burst  int
	now    func() time.Time
}

// New creates a limiter; keys are "<prefix>:<key>".
func New(rdb redis.UniversalClient, prefix string, rate float64, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{rdb: rdb, prefix: prefix, rate: rate, burst: burst, now: now}
}

// The bucket state is a hash {t: tokens, ts: last refill ms}; the whole
// read-refill-take-write cycle is one atomic script.
var bucketScript = redis.NewScript(`
local rate  = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local d = redis.call('HMGET', KEYS[1], 't', 'ts')
local tokens = tonumber(d[1])
local ts = tonumber(d[2])
if tokens == nil or ts == nil then tokens = burst; ts = now end
if now > ts then tokens = math.min(burst, tokens + (now - ts) / 1000 * rate) end
local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = math.ceil((1 - tokens) / rate * 1000)
end
redis.call('HSET', KEYS[1], 't', tostring(tokens), 'ts', tostring(math.max(now, ts)))
redis.call('PEXPIRE', KEYS[1], math.ceil(burst / rate * 1000) + 1000)
return {allowed, tostring(tokens), retry}
`)

// Allow takes one token for key.
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := bucketScript.Run(ctx, l.rdb, []string{l.prefix + ":" + key},
		l.rate, l.burst, l.now().UnixMilli()).Slice()
	if err != nil {
		return Result{}, fmt.Errorf("rate limit: %w", err)
	}
	if len(res) != 3 {
		return Result{}, fmt.Errorf("rate limit: unexpected reply %v", res)
	}
	allowed, _ := res[0].(int64)
	tokensStr, _ := res[1].(string)
	retryMs, _ := res[2].(int64)
	tokens, err := strconv.ParseFloat(tokensStr, 64)
	if err != nil {
		return Result{}, fmt.Errorf("rate limit: bad tokens %q: %w", tokensStr, err)
	}
	return Result{
		Allowed:    allowed == 1,
		Remaining:  int(math.Floor(tokens)),
		RetryAfter: time.Duration(retryMs) * time.Millisecond,
	}, nil
}
