package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
)

func TestWorkerProcessesQueue(t *testing.T) {
	e := newEnv(t)
	j := queued(t, e, zoo, nil)
	j2 := queued(t, e, "https://youtu.be/ddddddddddd", nil)
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{Queue: e.queue, Proc: e.proc, Concurrency: 2, ReclaimIdle: time.Hour, Log: e.log, Tick: 20 * time.Millisecond}
	done := make(chan struct{})
	go func() { w.Run(ctx, time.Second); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.store.Job(j.ID).Status == domain.StatusDone && e.store.Job(j2.ID).Status == domain.StatusDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
	if e.store.Job(j.ID).Status != domain.StatusDone || e.store.Job(j2.ID).Status != domain.StatusDone {
		t.Fatal("jobs not processed")
	}
	d, _ := e.queue.Depth(context.Background())
	if d["normal"] != 0 {
		t.Fatalf("entries must be acked: %v", d)
	}
}

// scripted consumer/processor for edge cases
type fakeConsumer struct {
	mu       sync.Mutex
	msgs     []*mq.Message
	acked    []string
	nextErr  error
	reclaim  []*mq.Message
	promoted int
}

func (f *fakeConsumer) Next(ctx context.Context) (*mq.Message, error) {
	f.mu.Lock()
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		f.mu.Unlock()
		return nil, err
	}
	if len(f.msgs) > 0 {
		m := f.msgs[0]
		f.msgs = f.msgs[1:]
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Millisecond):
	}
	return nil, nil
}

func (f *fakeConsumer) Ack(_ context.Context, m *mq.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, m.ID)
	return nil
}

func (f *fakeConsumer) PromoteDue(context.Context, time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promoted++
	return 0, errBoom
}

func (f *fakeConsumer) Reclaim(context.Context, time.Duration) ([]*mq.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.reclaim
	f.reclaim = nil
	return r, nil
}

type fakeProc struct {
	mu    sync.Mutex
	seen  []uuid.UUID
	err   error
	block time.Duration
}

func (p *fakeProc) Process(ctx context.Context, id uuid.UUID) error {
	p.mu.Lock()
	p.seen = append(p.seen, id)
	p.mu.Unlock()
	if p.block > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(p.block):
		}
	}
	return p.err
}

func TestWorkerEdgeCases(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	good, bad := uuid.New(), uuid.New()
	fc := &fakeConsumer{
		msgs:    []*mq.Message{{ID: "1", JobID: uuid.Nil}, {ID: "2", JobID: good}},
		reclaim: []*mq.Message{{ID: "3", JobID: bad}},
	}
	fp := &fakeProc{}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{Queue: fc, Proc: fp, Concurrency: 1, ReclaimIdle: time.Minute, Log: log, Tick: 10 * time.Millisecond}
	done := make(chan struct{})
	go func() { w.Run(ctx, time.Second); close(done) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.acked) != 3 || fc.promoted == 0 {
		t.Fatalf("acked = %v promoted = %d", fc.acked, fc.promoted)
	}

	// processing error → not acked; read error → retried; drain timeout cancels jobs
	fc2 := &fakeConsumer{msgs: []*mq.Message{{ID: "9", JobID: good}}, nextErr: errBoom}
	fp2 := &fakeProc{err: errBoom}
	ctx2, cancel2 := context.WithCancel(context.Background())
	w2 := &Worker{Queue: fc2, Proc: fp2, Concurrency: 1, Log: log}
	go func() { time.Sleep(2500 * time.Millisecond); cancel2() }()
	w2.Run(ctx2, time.Second)
	if len(fc2.acked) != 0 || len(fp2.seen) != 1 {
		t.Fatalf("acked = %v seen = %v", fc2.acked, fp2.seen)
	}

	fc3 := &fakeConsumer{msgs: []*mq.Message{{ID: "10", JobID: good}}}
	fp3 := &fakeProc{block: time.Hour}
	ctx3, cancel3 := context.WithCancel(context.Background())
	w3 := &Worker{Queue: fc3, Proc: fp3, Concurrency: 1, Log: log}
	go func() { time.Sleep(100 * time.Millisecond); cancel3() }()
	start := time.Now()
	w3.Run(ctx3, 100*time.Millisecond)
	if time.Since(start) > 3*time.Second {
		t.Fatal("drain timeout not applied")
	}
}
