package service

import (
	"context"
	"log/slog"
	"math"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/mq"
)

// Events accepts listen events: validate → Redis Streams → 204 immediately; the
// worker's batcher writes play_events in batches (spec §5.4).
type Events struct{ rdb redis.UniversalClient }

// NewEvents creates the events service.
func NewEvents(rdb redis.UniversalClient) *Events { return &Events{rdb: rdb} }

// TrackListened publishes one event.
func (e *Events) TrackListened(ctx context.Context, user, track uuid.UUID, pos float64, completed bool) error {
	if math.IsNaN(pos) || pos < 0 || pos > domain.MaxPositionSec {
		return domain.Invalid("positionSec must be between 0 and %d", domain.MaxPositionSec)
	}
	return mq.Publish(ctx, e.rdb, mq.PlayEvent{UserID: user, TrackID: track, PositionSec: pos, Completed: completed})
}

// TrackSkipped publishes a skip (the track was left before the listen event):
// only the user-event stream (reco profile), never play_events.
func (e *Events) TrackSkipped(ctx context.Context, user, track uuid.UUID, pos float64) error {
	if math.IsNaN(pos) || pos < 0 || pos > domain.MaxPositionSec {
		return domain.Invalid("positionSec must be between 0 and %d", domain.MaxPositionSec)
	}
	return mq.PublishUserEvent(ctx, e.rdb, mq.UserEvent{Kind: mq.KindSkip, UserID: user, TrackID: track, PositionSec: pos})
}

// UserEventPublisher returns a best-effort publisher: domain events feed the
// recommender, so a Redis hiccup is logged but never fails the user's request.
func UserEventPublisher(rdb redis.UniversalClient, log *slog.Logger) func(ctx context.Context, e mq.UserEvent) {
	return func(ctx context.Context, e mq.UserEvent) {
		if err := mq.PublishUserEvent(ctx, rdb, e); err != nil {
			log.Warn("user event not published", "kind", e.Kind, "err", err)
		}
	}
}
