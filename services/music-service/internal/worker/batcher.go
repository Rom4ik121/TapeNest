// Package worker holds music-service background jobs: the play_events batcher,
// the Navidrome catalog sync and aggregate/partition maintenance (spec §3.1 #11).
package worker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// EventStore persists batches (repo.Store).
type EventStore interface {
	InsertPlayEvents(ctx context.Context, evs []repo.PlayEvent) (int64, error)
}

// Batcher consumes music:play_events and writes play_events in batches.
// Entries are acked only after the batch is committed; a crashed worker's
// pending entries are re-read on start and claimed by others after ClaimIdle.
// Inserts are idempotent (event_id = stream id), so redelivery is harmless.
type Batcher struct {
	RDB       redis.UniversalClient
	Store     EventStore
	Consumer  string
	BatchSize int64
	Block     time.Duration
	ClaimIdle time.Duration
	Log       *slog.Logger
}

// Run blocks until ctx is done.
func (b *Batcher) Run(ctx context.Context) {
	if b.BatchSize == 0 {
		b.BatchSize = 500
	}
	if b.Block == 0 {
		b.Block = time.Second
	}
	if b.ClaimIdle == 0 {
		b.ClaimIdle = time.Minute
	}
	for ctx.Err() == nil {
		if err := b.ensureGroup(ctx); err != nil {
			b.Log.Warn("play events: group unavailable", "err", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		break
	}
	pending := true // own pending entries first (after a restart)
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) > b.ClaimIdle {
			b.claim(ctx)
			lastClaim = time.Now()
		}
		start := ">"
		if pending {
			start = "0"
		}
		res, err := b.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: mq.BatcherGroup, Consumer: b.Consumer, Streams: []string{mq.PlayEventsStream, start},
			Count: b.BatchSize, Block: b.Block,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			if strings.Contains(err.Error(), "NOGROUP") {
				_ = b.ensureGroup(ctx)
				continue
			}
			b.Log.Warn("play events: read failed", "err", err)
			sleep(ctx, time.Second)
			continue
		}
		var msgs []redis.XMessage
		for _, s := range res {
			msgs = append(msgs, s.Messages...)
		}
		if pending && len(msgs) == 0 {
			pending = false
			continue
		}
		if err := b.Flush(ctx, msgs); err != nil {
			b.Log.Warn("play events: batch failed, will retry", "n", len(msgs), "err", err)
			pending = true // re-read our pending entries
			sleep(ctx, 2*time.Second)
		}
	}
}

// Flush stores one batch and acks it (bad entries are acked and dropped).
func (b *Batcher) Flush(ctx context.Context, msgs []redis.XMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	evs := make([]repo.PlayEvent, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
		p, err := mq.Parse(m)
		if err != nil {
			b.Log.Warn("play events: dropping bad entry", "id", m.ID, "err", err)
			continue
		}
		evs = append(evs, repo.PlayEvent{EventID: p.ID, UserID: p.UserID, TrackID: p.TrackID, PlayedAt: p.PlayedAt, PositionSec: p.PositionSec, Completed: p.Completed})
	}
	n, err := b.Store.InsertPlayEvents(ctx, evs)
	if err != nil {
		return err
	}
	if err := b.RDB.XAck(ctx, mq.PlayEventsStream, mq.BatcherGroup, ids...).Err(); err != nil {
		return err
	}
	b.Log.Debug("play events batch stored", "read", len(msgs), "inserted", n)
	return nil
}

func (b *Batcher) ensureGroup(ctx context.Context) error {
	err := b.RDB.XGroupCreateMkStream(ctx, mq.PlayEventsStream, mq.BatcherGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// claim takes over entries another (dead) consumer left pending for > ClaimIdle.
func (b *Batcher) claim(ctx context.Context) {
	msgs, _, err := b.RDB.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: mq.PlayEventsStream, Group: mq.BatcherGroup, Consumer: b.Consumer,
		MinIdle: b.ClaimIdle, Start: "0-0", Count: b.BatchSize,
	}).Result()
	if err != nil || len(msgs) == 0 {
		return
	}
	if err := b.Flush(ctx, msgs); err != nil {
		b.Log.Warn("play events: claimed batch failed", "n", len(msgs), "err", err)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
