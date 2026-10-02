package worker_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
	"github.com/tapenest/tapenest/services/music-service/internal/worker"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestBatcherRunStoresAndAcks(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	u, tr := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if err := mq.Publish(ctx, rdb, mq.PlayEvent{UserID: u, TrackID: tr, PositionSec: float64(10 * i), Completed: i == 2}); err != nil {
			t.Fatal(err)
		}
	}
	// a malformed entry is acked and dropped
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: mq.PlayEventsStream, Values: map[string]any{"u": "bad"}})
	mem := testutil.NewMem(0)
	b := &worker.Batcher{RDB: rdb, Store: mem, Consumer: "c1", Block: 50 * time.Millisecond, Log: quiet}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.Run(rctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for mem.EventCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if len(mem.Events) != 3 || !mem.Events[2].Completed || mem.Events[1].PositionSec != 10 {
		t.Fatalf("events: %+v", mem.Events)
	}
	pend, err := rdb.XPending(ctx, mq.PlayEventsStream, mq.BatcherGroup).Result()
	if err != nil || pend.Count != 0 {
		t.Fatalf("all acked: %+v %v", pend, err)
	}
	// redelivery is idempotent
	if _, err := mem.InsertPlayEvents(ctx, mem.Events); err != nil || len(mem.Events) != 3 {
		t.Fatal("idempotent insert")
	}
}

func TestBatcherRetriesFailedBatch(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	_ = mq.Publish(ctx, rdb, mq.PlayEvent{UserID: uuid.New(), TrackID: uuid.New(), PositionSec: 1})
	mem := testutil.NewMem(0)
	mem.Err = testutil.ErrBoom
	b := &worker.Batcher{RDB: rdb, Store: mem, Consumer: "c1", Log: quiet}
	res, err := rdb.XRange(ctx, mq.PlayEventsStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Flush(ctx, res); err == nil {
		t.Fatal("store failure must surface (entries stay pending)")
	}
	if err := b.Flush(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// run: first batch fails, then the store recovers and the pending entry is re-read
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go func() {
		time.Sleep(200 * time.Millisecond)
		mem.SetErr(nil)
	}()
	b.Block = 50 * time.Millisecond
	go b.Run(rctx)
	for mem.EventCount() == 0 && rctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
	if len(mem.Events) != 1 {
		t.Fatal("retried batch not stored")
	}
}

func TestBatcherClaimsIdleEntries(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	rdb.XGroupCreateMkStream(ctx, mq.PlayEventsStream, mq.BatcherGroup, "0")
	_ = mq.Publish(ctx, rdb, mq.PlayEvent{UserID: uuid.New(), TrackID: uuid.New(), PositionSec: 5})
	// a dead consumer read it and never acked
	rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: mq.BatcherGroup, Consumer: "dead", Streams: []string{mq.PlayEventsStream, ">"}, Count: 10})
	time.Sleep(30 * time.Millisecond)
	mem := testutil.NewMem(0)
	b := &worker.Batcher{RDB: rdb, Store: mem, Consumer: "alive", Block: 20 * time.Millisecond, ClaimIdle: 10 * time.Millisecond, Log: quiet}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	go b.Run(rctx)
	for mem.EventCount() == 0 && rctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if len(mem.Events) != 1 {
		t.Fatal("idle entry not claimed")
	}
}

type fakeLib struct {
	songs []navidrome.Song
	err   error
	scans int
}

func (f *fakeLib) StartScan(context.Context) error { f.scans++; return f.err }
func (f *fakeLib) Songs(_ context.Context, off, cnt int) ([]navidrome.Song, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []navidrome.Song
	for i := off; i < len(f.songs) && i < off+cnt; i++ {
		out = append(out, f.songs[i])
	}
	return out, nil
}

func TestSyncer(t *testing.T) {
	ctx := context.Background()
	lib := &fakeLib{}
	for i := 0; i < 7; i++ {
		lib.songs = append(lib.songs, navidrome.Song{ID: uuid.NewString(), Title: "t", Artist: "a", ArtistID: "ar", Album: "al", AlbumID: "alid", Duration: 60})
	}
	lib.songs = append(lib.songs, navidrome.Song{ID: "", Title: "skipped"})
	mem := testutil.NewMem(0)
	now := time.Now()
	s := &worker.Syncer{Lib: lib, Store: mem, PageSize: 3, Log: quiet, Now: func() time.Time { return now }}
	res, err := s.Sync(ctx)
	if err != nil || res.Seen != 7 || res.Deleted != 0 || len(mem.Synced) != 7 {
		t.Fatalf("first sync: %+v %v %d", res, err, len(mem.Synced))
	}
	// two songs removed from the library → soft-deleted on the next full listing
	lib.songs = lib.songs[2:]
	now = now.Add(time.Minute)
	res, err = s.Sync(ctx)
	if err != nil || res.Seen != 5 || res.Deleted != 2 {
		t.Fatalf("second sync: %+v %v", res, err)
	}
	// empty listing while tracks exist → no mass delete
	lib.songs = nil
	now = now.Add(time.Minute)
	res, err = s.Sync(ctx)
	if err != nil || res.Deleted != 0 || len(mem.Synced) != 5 {
		t.Fatalf("empty listing guard: %+v %v", res, err)
	}
	lib.err = testutil.ErrBoom
	if _, err := s.Sync(ctx); err == nil {
		t.Fatal("navidrome error must surface")
	}
	lib.err, lib.songs = nil, []navidrome.Song{{ID: "x", Title: "y"}}
	mem.Err = testutil.ErrBoom
	if _, err := s.Sync(ctx); err == nil {
		t.Fatal("store error must surface")
	}
}

func TestMaintainerAndLoop(t *testing.T) {
	mem := testutil.NewMem(0)
	m := &worker.Maintainer{Store: mem, Log: quiet, Now: func() time.Time { return time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC) }}
	if err := m.EnsurePartitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mem.Parts) != 3 || mem.Parts[0] != "play_events_2026_12" || mem.Parts[2] != "play_events_2027_02" {
		t.Fatalf("partitions: %v", mem.Parts)
	}
	mem.Err = testutil.ErrBoom
	if err := m.EnsurePartitions(context.Background()); err == nil {
		t.Fatal("error expected")
	}
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Loop(ctx, "test", 10*time.Millisecond, quiet, func(context.Context) error { n.Add(1); return testutil.ErrBoom })
		close(done)
	}()
	for n.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
