package mq

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

// Locks is dedup layer 3 (spec §5.3): a redsync mutex per canonical URL, so two
// workers never download the same video at the same time; the second one waits
// and then finds the media stored by the first.
type Locks struct {
	rs *redsync.Redsync
}

// NewLocks creates the lock manager.
func NewLocks(rdb redis.UniversalClient) *Locks {
	return &Locks{rs: redsync.New(goredis.NewPool(rdb))}
}

// Lock tries once to take key for ttl. ok=false when someone else holds it.
func (l *Locks) Lock(ctx context.Context, key string, ttl time.Duration) (func(), bool, error) {
	m := l.rs.NewMutex(key, redsync.WithExpiry(ttl), redsync.WithTries(1))
	if err := m.LockContext(ctx); err != nil {
		var taken *redsync.ErrTaken
		if errors.Is(err, redsync.ErrFailed) || errors.As(err, &taken) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock %s: %w", key, err)
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = m.UnlockContext(uctx)
	}, true, nil
}
