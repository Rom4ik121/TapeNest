package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/reco"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// Recommender is the reco-service port (implemented by *reco.Client).
type Recommender interface {
	Next(ctx context.Context, req reco.NextRequest) ([]reco.Pick, error)
	State() string
}

// Wave is "My wave". Batches come from reco-service (personal taste profile, CF,
// audio content, diversity, exploration — ADR 0010); when reco is disabled, slow
// or down (timeout + circuit breaker in the client) the local heuristic of
// ADR 0009 §6 serves the batch instead. Sessions live in Redis (TTL WaveTTL).
type Wave struct {
	store   Store
	rdb     redis.UniversalClient
	reco    Recommender
	publish func(ctx context.Context, e mq.UserEvent)
	log     *slog.Logger
	batch   int
	pool    int
	ttl     time.Duration
	now     func() time.Time
	rnd     func() float64
}

// Wave defaults.
const (
	WaveBatch = 10
	WavePool  = 500
	WaveTTL   = 6 * time.Hour
	// feedback entries kept per session and sent to reco
	waveFeedbackKeep = 100
	waveRecentSent   = 30
)

// NewWave creates the wave service (heuristic only until WithReco).
func NewWave(store Store, rdb redis.UniversalClient) *Wave {
	return &Wave{
		store: store, rdb: rdb, batch: WaveBatch, pool: WavePool, ttl: WaveTTL, now: time.Now,
		rnd: rand.Float64, log: slog.Default(), publish: func(context.Context, mq.UserEvent) {}, //nolint:gosec // shuffling, not security
	}
}

// WithReco enables reco-service batches (nil keeps the heuristic).
func (w *Wave) WithReco(r Recommender) *Wave {
	if r != nil {
		w.reco = r
	}
	return w
}

// WithEvents sets the user-event publisher and logger.
func (w *Wave) WithEvents(pub func(ctx context.Context, e mq.UserEvent), log *slog.Logger) *Wave {
	if pub != nil {
		w.publish = pub
	}
	if log != nil {
		w.log = log
	}
	return w
}

// RecoState reports the reco breaker state for readyz.
func (w *Wave) RecoState() string {
	if w.reco == nil {
		return "disabled"
	}
	return w.reco.State()
}

func waveKey(id uuid.UUID, part string) string { return "music:wave:" + id.String() + part }

// NormalizeMode validates a wave mode ("" → default).
func NormalizeMode(mode string) (string, error) {
	if mode == "" {
		return "default", nil
	}
	if !domain.WaveModes[mode] {
		return "", domain.Invalid("mode must be one of default, calm, energetic, discover, favorites")
	}
	return mode, nil
}

// Start creates a session and returns the first batch.
func (w *Wave) Start(ctx context.Context, user uuid.UUID, mode string) (uuid.UUID, domain.WaveBatch, error) {
	mode, err := NormalizeMode(mode)
	if err != nil {
		return uuid.Nil, domain.WaveBatch{}, err
	}
	id := uuid.New()
	if err := w.rdb.HSet(ctx, waveKey(id, ""), "user", user.String(), "mode", mode).Err(); err != nil {
		return uuid.Nil, domain.WaveBatch{}, fmt.Errorf("wave session: %w", err)
	}
	w.rdb.Expire(ctx, waveKey(id, ""), w.ttl)
	b, err := w.next(ctx, user, id, mode)
	return id, b, err
}

// Next returns the next batch; ErrNotFound for unknown/expired/foreign sessions.
func (w *Wave) Next(ctx context.Context, user, session uuid.UUID) (domain.WaveBatch, error) {
	if err := w.owned(ctx, user, session); err != nil {
		return domain.WaveBatch{}, err
	}
	mode, _ := w.rdb.HGet(ctx, waveKey(session, ""), "mode").Result()
	if mode == "" {
		mode = "default"
	}
	return w.next(ctx, user, session, mode)
}

// Feedback records like/skip: session rerank (both strategies) + a domain event
// for reco-service's long-term profile and Thompson arms.
func (w *Wave) Feedback(ctx context.Context, user, session, track uuid.UUID, action string) error {
	if action != "like" && action != "skip" {
		return domain.Invalid("action must be like or skip")
	}
	if err := w.owned(ctx, user, session); err != nil {
		return err
	}
	artist, err := w.rdb.HGet(ctx, waveKey(session, ":artists"), track.String()).Result()
	if errors.Is(err, redis.Nil) {
		return nil // not a wave track (e.g. liked from elsewhere): nothing to learn
	}
	if err != nil {
		return fmt.Errorf("wave feedback: %w", err)
	}
	src, _ := w.rdb.HGet(ctx, waveKey(session, ":src"), track.String()).Result()
	pipe := w.rdb.TxPipeline()
	pipe.HIncrBy(ctx, waveKey(session, ":"+action), artist, 1)
	pipe.Expire(ctx, waveKey(session, ":"+action), w.ttl)
	pipe.RPush(ctx, waveKey(session, ":fb"), track.String()+"|"+action)
	pipe.LTrim(ctx, waveKey(session, ":fb"), -waveFeedbackKeep, -1)
	pipe.Expire(ctx, waveKey(session, ":fb"), w.ttl)
	if _, err = pipe.Exec(ctx); err != nil {
		return fmt.Errorf("wave feedback: %w", err)
	}
	kind := mq.KindWaveLike
	if action == "skip" {
		kind = mq.KindWaveSkip
	}
	w.publish(ctx, mq.UserEvent{Kind: kind, UserID: user, TrackID: track, SessionID: session, Source: src})
	return nil
}

func (w *Wave) owned(ctx context.Context, user, session uuid.UUID) error {
	owner, err := w.rdb.HGet(ctx, waveKey(session, ""), "user").Result()
	if errors.Is(err, redis.Nil) || (err == nil && owner != user.String()) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("wave session: %w", err)
	}
	return nil
}

type scored struct {
	c     repo.WaveCandidate
	score float64
}

// Score combines the fallback heuristic's signals (ADR 0009 §6); exported for tests and docs.
func Score(c repo.WaveCandidate, now time.Time, likes, skips map[string]int, jitter float64) float64 {
	s := math.Log1p(c.Popularity)
	if c.Liked {
		s += 1.5
	}
	if c.LikedArtist {
		s += 0.7
	}
	a := c.ArtistID.String()
	s += 1.0*float64(likes[a]) - 1.5*float64(skips[a])
	if c.LastPlayed != nil {
		switch ago := now.Sub(*c.LastPlayed); {
		case ago < 2*time.Hour:
			s -= 3
		case ago < 24*time.Hour:
			s--
		}
	}
	return s + jitter
}

type pickedTrack struct {
	id     uuid.UUID
	source string
	reason *domain.WaveReason
}

func (w *Wave) next(ctx context.Context, user, session uuid.UUID, mode string) (domain.WaveBatch, error) {
	served, err := w.rdb.SMembers(ctx, waveKey(session, ":served")).Result()
	if err != nil {
		return domain.WaveBatch{}, fmt.Errorf("wave served: %w", err)
	}
	strategy := domain.StrategyFallback
	var picks []pickedTrack
	if w.reco != nil {
		picks, err = w.fromReco(ctx, user, session, mode, served)
		switch {
		case err != nil:
			w.log.Warn("wave: reco unavailable, using heuristic", "err", err.Error(), "breaker", w.reco.State())
		case len(picks) > 0:
			strategy = domain.StrategyReco
		}
	}
	if strategy == domain.StrategyFallback {
		if picks, err = w.fromHeuristic(ctx, user, session, served); err != nil {
			return domain.WaveBatch{}, err
		}
	}
	b := domain.WaveBatch{Tracks: []domain.WaveTrack{}, Strategy: strategy, Mode: mode}
	if len(picks) == 0 {
		return b, nil
	}
	ids := make([]uuid.UUID, len(picks))
	for i, p := range picks {
		ids[i] = p.id
	}
	tracks, err := w.store.TracksByIDs(ctx, user, ids)
	if err != nil {
		return b, err
	}
	byID := make(map[uuid.UUID]pickedTrack, len(picks))
	for _, p := range picks {
		byID[p.id] = p
	}
	pipe := w.rdb.TxPipeline()
	for _, t := range tracks {
		p := byID[t.ID]
		pipe.SAdd(ctx, waveKey(session, ":served"), t.ID.String())
		pipe.RPush(ctx, waveKey(session, ":order"), t.ID.String())
		pipe.HSet(ctx, waveKey(session, ":artists"), t.ID.String(), t.ArtistID.String())
		if p.source != "" {
			pipe.HSet(ctx, waveKey(session, ":src"), t.ID.String(), p.source)
		}
		b.Tracks = append(b.Tracks, domain.WaveTrack{Track: t, Reason: p.reason})
	}
	for _, part := range []string{"", ":served", ":order", ":artists", ":src"} {
		pipe.Expire(ctx, waveKey(session, part), w.ttl)
	}
	pipe.LTrim(ctx, waveKey(session, ":order"), -200, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return b, fmt.Errorf("wave save: %w", err)
	}
	return b, nil
}

// fromReco asks reco-service; when the catalog is exhausted for this session it
// starts a new round (served reset, only the last batch stays excluded).
func (w *Wave) fromReco(ctx context.Context, user, session uuid.UUID, mode string, served []string) ([]pickedTrack, error) {
	order, _ := w.rdb.LRange(ctx, waveKey(session, ":order"), -waveRecentSent, -1).Result()
	fbRaw, _ := w.rdb.LRange(ctx, waveKey(session, ":fb"), -waveFeedbackKeep, -1).Result()
	req := reco.NextRequest{
		UserID: user, SessionID: session, Mode: mode, Limit: w.batch,
		Exclude: parseIDs(served), Recent: parseIDs(order), Feedback: []reco.Feedback{},
	}
	for _, f := range fbRaw {
		id, action, ok := strings.Cut(f, "|")
		if tid, err := uuid.Parse(id); ok && err == nil {
			req.Feedback = append(req.Feedback, reco.Feedback{TrackID: tid, Action: action})
		}
	}
	picks, err := w.reco.Next(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(picks) == 0 && len(served) > 0 {
		last := order
		if len(last) > w.batch {
			last = last[len(last)-w.batch:]
		}
		w.rdb.Del(ctx, waveKey(session, ":served"))
		req.Exclude = parseIDs(last)
		if picks, err = w.reco.Next(ctx, req); err != nil {
			return nil, err
		}
	}
	out := make([]pickedTrack, 0, len(picks))
	for _, p := range picks {
		pt := pickedTrack{id: p.TrackID, source: p.Source}
		if r := p.Reason; r != nil && r.Kind != "" {
			pt.reason = &domain.WaveReason{
				Kind: r.Kind, RefTrackID: r.RefTrackID, RefTitle: r.RefTitle, RefArtist: r.RefArtist,
				Artist: r.Artist, Genre: r.Genre, Tag: r.Tag,
			}
		}
		out = append(out, pt)
	}
	return out, nil
}

func parseIDs(ss []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ss))
	for _, s := range ss {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// fromHeuristic is the ADR 0009 §6 fallback (with simple reasons).
func (w *Wave) fromHeuristic(ctx context.Context, user, session uuid.UUID, served []string) ([]pickedTrack, error) {
	cands, err := w.store.WaveCandidates(ctx, user, w.pool)
	if err != nil {
		return nil, err
	}
	likes, skips := w.counts(ctx, session, ":like"), w.counts(ctx, session, ":skip")
	seen := make(map[string]bool, len(served))
	for _, s := range served {
		seen[s] = true
	}
	var pool []scored
	for _, c := range cands {
		if !seen[c.ID.String()] {
			pool = append(pool, scored{c: c, score: Score(c, w.now(), likes, skips, w.rnd())})
		}
	}
	if len(pool) < w.batch && len(cands) > len(pool) {
		// small catalog: start a new round, keeping only the last batch out
		last, _ := w.rdb.LRange(ctx, waveKey(session, ":order"), int64(-w.batch), -1).Result()
		recent := map[string]bool{}
		for _, id := range last {
			recent[id] = true
		}
		w.rdb.Del(ctx, waveKey(session, ":served"))
		pool = pool[:0]
		for _, c := range cands {
			if !recent[c.ID.String()] || len(cands) <= w.batch {
				pool = append(pool, scored{c: c, score: Score(c, w.now(), likes, skips, w.rnd())})
			}
		}
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].score > pool[j].score })
	picked := diversify(pool, w.batch)
	out := make([]pickedTrack, len(picked))
	for i, p := range picked {
		out[i] = pickedTrack{id: p.c.ID, reason: heuristicReason(p.c, likes)}
	}
	return out, nil
}

func heuristicReason(c repo.WaveCandidate, likes map[string]int) *domain.WaveReason {
	switch {
	case c.Liked:
		return &domain.WaveReason{Kind: "favorite"}
	case likes[c.ArtistID.String()] > 0:
		return &domain.WaveReason{Kind: "session_artist"}
	case c.LikedArtist:
		return &domain.WaveReason{Kind: "artist_you_like"}
	case c.Popularity > 0:
		return &domain.WaveReason{Kind: "popular"}
	}
	return &domain.WaveReason{Kind: "discovery"}
}

func (w *Wave) counts(ctx context.Context, session uuid.UUID, part string) map[string]int {
	m, _ := w.rdb.HGetAll(ctx, waveKey(session, part)).Result()
	out := make(map[string]int, len(m))
	for k, v := range m {
		var n int
		_, _ = fmt.Sscan(v, &n)
		out[k] = n
	}
	return out
}

// diversify takes n best while avoiding the same artist twice in a row when possible.
func diversify(pool []scored, n int) []scored {
	out := make([]scored, 0, n)
	used := make([]bool, len(pool))
	for len(out) < n {
		pick := -1
		for i := range pool {
			if used[i] {
				continue
			}
			if pick < 0 {
				pick = i
			}
			if len(out) == 0 || pool[i].c.ArtistID != out[len(out)-1].c.ArtistID {
				pick = i
				break
			}
		}
		if pick < 0 {
			break
		}
		used[pick] = true
		out = append(out, pool[pick])
	}
	return out
}
