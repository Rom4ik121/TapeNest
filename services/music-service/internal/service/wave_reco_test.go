package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/reco"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
)

// fakeReco returns tracks from a fixed list minus the excluded ones.
type fakeReco struct {
	mu    sync.Mutex
	ids   []uuid.UUID
	err   error
	reqs  []reco.NextRequest
	state string
}

func (f *fakeReco) Next(_ context.Context, req reco.NextRequest) ([]reco.Pick, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	ex := map[uuid.UUID]bool{}
	for _, id := range req.Exclude {
		ex[id] = true
	}
	var out []reco.Pick
	for _, id := range f.ids {
		if !ex[id] && len(out) < req.Limit {
			out = append(out, reco.Pick{TrackID: id, Source: "cf", Reason: &reco.Reason{Kind: "because_you_liked", RefTitle: "X"}})
		}
	}
	return out, nil
}

func (f *fakeReco) State() string { return f.state }

func newWave(t *testing.T, n int) (*service.Wave, *testutil.Mem, *fakeReco, *redis.Client, *[]mq.UserEvent) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mem := testutil.NewMem(n)
	f := &fakeReco{state: "closed"}
	for _, tr := range mem.Tracks {
		f.ids = append(f.ids, tr.ID)
	}
	var mu sync.Mutex
	events := &[]mq.UserEvent{}
	pub := func(_ context.Context, e mq.UserEvent) {
		mu.Lock()
		defer mu.Unlock()
		*events = append(*events, e)
	}
	w := service.NewWave(mem, rdb).WithReco(f).WithEvents(pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w, mem, f, rdb, events
}

func TestWaveReco(t *testing.T) {
	w, _, f, _, events := newWave(t, 15)
	ctx := context.Background()
	u := uuid.New()
	if w.RecoState() != "closed" {
		t.Fatal("state")
	}
	id, b, err := w.Start(ctx, u, "calm")
	if err != nil || b.Strategy != domain.StrategyReco || b.Mode != "calm" || len(b.Tracks) != 10 {
		t.Fatalf("start %+v %v", b, err)
	}
	if b.Tracks[0].Reason == nil || b.Tracks[0].Reason.Kind != "because_you_liked" || b.Tracks[0].Reason.RefTitle != "X" {
		t.Fatalf("reason %+v", b.Tracks[0].Reason)
	}
	first := b.Tracks[0].ID
	if err := w.Feedback(ctx, u, id, first, "like"); err != nil {
		t.Fatal(err)
	}
	if err := w.Feedback(ctx, u, id, b.Tracks[1].ID, "skip"); err != nil {
		t.Fatal(err)
	}
	if err := w.Feedback(ctx, u, id, uuid.New(), "skip"); err != nil { // not served: ignored
		t.Fatal(err)
	}
	if len(*events) != 2 || (*events)[0].Kind != mq.KindWaveLike || (*events)[0].Source != "cf" || (*events)[1].Kind != mq.KindWaveSkip {
		t.Fatalf("events %+v", *events)
	}
	b2, err := w.Next(ctx, u, id)
	if err != nil || len(b2.Tracks) != 5 || b2.Mode != "calm" {
		t.Fatalf("next %d %v", len(b2.Tracks), err)
	}
	last := f.reqs[len(f.reqs)-1]
	if len(last.Exclude) != 10 || len(last.Recent) != 10 || len(last.Feedback) != 2 || last.Feedback[0].Action != "like" || last.Mode != "calm" {
		t.Fatalf("request %+v", last)
	}
	// catalog exhausted → reco returns nothing → new round excluding the last batch-size tracks
	b3, err := w.Next(ctx, u, id)
	if err != nil || b3.Strategy != domain.StrategyReco || len(b3.Tracks) != 5 { // 15 − the 10 most recent
		t.Fatalf("new round %d %v", len(b3.Tracks), err)
	}
	for _, tr := range b3.Tracks {
		for _, p := range b2.Tracks {
			if tr.ID == p.ID {
				t.Fatal("new round repeats the last batch")
			}
		}
	}
	var inv *domain.InvalidError
	if _, _, err := w.Start(ctx, u, "party"); !errors.As(err, &inv) {
		t.Fatalf("bad mode: %v", err)
	}
}

func TestWaveRecoFallback(t *testing.T) {
	w, _, f, _, _ := newWave(t, 15)
	ctx := context.Background()
	f.err = errors.New("reco: timeout")
	f.state = "open"
	_, b, err := w.Start(ctx, uuid.New(), "")
	if err != nil || b.Strategy != domain.StrategyFallback || len(b.Tracks) != 10 || b.Mode != "default" {
		t.Fatalf("fallback %+v %v", b, err)
	}
	for _, tr := range b.Tracks {
		if tr.Reason == nil || tr.Reason.Kind == "" {
			t.Fatal("heuristic reasons missing")
		}
	}
	// reco healthy but returns nothing for a fresh session → heuristic
	f.err, f.ids = nil, nil
	if _, b, _ := w.Start(ctx, uuid.New(), ""); b.Strategy != domain.StrategyFallback || len(b.Tracks) == 0 {
		t.Fatalf("empty reco: %+v", b)
	}
	if service.NewWave(testutil.NewMem(1), nil).RecoState() != "disabled" {
		t.Fatal("disabled state")
	}
}

func TestNormalizeMode(t *testing.T) {
	for in, want := range map[string]string{"": "default", "energetic": "energetic", "discover": "discover", "favorites": "favorites"} {
		if got, err := service.NormalizeMode(in); err != nil || got != want {
			t.Fatalf("%q → %q %v", in, got, err)
		}
	}
}

func TestUserEventPublisher(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := service.UserEventPublisher(rdb, log)
	u, tr := uuid.New(), uuid.New()
	pub(context.Background(), mq.UserEvent{Kind: mq.KindWaveSkip, UserID: u, TrackID: tr, SessionID: uuid.New(), Source: "als"})
	lib := service.NewLibrary(testutil.NewMem(3)).WithEvents(pub)
	_ = lib
	ev := service.NewEvents(rdb)
	if err := ev.TrackSkipped(context.Background(), u, tr, 12.5); err != nil {
		t.Fatal(err)
	}
	var inv *domain.InvalidError
	if err := ev.TrackSkipped(context.Background(), u, tr, -1); !errors.As(err, &inv) {
		t.Fatal("negative position")
	}
	msgs, _ := rdb.XRange(context.Background(), mq.UserEventsStream, "-", "+").Result()
	if len(msgs) != 2 || msgs[0].Values["src"] != "als" || msgs[1].Values["k"] != "skip" || msgs[1].Values["p"] != "12.5" {
		t.Fatalf("stream %+v", msgs)
	}
	mr.Close()
	pub(context.Background(), mq.UserEvent{Kind: mq.KindLike, UserID: u, TrackID: tr}) // best effort, no panic
}
