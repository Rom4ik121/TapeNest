package core

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisNotifier(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	n := RedisNotifier{Redis: rdb}
	ctx := context.Background()
	n.Wake(ctx)
	if err := n.StoreDisk(ctx, DiskStats{LibraryBytes: 42, TorrentBytes: 7}); err != nil {
		t.Fatal(err)
	}
	d, err := n.Disk(ctx)
	if err != nil || d.LibraryBytes != 42 {
		t.Fatal(d, err)
	}
	if n.LibraryBytes(ctx) != 42 {
		t.Fatal(n.LibraryBytes(ctx))
	}
}
