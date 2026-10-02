// Package mq is the Redis side of music-service: the listen-event stream
// (API → Redis Streams → batcher, spec §5.4/§7.9).
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
)

// Stream and consumer group of listen events.
const (
	PlayEventsStream = "music:play_events"
	BatcherGroup     = "music-batcher"
	streamMaxLen     = 200_000
)

// PlayEvent is what the API publishes for POST /events/track-listened.
type PlayEvent struct {
	UserID      uuid.UUID
	TrackID     uuid.UUID
	PositionSec float64
	Completed   bool
}

// Publish appends a listen event (approximate MAXLEN keeps the stream bounded).
func Publish(ctx context.Context, rdb redis.UniversalClient, e PlayEvent) error {
	c := "0"
	if e.Completed {
		c = "1"
	}
	err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: PlayEventsStream, MaxLen: streamMaxLen, Approx: true,
		Values: map[string]any{"u": e.UserID.String(), "t": e.TrackID.String(), "p": strconv.FormatFloat(e.PositionSec, 'f', 1, 64), "c": c},
	}).Err()
	if err != nil {
		return fmt.Errorf("publish play event: %w", err)
	}
	return nil
}

// Parsed is a stream entry decoded for the batcher; PlayedAt comes from the entry id
// (server time, monotonic per stream) so a redelivered entry maps to the same row.
type Parsed struct {
	ID          string
	UserID      uuid.UUID
	TrackID     uuid.UUID
	PositionSec float32
	Completed   bool
	PlayedAt    time.Time
}

// ErrBadEntry marks entries that can never be stored (acked and dropped).
var ErrBadEntry = errors.New("bad play event entry")

// Parse decodes one stream message.
func Parse(m redis.XMessage) (Parsed, error) {
	get := func(k string) string { s, _ := m.Values[k].(string); return s }
	u, err1 := uuid.Parse(get("u"))
	t, err2 := uuid.Parse(get("t"))
	p, err3 := strconv.ParseFloat(get("p"), 32)
	ms, err4 := strconv.ParseInt(strings.SplitN(m.ID, "-", 2)[0], 10, 64)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return Parsed{}, fmt.Errorf("%w %s: %w", ErrBadEntry, m.ID, err)
	}
	return Parsed{ID: m.ID, UserID: u, TrackID: t, PositionSec: float32(p), Completed: get("c") == "1", PlayedAt: time.UnixMilli(ms).UTC()}, nil
}

// UserEventsStream carries domain events for reco-service (likes, playlist
// changes, wave feedback, skips). music-service only publishes; consumers own
// their consumer groups (reco-service: group "reco").
const UserEventsStream = "music:user_events"

// User event kinds.
const (
	KindLike           = "like"
	KindUnlike         = "unlike"
	KindPlaylistAdd    = "playlist_add"
	KindPlaylistRemove = "playlist_remove"
	KindWaveLike       = "wave_like"
	KindWaveSkip       = "wave_skip"
	KindSkip           = "skip"
)

// UserEvent is one domain event.
type UserEvent struct {
	Kind        string
	UserID      uuid.UUID
	TrackID     uuid.UUID
	SessionID   uuid.UUID // wave events only
	Source      string    // wave events: candidate source that served the track
	PositionSec float64   // skip events
}

// PublishUserEvent appends a domain event (bounded stream).
func PublishUserEvent(ctx context.Context, rdb redis.UniversalClient, e UserEvent) error {
	v := map[string]any{"k": e.Kind, "u": e.UserID.String(), "t": e.TrackID.String()}
	if e.SessionID != uuid.Nil {
		v["s"] = e.SessionID.String()
	}
	if e.Source != "" {
		v["src"] = e.Source
	}
	if e.Kind == KindSkip {
		v["p"] = strconv.FormatFloat(e.PositionSec, 'f', 1, 64)
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: UserEventsStream, MaxLen: streamMaxLen, Approx: true, Values: v}).Err(); err != nil {
		return fmt.Errorf("publish user event: %w", err)
	}
	return nil
}

// Catalog refresh signalling (ADR 0011): music-service API → music-worker
// (rescan + sync now), music-worker → reco-worker (catalog changed).
const (
	CatalogRefreshChannel = "music:catalog_refresh"
	CatalogUpdatedChannel = "music:catalog_updated"
)
