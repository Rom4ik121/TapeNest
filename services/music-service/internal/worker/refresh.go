package worker

import (
	"context"
	"log/slog"
	"time"
)

// Scanner is the Navidrome scan control (navidrome.Client).
type Scanner interface {
	StartScan(ctx context.Context) error
	Scanning(ctx context.Context) (bool, error)
}

// Refresher runs "rescan now → wait → sync → notify" when a catalog refresh
// is requested, instead of waiting for the periodic sync.
type Refresher struct {
	Scan     Scanner
	Sync     func(ctx context.Context) error
	Publish  func(ctx context.Context) error // catalog changed (reco-worker)
	Poll     time.Duration
	MaxWait  time.Duration
	Debounce time.Duration
	Log      *slog.Logger
}

// Run performs one refresh.
func (r *Refresher) Run(ctx context.Context) error {
	poll, maxWait := r.Poll, r.MaxWait
	if poll <= 0 {
		poll = time.Second
	}
	if maxWait <= 0 {
		maxWait = 3 * time.Minute
	}
	if err := r.Scan.StartScan(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, poll) {
			return ctx.Err()
		}
		busy, err := r.Scan.Scanning(ctx)
		if err == nil && !busy {
			break
		}
	}
	if err := r.Sync(ctx); err != nil {
		return err
	}
	if r.Publish != nil {
		return r.Publish(ctx)
	}
	return nil
}

// Listen runs a refresh per burst of signals (debounced) until ctx is done.
func (r *Refresher) Listen(ctx context.Context, signals <-chan struct{}) {
	debounce := r.Debounce
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signals:
			if !ok {
				return
			}
		}
		// swallow the rest of the burst
		t := time.NewTimer(debounce)
	drain:
		for {
			select {
			case <-signals:
			case <-t.C:
				break drain
			case <-ctx.Done():
				t.Stop()
				return
			}
		}
		if err := r.Run(ctx); err != nil && ctx.Err() == nil && r.Log != nil {
			r.Log.Warn("catalog refresh failed", "err", err)
		} else if r.Log != nil {
			r.Log.Info("catalog refreshed on demand")
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
