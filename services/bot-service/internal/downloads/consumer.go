package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stream and consumer group (download-service publishes, bot-service consumes).
const (
	Stream = "download:events"
	Group  = "bot-service"
)

// Consumer reads download:events with a consumer group: pending entries of this
// consumer first (restart), then new ones; entries of dead consumers are
// reclaimed after ClaimIdle. Every entry is acked after handling (failures are
// retried a few times, then dropped with a log line — no poison loops).
type Consumer struct {
	Redis     redis.UniversalClient
	Name      string
	Handle    func(ctx context.Context, e Event) error
	Log       *slog.Logger
	Block     time.Duration
	ClaimIdle time.Duration
	Retries   int
}

// Run blocks until ctx is done.
func (c *Consumer) Run(ctx context.Context) {
	if c.Block == 0 {
		c.Block = 5 * time.Second
	}
	if c.ClaimIdle == 0 {
		c.ClaimIdle = 2 * time.Minute
	}
	if c.Retries == 0 {
		c.Retries = 3
	}
	for ctx.Err() == nil {
		if err := c.ensureGroup(ctx); err != nil {
			c.Log.Warn("download events: group unavailable", "err", err)
			sleep(ctx, 3*time.Second)
			continue
		}
		break
	}
	pending := true
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) > c.ClaimIdle {
			lastClaim = time.Now()
			c.claim(ctx)
		}
		id, block := ">", c.Block
		if pending {
			id, block = "0", -1
		}
		res, err := c.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: c.Name, Streams: []string{Stream, id}, Count: 10, Block: block}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				c.Log.Warn("download events: read failed", "err", err)
				if strings.Contains(err.Error(), "NOGROUP") {
					_ = c.ensureGroup(ctx)
				}
				sleep(ctx, 2*time.Second)
			}
			continue
		}
		n := 0
		for _, s := range res {
			for _, m := range s.Messages {
				n++
				c.process(ctx, m)
			}
		}
		if pending && n == 0 {
			pending = false
		}
	}
}

func (c *Consumer) ensureGroup(ctx context.Context) error {
	// "$": events published before the first bot-service start are not replayed
	err := c.Redis.XGroupCreateMkStream(ctx, Stream, Group, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (c *Consumer) claim(ctx context.Context) {
	msgs, _, err := c.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: Stream, Group: Group, Consumer: c.Name, MinIdle: c.ClaimIdle, Start: "0", Count: 50}).Result()
	if err != nil {
		return
	}
	for _, m := range msgs {
		c.process(ctx, m)
	}
}

func (c *Consumer) process(ctx context.Context, m redis.XMessage) {
	ack := true
	defer func() {
		if ack { // on shutdown the entry stays pending and is re-read on restart
			_ = c.Redis.XAck(context.WithoutCancel(ctx), Stream, Group, m.ID).Err()
		}
	}()
	raw, _ := m.Values["data"].(string)
	var e Event
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		c.Log.Warn("download events: bad entry", "id", m.ID)
		return
	}
	for attempt := 1; ; attempt++ {
		hctx, cancel := context.WithTimeout(ctx, 12*time.Minute) // uploads of ~50 MB
		err := c.Handle(hctx, e)
		cancel()
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			ack = false
			return
		}
		if attempt >= c.Retries {
			c.Log.Error("download event delivery failed", "job", e.JobID, "type", e.Type, "chat_id", e.ChatID, "err", err)
			return
		}
		sleep(ctx, time.Duration(attempt)*time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
