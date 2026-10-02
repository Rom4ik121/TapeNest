package ingest_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/reco-service/internal/ingest"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
	"github.com/tapenest/tapenest/services/reco-service/internal/testutil"
)

var log = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestParse(t *testing.T) {
	u, tr := uuid.New(), uuid.New()
	sg, err := ingest.ParsePlay(redis.XMessage{ID: "1700000000000-0", Values: map[string]any{"u": u.String(), "t": tr.String(), "p": "31.5", "c": "1"}})
	if err != nil || sg.Key != "play:1700000000000-0" || !sg.Event.Completed || sg.Event.Kind != profile.KindPlay || sg.Event.At.Unix() != 1700000000 {
		t.Fatalf("play %+v %v", sg, err)
	}
	if _, err := ingest.ParsePlay(redis.XMessage{ID: "x", Values: map[string]any{}}); !errors.Is(err, ingest.ErrSkip) {
		t.Fatal("bad play must be skipped")
	}
	sg, err = ingest.ParseUser(redis.XMessage{ID: "1700000000000-1", Values: map[string]any{"k": "wave_like", "u": u.String(), "t": tr.String(), "src": "cf"}})
	if err != nil || sg.Source != "cf" || sg.Key != "ue:1700000000000-1" {
		t.Fatalf("user %+v %v", sg, err)
	}
	sg, _ = ingest.ParseUser(redis.XMessage{ID: "1700000000000-2", Values: map[string]any{"k": "skip", "u": u.String(), "t": tr.String(), "p": "12", "src": "x"}})
	if sg.Event.PositionSec != 12 || sg.Source != "" {
		t.Fatalf("skip %+v", sg)
	}
	if _, err := ingest.ParseUser(redis.XMessage{ID: "1-0", Values: map[string]any{"k": "dance", "u": u.String(), "t": tr.String()}}); err == nil {
		t.Fatal("unknown kind must be skipped")
	}
}

func TestConsumerRun(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ingest.EnsureGroups(ctx, rdb); err != nil {
		t.Fatal(err)
	}
	if err := ingest.EnsureGroups(ctx, rdb); err != nil { // BUSYGROUP is fine
		t.Fatal(err)
	}
	store := testutil.NewMem()
	u, tr := uuid.New(), uuid.New()
	artist := uuid.New()
	snap := model.New([]model.Track{{ID: tr, ArtistID: artist, Genre: "Jazz", Tags: nil}})
	c := &ingest.Consumer{RDB: rdb, Store: store, Snapshot: func() *model.Model { return snap }, Name: "t", Log: log, Block: 50 * time.Millisecond}
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: ingest.PlayStream, Values: map[string]any{"u": u.String(), "t": tr.String(), "p": "100", "c": "1"}})
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: ingest.UserStream, Values: map[string]any{"k": "like", "u": u.String(), "t": tr.String()}})
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: ingest.UserStream, Values: map[string]any{"k": "garbage"}})
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for store.SignalCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if store.SignalCount() != 2 {
		t.Fatalf("applied %d signals", store.SignalCount())
	}
	if len(store.Signals[0].Keys) != 2 { // artist + genre (album nil)
		t.Fatalf("keys %+v", store.Signals[0].Keys)
	}
	pending, _ := rdb.XPending(context.Background(), ingest.UserStream, ingest.Group).Result()
	if pending.Count != 0 {
		t.Fatalf("pending %d", pending.Count)
	}
}

func TestBackfill(t *testing.T) {
	store := testutil.NewMem()
	music := testutil.NewMusic("tok")
	defer music.Close()
	u, tr := uuid.New().String(), uuid.New().String()
	at := time.Now().UTC().Format(time.RFC3339)
	music.Interactions = []map[string]any{
		{"kind": "like", "userId": u, "trackId": tr, "at": at},
		{"kind": "playlist_add", "userId": u, "trackId": tr, "at": at},
		{"kind": "play", "userId": u, "trackId": tr, "at": at, "completed": true, "eventId": "1-0"},
		{"kind": "play", "userId": u, "trackId": tr, "at": at},
		{"kind": "other", "userId": u, "trackId": tr, "at": at},
	}
	c := &ingest.Consumer{Store: store, Log: log}
	mc := musicclient.New(music.URL, "tok")
	n, err := ingest.Backfill(context.Background(), c, mc, 90)
	if err != nil || n != 3 {
		t.Fatalf("backfill n=%d err=%v", n, err)
	}
	if store.State[repo.StateBackfillDone] == "" {
		t.Fatal("backfill flag not set")
	}
	n, _ = ingest.Backfill(context.Background(), c, mc, 90)
	if n != 0 {
		t.Fatal("backfill must run once")
	}
	store2 := testutil.NewMem()
	music.Fail = true
	if _, err := ingest.Backfill(context.Background(), &ingest.Consumer{Store: store2, Log: log}, mc, 90); err == nil || store2.State[repo.StateBackfillDone] != "" {
		t.Fatal("failed backfill must not set the flag")
	}
}
