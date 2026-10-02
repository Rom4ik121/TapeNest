// Package cookies reads source cookies written by the Playwright sidecar
// (tools/cookie-refresher, spec §5.3): Netscape cookies.txt per source in Redis
// (`cookies:<source>`) plus the refresh time (`cookies:<source>:updated_at`, unix).
package cookies

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Store loads cookies.
type Store struct {
	rdb    redis.UniversalClient
	maxAge time.Duration
}

// New creates a Store; maxAge is the staleness alert threshold (COOKIES_MAX_AGE).
func New(rdb redis.UniversalClient, maxAge time.Duration) *Store {
	return &Store{rdb: rdb, maxAge: maxAge}
}

// Key is the Redis key of a source's cookies.
func Key(s domain.Source) string { return "cookies:" + string(s) }

// Jar is a cookies file on disk.
type Jar struct {
	Path  string
	Age   time.Duration // 0 when unknown
	Stale bool
}

// WriteFile writes the source's cookies into dir (0600) for yt-dlp --cookies.
// ok=false when no cookies are available for the source.
func (s *Store) WriteFile(ctx context.Context, src domain.Source, dir string) (Jar, bool, error) {
	vals, err := s.rdb.MGet(ctx, Key(src), Key(src)+":updated_at").Result()
	if err != nil {
		return Jar{}, false, fmt.Errorf("cookies get: %w", err)
	}
	body, _ := vals[0].(string)
	if body == "" {
		return Jar{}, false, nil
	}
	j := Jar{Path: filepath.Join(dir, "cookies-"+string(src)+".txt")}
	if ts, _ := vals[1].(string); ts != "" {
		if sec, err := strconv.ParseInt(ts, 10, 64); err == nil {
			j.Age = time.Since(time.Unix(sec, 0))
			j.Stale = s.maxAge > 0 && j.Age > s.maxAge
		}
	}
	if err := os.WriteFile(j.Path, []byte(body), 0o600); err != nil {
		return Jar{}, false, fmt.Errorf("cookies write: %w", err)
	}
	return j, true, nil
}

// Put stores cookies (used by the sidecar contract tests and the import CLI).
func (s *Store) Put(ctx context.Context, src domain.Source, netscape string, at time.Time) error {
	if netscape == "" {
		return errors.New("empty cookies")
	}
	if err := s.rdb.MSet(ctx, Key(src), netscape, Key(src)+":updated_at", at.Unix()).Err(); err != nil {
		return fmt.Errorf("cookies put: %w", err)
	}
	return nil
}
