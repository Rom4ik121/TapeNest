package mq_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/mq"
)

func TestPublishParse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	e := mq.PlayEvent{UserID: uuid.New(), TrackID: uuid.New(), PositionSec: 12.34, Completed: true}
	if err := mq.Publish(ctx, rdb, e); err != nil {
		t.Fatal(err)
	}
	msgs, _ := rdb.XRange(ctx, mq.PlayEventsStream, "-", "+").Result()
	p, err := mq.Parse(msgs[0])
	if err != nil || p.UserID != e.UserID || p.TrackID != e.TrackID || p.PositionSec != 12.3 || !p.Completed || p.PlayedAt.IsZero() || p.ID != msgs[0].ID {
		t.Fatalf("parse: %+v %v", p, err)
	}
	if _, err := mq.Parse(redis.XMessage{ID: "x", Values: map[string]any{"u": "1"}}); !errors.Is(err, mq.ErrBadEntry) {
		t.Fatalf("bad: %v", err)
	}
	mr.Close()
	if err := mq.Publish(ctx, rdb, e); err == nil {
		t.Fatal("redis down")
	}
}
