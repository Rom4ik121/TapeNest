package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis keys/channels.
const (
	WakeChannel = "acq:wake"
	DiskKey     = "acq:disk"
)

// RedisNotifier implements Notifier on Redis pub/sub + a stats key.
type RedisNotifier struct{ Redis *redis.Client }

// Wake nudges the worker (best effort).
func (n RedisNotifier) Wake(ctx context.Context) { _ = n.Redis.Publish(ctx, WakeChannel, "1").Err() }

// LibraryBytes returns the last library size measured by the worker.
func (n RedisNotifier) LibraryBytes(ctx context.Context) int64 {
	d, err := n.Disk(ctx)
	if err != nil {
		return 0
	}
	return d.LibraryBytes
}

// Disk returns the last stats stored by the worker.
func (n RedisNotifier) Disk(ctx context.Context) (DiskStats, error) {
	var d DiskStats
	b, err := n.Redis.Get(ctx, DiskKey).Bytes()
	if err != nil {
		return d, err
	}
	return d, json.Unmarshal(b, &d)
}

// StoreDisk saves stats for the API side.
func (n RedisNotifier) StoreDisk(ctx context.Context, d DiskStats) error {
	b, _ := json.Marshal(d)
	return n.Redis.Set(ctx, DiskKey, b, 24*time.Hour).Err()
}
