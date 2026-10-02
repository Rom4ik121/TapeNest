package mq

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func TestQueuePriorityAckDepth(t *testing.T) {
	_, rdb := newRedis(t)
	ctx := context.Background()
	q, err := NewQueue(ctx, rdb, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewQueue(ctx, rdb, "w2"); err != nil { // BUSYGROUP is fine
		t.Fatal(err)
	}
	q.block = 20 * time.Millisecond
	low, normal, high := uuid.New(), uuid.New(), uuid.New()
	for id, p := range map[uuid.UUID]domain.Priority{low: domain.PriorityLow, normal: domain.PriorityNormal, high: domain.PriorityHigh} {
		if err := q.Enqueue(ctx, id, p); err != nil {
			t.Fatal(err)
		}
	}
	d, err := q.Depth(ctx)
	if err != nil || d["high"] != 1 || d["normal"] != 1 || d["low"] != 1 || d["delayed"] != 0 {
		t.Fatalf("depth = %v %v", d, err)
	}
	for _, want := range []uuid.UUID{high, normal, low} {
		m, err := q.Next(ctx)
		if err != nil || m == nil || m.JobID != want {
			t.Fatalf("next = %+v %v, want %s", m, err, want)
		}
		if err := q.Ack(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	m, err := q.Next(ctx) // blocking read times out
	if err != nil || m != nil {
		t.Fatalf("empty next = %+v %v", m, err)
	}
	d, _ = q.Depth(ctx)
	if d["high"]+d["normal"]+d["low"] != 0 {
		t.Fatalf("acked entries must be deleted: %v", d)
	}
}

func TestQueueBlockingNext(t *testing.T) {
	_, rdb := newRedis(t)
	ctx := context.Background()
	q, _ := NewQueue(ctx, rdb, "w1")
	q.block = 2 * time.Second
	id := uuid.New()
	go func() { time.Sleep(100 * time.Millisecond); _ = q.Enqueue(ctx, id, domain.PriorityNormal) }()
	m, err := q.Next(ctx)
	if err != nil || m == nil || m.JobID != id {
		t.Fatalf("next = %+v %v", m, err)
	}
}

func TestQueueDelayedAndReclaim(t *testing.T) {
	_, rdb := newRedis(t)
	ctx := context.Background()
	q, _ := NewQueue(ctx, rdb, "w1")
	q.block = 10 * time.Millisecond
	now := time.Now()
	due, later := uuid.New(), uuid.New()
	_ = q.EnqueueAt(ctx, due, domain.PriorityLow, now.Add(-time.Second))
	_ = q.EnqueueAt(ctx, later, domain.PriorityLow, now.Add(time.Hour))
	rdb.ZAdd(ctx, delayedKey, redis.Z{Score: 1, Member: "garbage|low"})
	n, err := q.PromoteDue(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("promoted %d %v", n, err)
	}
	d, _ := q.Depth(ctx)
	if d["low"] != 1 || d["delayed"] != 1 {
		t.Fatalf("depth = %v", d)
	}
	m, _ := q.Next(ctx) // taken by w1 but never acked (crash)
	if m == nil || m.JobID != due {
		t.Fatalf("next = %+v", m)
	}
	q2, _ := NewQueue(ctx, rdb, "w2")
	time.Sleep(20 * time.Millisecond)
	msgs, err := q2.Reclaim(ctx, 10*time.Millisecond)
	if err != nil || len(msgs) != 1 || msgs[0].JobID != due {
		t.Fatalf("reclaim = %+v %v", msgs, err)
	}
	if StreamName(domain.PriorityHigh) != "download:q:high" {
		t.Fatal(StreamName(domain.PriorityHigh))
	}
}

func TestQueueErrors(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	q, _ := NewQueue(ctx, rdb, "w1")
	mr.Close()
	id := uuid.New()
	if q.Enqueue(ctx, id, domain.PriorityLow) == nil || q.EnqueueAt(ctx, id, domain.PriorityLow, time.Now()) == nil {
		t.Fatal("expected errors")
	}
	if _, err := q.PromoteDue(ctx, time.Now()); err == nil {
		t.Fatal("expected promote error")
	}
	if _, err := q.Next(ctx); err == nil {
		t.Fatal("expected next error")
	}
	if _, err := q.Depth(ctx); err == nil {
		t.Fatal("expected depth error")
	}
	if _, err := q.Reclaim(ctx, time.Second); err == nil {
		t.Fatal("expected reclaim error")
	}
	if err := q.Ack(ctx, &Message{Stream: "s", ID: "1-1"}); err == nil {
		t.Fatal("expected ack error")
	}
	if _, err := NewQueue(ctx, rdb, "w"); err == nil {
		t.Fatal("expected group error")
	}
}

func TestSemaphores(t *testing.T) {
	_, rdb := newRedis(t)
	ctx := context.Background()
	s := NewSemaphores(rdb, map[domain.Source]int{domain.SourceYouTube: 2}, time.Minute)
	for i, want := range []bool{true, true, false} {
		ok, err := s.Acquire(ctx, domain.SourceYouTube, uuid.NewString())
		if err != nil || ok != want {
			t.Fatalf("acquire %d = %v %v", i, ok, err)
		}
	}
	if ok, _ := s.Acquire(ctx, domain.SourceVK, "x"); !ok {
		t.Fatal("unlimited source must pass")
	}
	holder := "h"
	rdb.Del(ctx, semKey(domain.SourceYouTube))
	if ok, _ := s.Acquire(ctx, domain.SourceYouTube, holder); !ok {
		t.Fatal("slot expected")
	}
	if err := s.Release(ctx, domain.SourceYouTube, holder); err != nil {
		t.Fatal(err)
	}
	if n := rdb.ZCard(ctx, semKey(domain.SourceYouTube)).Val(); n != 0 {
		t.Fatalf("released: %d", n)
	}
	// expired holders are evicted
	exp := NewSemaphores(rdb, map[domain.Source]int{domain.SourceRuTube: 1}, -time.Second)
	_, _ = exp.Acquire(ctx, domain.SourceRuTube, "old")
	if ok, _ := exp.Acquire(ctx, domain.SourceRuTube, "new"); !ok {
		t.Fatal("expired slot must be reclaimed")
	}
}

func TestSemaphoreErrors(t *testing.T) {
	mr, rdb := newRedis(t)
	s := NewSemaphores(rdb, map[domain.Source]int{domain.SourceYouTube: 1}, time.Minute)
	mr.Close()
	if _, err := s.Acquire(context.Background(), domain.SourceYouTube, "h"); err == nil {
		t.Fatal("expected error")
	}
	if err := s.Release(context.Background(), domain.SourceYouTube, "h"); err == nil {
		t.Fatal("expected error")
	}
}

func TestBusProgressPublishDedup(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	b := NewBus(rdb)
	id := uuid.New()
	if p, err := b.Progress(ctx, id); p != nil || err != nil {
		t.Fatalf("unknown progress = %v %v", p, err)
	}
	sub := b.Subscribe(ctx, id)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.SetProgress(ctx, id, domain.Progress{Stage: domain.StageDownloading, Pct: 42}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Progress(ctx, id)
	if err != nil || p == nil || p.Pct != 42 {
		t.Fatalf("progress = %+v %v", p, err)
	}
	msg, err := sub.ReceiveMessage(ctx)
	if err != nil || msg.Channel != JobChannel(id) {
		t.Fatalf("pubsub = %v %v", msg, err)
	}
	mr.Set(progressKey(id), "{bad")
	if p, err := b.Progress(ctx, id); p != nil || err != nil {
		t.Fatal("corrupt progress must be nil")
	}

	// web job: channel only; bot job: channel + events stream
	if err := b.Publish(ctx, Event{Type: EventProgress, JobID: id}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, Event{Type: EventDownloaded, JobID: id, ChatID: 7, File: &FileInfo{SizeBytes: 5}}); err != nil {
		t.Fatal(err)
	}
	xs := rdb.XRange(ctx, EventsStream, "-", "+").Val()
	if len(xs) != 1 || xs[0].Values["type"] != EventDownloaded {
		t.Fatalf("events = %+v", xs)
	}
	var e Event
	if err := json.Unmarshal([]byte(xs[0].Values["data"].(string)), &e); err != nil || e.ChatID != 7 || e.File.SizeBytes != 5 || e.At.IsZero() {
		t.Fatalf("event = %+v %v", e, err)
	}

	mid := uuid.New()
	if got, _ := b.CachedMedia(ctx, "h"); got != uuid.Nil {
		t.Fatal("miss expected")
	}
	_ = b.CacheMedia(ctx, "h", mid)
	if got, _ := b.CachedMedia(ctx, "h"); got != mid {
		t.Fatal("hit expected")
	}
	mr.Set(dedupPrefix+"bad", "nope")
	if got, err := b.CachedMedia(ctx, "bad"); got != uuid.Nil || err != nil {
		t.Fatal("corrupt = miss")
	}
	_ = b.ForgetMedia(ctx, "h")
	if got, _ := b.CachedMedia(ctx, "h"); got != uuid.Nil {
		t.Fatal("forgotten")
	}
	mr.Close()
	if b.SetProgress(ctx, id, domain.Progress{}) == nil || b.Publish(ctx, Event{JobID: id}) == nil || b.CacheMedia(ctx, "h", mid) == nil {
		t.Fatal("expected errors")
	}
	if _, err := b.Progress(ctx, id); err == nil {
		t.Fatal("expected error")
	}
	if _, err := b.CachedMedia(ctx, "h"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlansAndLocks(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	p := NewPlans(rdb)
	id := uuid.New()
	if pl, err := p.Get(ctx, id); err != nil || pl.Tier != "" {
		t.Fatalf("empty plan = %+v %v", pl, err)
	}
	want := Plan{Attempt: domain.Attempt{Tier: domain.TierResidential, WithCookies: true}, LastProxy: "http://p"}
	if err := p.Set(ctx, id, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Get(ctx, id); got != want {
		t.Fatalf("plan = %+v", got)
	}
	mr.Set(planKey(id), "{bad")
	if got, _ := p.Get(ctx, id); got.Tier != "" {
		t.Fatal("corrupt plan must be empty")
	}
	if err := p.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}

	l := NewLocks(rdb)
	unlock, ok, err := l.Lock(ctx, "k", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lock = %v %v", ok, err)
	}
	if _, ok2, err := l.Lock(ctx, "k", time.Minute); err != nil || ok2 {
		t.Fatalf("second lock = %v %v", ok2, err)
	}
	unlock()
	unlock2, ok, _ := l.Lock(ctx, "k", time.Minute)
	if !ok {
		t.Fatal("lock after unlock")
	}
	unlock2()

	mr.Close()
	if _, err := p.Get(ctx, id); err == nil {
		t.Fatal("expected error")
	}
	if p.Set(ctx, id, want) == nil {
		t.Fatal("expected error")
	}
}
