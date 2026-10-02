package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/mq"
)

// Consumer is the worker's view of the queue (mq.Queue).
type Consumer interface {
	Next(ctx context.Context) (*mq.Message, error)
	Ack(ctx context.Context, m *mq.Message) error
	PromoteDue(ctx context.Context, now time.Time) (int, error)
	Reclaim(ctx context.Context, minIdle time.Duration) ([]*mq.Message, error)
}

// JobProcessor processes one job (Processor).
type JobProcessor interface {
	Process(ctx context.Context, id uuid.UUID) error
}

// Worker runs bounded-concurrency consumers plus the delayed-queue mover and the
// reclaimer (spec §7: bounded worker pools, graceful shutdown with drain).
type Worker struct {
	Queue       Consumer
	Proc        JobProcessor
	Concurrency int
	// ReclaimIdle: entries unacked this long belong to a dead worker.
	ReclaimIdle time.Duration
	Log         *slog.Logger
	Tick        time.Duration // delayed-queue poll interval
}

// Run blocks until ctx is cancelled. New jobs stop immediately; in-flight jobs
// get `drain` to finish, then their context is cancelled (they re-queue themselves).
func (w *Worker) Run(ctx context.Context, drain time.Duration) {
	if w.Tick == 0 {
		w.Tick = time.Second
	}
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.consume(ctx, jobCtx)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.housekeeping(ctx)
	}()
	<-ctx.Done()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drain):
		w.Log.Warn("drain timeout: cancelling in-flight jobs")
		cancelJobs()
		<-done
	}
}

func (w *Worker) consume(ctx, jobCtx context.Context) {
	for ctx.Err() == nil {
		msg, err := w.Queue.Next(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.Log.Error("queue read failed", "err", err)
				sleep(ctx, 2*time.Second)
			}
			continue
		}
		if msg == nil {
			continue
		}
		w.handle(jobCtx, msg)
	}
}

func (w *Worker) handle(ctx context.Context, msg *mq.Message) {
	if msg.JobID == uuid.Nil {
		_ = w.Queue.Ack(ctx, msg)
		return
	}
	if err := w.Proc.Process(ctx, msg.JobID); err != nil {
		// not acked: the reclaimer retries it after ReclaimIdle
		w.Log.Error("job processing error", "job", msg.JobID, "err", err)
		return
	}
	if err := w.Queue.Ack(context.WithoutCancel(ctx), msg); err != nil {
		w.Log.Error("ack failed", "job", msg.JobID, "err", err)
	}
}

func (w *Worker) housekeeping(ctx context.Context) {
	t := time.NewTicker(w.Tick)
	defer t.Stop()
	lastReclaim := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if _, err := w.Queue.PromoteDue(ctx, now); err != nil && ctx.Err() == nil {
				w.Log.Error("delayed queue failed", "err", err)
			}
			if w.ReclaimIdle > 0 && now.Sub(lastReclaim) > time.Minute {
				lastReclaim = now
				msgs, err := w.Queue.Reclaim(ctx, w.ReclaimIdle)
				if err != nil && ctx.Err() == nil {
					w.Log.Error("reclaim failed", "err", err)
				}
				for _, m := range msgs {
					w.Log.Warn("reclaimed stale job", "job", m.JobID)
					w.handle(ctx, m)
				}
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
