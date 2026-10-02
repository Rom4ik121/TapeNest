// Package mq holds the Redis side of download-service (spec §5.3): the job queue
// on Redis Streams (high/normal/low), delayed retries, per-domain semaphores,
// the dedup cache, progress state and the event stream consumed by bot-service.
package mq

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Redis keys.
const (
	streamPrefix = "download:q:"
	delayedKey   = "download:q:delayed"
	group        = "workers"
)

// StreamName returns the stream key of a priority.
func StreamName(p domain.Priority) string { return streamPrefix + string(p) }

// Message is one queue entry.
type Message struct {
	Stream string
	ID     string
	JobID  uuid.UUID
}

// Queue is the job queue.
type Queue struct {
	rdb      redis.UniversalClient
	consumer string
	block    time.Duration
}

// NewQueue creates the consumer groups (idempotent). consumer names this worker process.
func NewQueue(ctx context.Context, rdb redis.UniversalClient, consumer string) (*Queue, error) {
	for _, p := range domain.Priorities {
		err := rdb.XGroupCreateMkStream(ctx, StreamName(p), group, "0").Err()
		if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			return nil, fmt.Errorf("create group %s: %w", p, err)
		}
	}
	return &Queue{rdb: rdb, consumer: consumer, block: 2 * time.Second}, nil
}

// Enqueue adds a job to the priority stream.
func (q *Queue) Enqueue(ctx context.Context, id uuid.UUID, p domain.Priority) error {
	if err := q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: StreamName(p), Values: map[string]any{"job": id.String()}}).Err(); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}

// EnqueueAt schedules a job for later (retries with backoff, busy domain).
func (q *Queue) EnqueueAt(ctx context.Context, id uuid.UUID, p domain.Priority, at time.Time) error {
	member := id.String() + "|" + string(p)
	if err := q.rdb.ZAdd(ctx, delayedKey, redis.Z{Score: float64(at.UnixMilli()), Member: member}).Err(); err != nil {
		return fmt.Errorf("enqueue delayed: %w", err)
	}
	return nil
}

// PromoteDue moves due delayed jobs into their streams; returns how many moved.
// Safe with several workers: ZREM decides who moves an entry.
func (q *Queue) PromoteDue(ctx context.Context, now time.Time) (int, error) {
	due, err := q.rdb.ZRangeByScore(ctx, delayedKey, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10), Count: 100}).Result()
	if err != nil {
		return 0, fmt.Errorf("delayed range: %w", err)
	}
	moved := 0
	for _, m := range due {
		n, err := q.rdb.ZRem(ctx, delayedKey, m).Result()
		if err != nil {
			return moved, fmt.Errorf("delayed rem: %w", err)
		}
		if n == 0 {
			continue
		}
		idStr, prio, _ := strings.Cut(m, "|")
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		if err := q.Enqueue(ctx, id, domain.Priority(prio)); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// Next returns the next message, polling high → normal → low first and then
// blocking on all streams for up to the block interval. (nil, nil) on timeout.
func (q *Queue) Next(ctx context.Context) (*Message, error) {
	for _, p := range domain.Priorities {
		msg, err := q.read(ctx, []string{StreamName(p), ">"}, -1)
		if msg != nil || err != nil {
			return msg, err
		}
	}
	streams := make([]string, 0, 2*len(domain.Priorities))
	for _, p := range domain.Priorities {
		streams = append(streams, StreamName(p))
	}
	for range domain.Priorities {
		streams = append(streams, ">")
	}
	return q.read(ctx, streams, q.block)
}

func (q *Queue) read(ctx context.Context, streams []string, block time.Duration) (*Message, error) {
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: q.consumer, Streams: streams, Count: 1, Block: block}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}
	for _, s := range res {
		for _, m := range s.Messages {
			return parse(s.Stream, m), nil
		}
	}
	return nil, nil
}

func parse(stream string, m redis.XMessage) *Message {
	raw, _ := m.Values["job"].(string)
	id, _ := uuid.Parse(raw)
	return &Message{Stream: stream, ID: m.ID, JobID: id}
}

// Ack confirms processing (also for malformed entries).
func (q *Queue) Ack(ctx context.Context, m *Message) error {
	if err := q.rdb.XAck(ctx, m.Stream, group, m.ID).Err(); err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	return q.rdb.XDel(ctx, m.Stream, m.ID).Err()
}

// Reclaim takes over entries pending longer than minIdle (a crashed worker's jobs).
func (q *Queue) Reclaim(ctx context.Context, minIdle time.Duration) ([]*Message, error) {
	var out []*Message
	for _, p := range domain.Priorities {
		msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: StreamName(p), Group: group, Consumer: q.consumer, MinIdle: minIdle, Start: "0", Count: 10}).Result()
		if err != nil {
			return out, fmt.Errorf("xautoclaim: %w", err)
		}
		for _, m := range msgs {
			out = append(out, parse(StreamName(p), m))
		}
	}
	return out, nil
}

// Depth returns queued entries per priority plus delayed ones (metrics, readyz).
func (q *Queue) Depth(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, p := range domain.Priorities {
		n, err := q.rdb.XLen(ctx, StreamName(p)).Result()
		if err != nil {
			return nil, fmt.Errorf("xlen: %w", err)
		}
		out[string(p)] = n
	}
	n, err := q.rdb.ZCard(ctx, delayedKey).Result()
	if err != nil {
		return nil, fmt.Errorf("zcard: %w", err)
	}
	out["delayed"] = n
	return out, nil
}
