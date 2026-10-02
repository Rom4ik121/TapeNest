// Package ingest folds user signals into the long-term profile: it consumes
// music-service's Redis streams (music:play_events with its own consumer group,
// music:user_events) and runs the one-off backfill from the internal export.
// Every signal is applied exactly once (reco.ingested idempotency keys).
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
)

// Streams and group (owned by music-service; reco only reads).
const (
	PlayStream = "music:play_events"
	UserStream = "music:user_events"
	Group      = "reco"
)

// Store is the persistence port.
type Store interface {
	ApplySignal(ctx context.Context, sg repo.Signal) (bool, error)
	TrackKeys(ctx context.Context, id uuid.UUID) ([]profile.TasteKey, error)
	GetState(ctx context.Context, key string) (string, error)
	SetState(ctx context.Context, key, value string) error
}

// Consumer reads both streams.
type Consumer struct {
	RDB      redis.UniversalClient
	Store    Store
	Snapshot func() *model.Model // current model (tags); may return nil
	Name     string
	Log      *slog.Logger
	Block    time.Duration
	Applied  func(n int) // metrics hook (optional)
}

// EnsureGroups creates the consumer groups at the stream tail ("$"): history
// before the group existed comes from the backfill export instead.
func EnsureGroups(ctx context.Context, rdb redis.UniversalClient) error {
	for _, s := range []string{PlayStream, UserStream} {
		err := rdb.XGroupCreateMkStream(ctx, s, Group, "$").Err()
		if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("create group %s: %w", s, err)
		}
	}
	return nil
}

// ErrSkip marks entries that are malformed (acked and dropped).
var ErrSkip = errors.New("skip entry")

// ParsePlay decodes a music:play_events entry (u, t, p, c).
func ParsePlay(m redis.XMessage) (repo.Signal, error) {
	u, err1 := uuid.Parse(str(m, "u"))
	t, err2 := uuid.Parse(str(m, "t"))
	p, err3 := strconv.ParseFloat(str(m, "p"), 64)
	at, err4 := entryTime(m.ID)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return repo.Signal{}, fmt.Errorf("%w: play %s", ErrSkip, m.ID)
	}
	return repo.Signal{
		Key: "play:" + m.ID, User: u, Track: t,
		Event: profile.Event{Kind: profile.KindPlay, PositionSec: p, Completed: str(m, "c") == "1", At: at},
	}, nil
}

var userKinds = map[string]bool{
	profile.KindLike: true, profile.KindUnlike: true, profile.KindPlaylistAdd: true, profile.KindPlaylistRemove: true,
	profile.KindWaveLike: true, profile.KindWaveSkip: true, profile.KindSkip: true,
}

// ParseUser decodes a music:user_events entry (k, u, t, s?, src?, p?).
func ParseUser(m redis.XMessage) (repo.Signal, error) {
	k := str(m, "k")
	u, err1 := uuid.Parse(str(m, "u"))
	t, err2 := uuid.Parse(str(m, "t"))
	at, err3 := entryTime(m.ID)
	if err := errors.Join(err1, err2, err3); err != nil || !userKinds[k] {
		return repo.Signal{}, fmt.Errorf("%w: user event %s", ErrSkip, m.ID)
	}
	sg := repo.Signal{Key: "ue:" + m.ID, User: u, Track: t, Event: profile.Event{Kind: k, At: at}}
	if p := str(m, "p"); p != "" {
		sg.Event.PositionSec, _ = strconv.ParseFloat(p, 64)
	}
	if k == profile.KindWaveLike || k == profile.KindWaveSkip {
		sg.Source = str(m, "src")
	}
	return sg, nil
}

func str(m redis.XMessage, k string) string { s, _ := m.Values[k].(string); return s }

func entryTime(id string) (time.Time, error) {
	ms, err := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms).UTC(), nil
}

// keys resolves taste keys of a track from the snapshot (with audio tags) or the DB.
func (c *Consumer) keys(ctx context.Context, id uuid.UUID) []profile.TasteKey {
	if c.Snapshot != nil {
		if m := c.Snapshot(); m != nil {
			if i, ok := m.Index[id]; ok {
				t := m.Tracks[i]
				album := ""
				if t.AlbumID != uuid.Nil {
					album = t.AlbumID.String()
				}
				return profile.KeysFor(t.ArtistID.String(), album, t.Genre, t.Tags)
			}
		}
	}
	ks, err := c.Store.TrackKeys(ctx, id)
	if err != nil {
		c.Log.Warn("track keys lookup failed", "track", id.String(), "err", err)
	}
	return ks
}

// Apply folds one signal (resolving taste keys).
func (c *Consumer) Apply(ctx context.Context, sg repo.Signal) error {
	sg.Keys = c.keys(ctx, sg.Track)
	_, err := c.Store.ApplySignal(ctx, sg)
	return err
}

// Run consumes both streams until ctx is done: pending entries first (crash
// recovery), then new ones. Failed entries stay pending and are retried.
func (c *Consumer) Run(ctx context.Context) {
	if c.Block == 0 {
		c.Block = 5 * time.Second
	}
	start := "0"
	lastPending := time.Now()
	for ctx.Err() == nil {
		if start == ">" && time.Since(lastPending) > time.Minute {
			start, lastPending = "0", time.Now() // retry entries that failed earlier
		}
		res, err := c.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: Group, Consumer: c.Name, Streams: []string{PlayStream, UserStream, start, start}, Count: 200, Block: c.Block,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				start = ">"
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if strings.Contains(err.Error(), "NOGROUP") {
				_ = EnsureGroups(ctx, c.RDB)
			}
			c.Log.Warn("ingest read failed", "err", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		n := 0
		for _, s := range res {
			for _, m := range s.Messages {
				if c.handle(ctx, s.Stream, m) {
					n++
				}
			}
		}
		if start == "0" && n == 0 {
			start = ">"
		}
		if c.Applied != nil && n > 0 {
			c.Applied(n)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, stream string, m redis.XMessage) bool {
	var sg repo.Signal
	var err error
	if stream == PlayStream {
		sg, err = ParsePlay(m)
	} else {
		sg, err = ParseUser(m)
	}
	if err == nil {
		err = c.Apply(ctx, sg)
		if err != nil {
			c.Log.Warn("ingest apply failed (will retry)", "stream", stream, "id", m.ID, "err", err)
			return false
		}
	} else {
		c.Log.Warn("ingest: dropping malformed entry", "stream", stream, "id", m.ID)
	}
	c.RDB.XAck(ctx, stream, Group, m.ID)
	return err == nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Backfill imports history once (likes, playlist adds, recent play events).
func Backfill(ctx context.Context, c *Consumer, mc *musicclient.Client, days int) (int, error) {
	if done, err := c.Store.GetState(ctx, repo.StateBackfillDone); err != nil || done != "" {
		return 0, err
	}
	n := 0
	err := mc.Interactions(ctx, days, func(it musicclient.Interaction) error {
		sg := repo.Signal{User: it.UserID, Track: it.TrackID, Event: profile.Event{At: it.At}}
		switch it.Kind {
		case "like":
			sg.Key = "bf:like:" + it.UserID.String() + ":" + it.TrackID.String()
			sg.Event.Kind = profile.KindLike
		case "playlist_add":
			sg.Key = fmt.Sprintf("bf:pl:%s:%s:%d", it.UserID, it.TrackID, it.At.UnixMilli())
			sg.Event.Kind = profile.KindPlaylistAdd
		case "play":
			if it.EventID == "" {
				return nil
			}
			sg.Key = "play:" + it.EventID
			sg.Event.Kind = profile.KindPlay
			sg.Event.Completed = it.Completed
			sg.Event.PositionSec = it.PositionSec
		default:
			return nil
		}
		if err := c.Apply(ctx, sg); err != nil {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		return n, err
	}
	return n, c.Store.SetState(ctx, repo.StateBackfillDone, time.Now().UTC().Format(time.RFC3339))
}
