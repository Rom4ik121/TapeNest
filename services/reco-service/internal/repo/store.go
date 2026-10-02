package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo/db"
)

// State keys.
const (
	StateModelVersion = "model_version"
	StateBackfillDone = "backfill_done"
	StateTrainedAt    = "trained_at"
)

// Store is the PostgreSQL layer of reco-service.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: db.New(pool)} }

// CatalogTrack is one row of the music-service catalog export.
type CatalogTrack struct {
	ID          uuid.UUID
	Title       string
	ArtistID    uuid.UUID
	Artist      string
	AlbumID     *uuid.UUID
	Album       string
	Genre       string
	Year        *int32
	DurationSec int32
	Popularity  float32
	CreatedAt   time.Time
}

// UpsertTracks stores a catalog page.
func (s *Store) UpsertTracks(ctx context.Context, ts []CatalogTrack, syncedAt time.Time) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		for _, t := range ts {
			if err := q.UpsertTrack(ctx, db.UpsertTrackParams{
				ID: t.ID, Title: t.Title, ArtistID: t.ArtistID, Artist: t.Artist,
				AlbumID: t.AlbumID, Album: t.Album, Genre: t.Genre, Year: t.Year, DurationSec: t.DurationSec,
				Popularity: t.Popularity, CreatedAt: t.CreatedAt, SyncedAt: syncedAt,
			}); err != nil {
				return fmt.Errorf("upsert track: %w", err)
			}
		}
		return nil
	})
}

// MarkMissingDeleted soft-deletes tracks not seen in the last sync.
func (s *Store) MarkMissingDeleted(ctx context.Context, before time.Time) (int64, error) {
	return s.q.MarkTracksDeleted(ctx, before)
}

// TracksNeedingAnalysis lists tracks without current audio features.
func (s *Store) TracksNeedingAnalysis(ctx context.Context, version, maxAttempts, limit int) ([]uuid.UUID, error) {
	return s.q.TracksNeedingAnalysis(ctx, db.TracksNeedingAnalysisParams{Version: int32(version), MaxAttempts: int32(maxAttempts), Lim: int32(limit)}) //nolint:gosec // small ints
}

// Features is what the analyzer stores.
type Features struct {
	Tempo, Energy, LoudnessDB, Centroid, Flatness, Valence float64
	Raw                                                    []float32
}

// SaveFeatures stores a successful analysis.
func (s *Store) SaveFeatures(ctx context.Context, id uuid.UUID, version int, f Features) error {
	return s.q.UpsertFeatures(ctx, db.UpsertFeaturesParams{
		TrackID: id, Version: int32(version), Tempo: float32(f.Tempo), //nolint:gosec // small
		Energy: float32(f.Energy), LoudnessDb: float32(f.LoudnessDB), Centroid: float32(f.Centroid), Flatness: float32(f.Flatness),
		Valence: float32(f.Valence), Raw: f.Raw,
	})
}

// SaveAnalysisError records a failed analysis (message must already be scrubbed).
func (s *Store) SaveAnalysisError(ctx context.Context, id uuid.UUID, version int, msg string) error {
	if len(msg) > 300 {
		msg = msg[:300]
	}
	// Postgres text rejects NUL and invalid UTF-8 (also a byte cut above may split a rune).
	msg = strings.ToValidUTF8(strings.ReplaceAll(msg, "\x00", ""), "")
	return s.q.RecordAnalysisError(ctx, db.RecordAnalysisErrorParams{TrackID: id, Version: int32(version), Error: msg}) //nolint:gosec // small
}

// FeatureStats reports analysis coverage.
func (s *Store) FeatureStats(ctx context.Context, version int) (analyzed, failed, tracks int64, err error) {
	r, err := s.q.FeatureStats(ctx, int32(version)) //nolint:gosec // small
	return r.Analyzed, r.Failed, r.Tracks, err
}

// LoadModel builds the in-memory snapshot (catalog + features + neighbours + item factors).
func (s *Store) LoadModel(ctx context.Context, featureVersion int) (*model.Model, error) {
	rows, err := s.q.ListActiveTracks(ctx)
	if err != nil {
		return nil, fmt.Errorf("load tracks: %w", err)
	}
	tracks := make([]model.Track, len(rows))
	for i, r := range rows {
		t := model.Track{
			ID: r.ID, Title: r.Title, ArtistID: r.ArtistID, Artist: r.Artist, Album: r.Album, Genre: r.Genre,
			DurationSec: int(r.DurationSec), CreatedAt: r.CreatedAt, Popularity: float64(r.Popularity),
		}
		if r.AlbumID != nil {
			t.AlbumID = *r.AlbumID
		}
		if r.Year != nil {
			t.Year = int(*r.Year)
		}
		if int(r.FeatureVersion) == featureVersion && len(r.Raw) > 0 {
			t.Raw = r.Raw
		}
		tracks[i] = t
	}
	normalizePopularity(tracks)
	m := model.New(tracks)
	nbs, err := s.q.ListNeighbors(ctx)
	if err != nil {
		return nil, fmt.Errorf("load neighbours: %w", err)
	}
	for _, n := range nbs {
		i, ok1 := m.Index[n.TrackID]
		j, ok2 := m.Index[n.NeighborID]
		if !ok1 || !ok2 {
			continue
		}
		nb := model.Neighbor{Idx: int32(j), Sim: n.Sim} //nolint:gosec // j < len
		if n.Source == "cf" {
			m.CF[i] = append(m.CF[i], nb)
		} else {
			m.Content[i] = append(m.Content[i], nb)
		}
	}
	fs, err := s.q.ListItemFactors(ctx)
	if err != nil {
		return nil, fmt.Errorf("load factors: %w", err)
	}
	for _, f := range fs {
		if i, ok := m.Index[f.TrackID]; ok {
			m.ItemFactors[i] = f.Factors
		}
	}
	v, err := s.ModelVersion(ctx)
	if err != nil {
		return nil, err
	}
	m.Version = v
	return m, nil
}

// ModelVersion returns the current model version (0 before the first training).
func (s *Store) ModelVersion(ctx context.Context) (int64, error) {
	v, err := s.q.GetState(ctx, StateModelVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("model version: %w", err)
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n, nil
}

// GetState reads a state value ("" when unset).
func (s *Store) GetState(ctx context.Context, key string) (string, error) {
	v, err := s.q.GetState(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetState writes a state value.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	return s.q.SetState(ctx, db.SetStateParams{Key: key, Value: value})
}

// Trained is the output of a training run.
type Trained struct {
	CF, Content  map[uuid.UUID][]NeighborRow
	ItemFactors  map[uuid.UUID][]float32
	UserFactors  map[uuid.UUID][]float32
	TrainContent bool // replace content neighbours too
}

// NeighborRow is one stored neighbour.
type NeighborRow struct {
	ID  uuid.UUID
	Sim float32
}

// SaveModel atomically replaces the model tables and bumps model_version.
func (s *Store) SaveModel(ctx context.Context, t Trained, at time.Time) (int64, error) {
	var version int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		ins := func(source string, m map[uuid.UUID][]NeighborRow) error {
			if err := q.DeleteNeighbors(ctx, source); err != nil {
				return err
			}
			var rows []db.InsertNeighborsParams
			for id, ns := range m {
				for _, n := range ns {
					rows = append(rows, db.InsertNeighborsParams{TrackID: id, Source: source, NeighborID: n.ID, Sim: n.Sim})
				}
			}
			_, err := q.InsertNeighbors(ctx, rows)
			return err
		}
		if err := ins("cf", t.CF); err != nil {
			return fmt.Errorf("save cf: %w", err)
		}
		if t.TrainContent {
			if err := ins("content", t.Content); err != nil {
				return fmt.Errorf("save content: %w", err)
			}
		}
		if err := q.DeleteItemFactors(ctx); err != nil {
			return err
		}
		var it []db.InsertItemFactorsParams
		for id, f := range t.ItemFactors {
			it = append(it, db.InsertItemFactorsParams{TrackID: id, Factors: f})
		}
		if _, err := q.InsertItemFactors(ctx, it); err != nil {
			return fmt.Errorf("save item factors: %w", err)
		}
		if err := q.DeleteUserFactors(ctx); err != nil {
			return err
		}
		var ut []db.InsertUserFactorsParams
		for id, f := range t.UserFactors {
			ut = append(ut, db.InsertUserFactorsParams{UserID: id, Factors: f})
		}
		if _, err := q.InsertUserFactors(ctx, ut); err != nil {
			return fmt.Errorf("save user factors: %w", err)
		}
		cur, err := q.GetState(ctx, StateModelVersion)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		version, _ = strconv.ParseInt(cur, 10, 64)
		version++
		if err := q.SetState(ctx, db.SetStateParams{Key: StateModelVersion, Value: strconv.FormatInt(version, 10)}); err != nil {
			return err
		}
		return q.SetState(ctx, db.SetStateParams{Key: StateTrainedAt, Value: at.UTC().Format(time.RFC3339)})
	})
	return version, err
}

// Affinity is one positive (user, track) signal for training.
type Affinity struct {
	User, Track uuid.UUID
	Value       float64
}

// PositiveAffinities returns decayed positive affinities.
func (s *Store) PositiveAffinities(ctx context.Context, now time.Time) ([]Affinity, error) {
	rows, err := s.q.PositiveAffinities(ctx)
	if err != nil {
		return nil, fmt.Errorf("affinities: %w", err)
	}
	out := make([]Affinity, 0, len(rows))
	for _, r := range rows {
		if v := profile.Decay(r.Affinity, r.AffinityAt, now); v > 0.05 {
			out = append(out, Affinity{User: r.UserID, Track: r.TrackID, Value: v})
		}
	}
	return out, nil
}

// Signal is one event to fold into the profile.
type Signal struct {
	Key    string // idempotency key
	User   uuid.UUID
	Track  uuid.UUID
	Event  profile.Event
	Keys   []profile.TasteKey
	Source string // wave source that served the track (Thompson arm), optional
}

// ApplySignal folds one event into user_tracks / user_taste / source_stats
// exactly once (reco.ingested). Returns false for duplicates.
func (s *Store) ApplySignal(ctx context.Context, sg Signal) (bool, error) {
	applied := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.MarkIngested(ctx, sg.Key)
		if err != nil || n == 0 {
			return err
		}
		applied = true
		st := profile.TrackState{}
		row, err := q.GetUserTrack(ctx, db.GetUserTrackParams{UserID: sg.User, TrackID: sg.Track})
		switch {
		case err == nil:
			st = profile.TrackState{
				Affinity: row.Affinity, At: row.AffinityAt, Plays: int(row.Plays), Completes: int(row.Completes),
				EarlySkips: int(row.EarlySkips), Skips: int(row.Skips), PlAdds: int(row.PlaylistAdds), Liked: row.Liked,
			}
			if row.LastPlayed != nil {
				st.LastPlayed = *row.LastPlayed
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("get user track: %w", err)
		}
		delta := st.Apply(sg.Event)
		var last *time.Time
		if !st.LastPlayed.IsZero() {
			lp := st.LastPlayed
			last = &lp
		}
		if err := q.UpsertUserTrack(ctx, db.UpsertUserTrackParams{
			UserID: sg.User, TrackID: sg.Track, Affinity: st.Affinity,
			AffinityAt: st.At, Plays: int32(st.Plays), Completes: int32(st.Completes), EarlySkips: int32(st.EarlySkips), //nolint:gosec // counters
			Skips: int32(st.Skips), PlaylistAdds: int32(st.PlAdds), Liked: st.Liked, LastPlayed: last, //nolint:gosec // counters
		}); err != nil {
			return fmt.Errorf("upsert user track: %w", err)
		}
		if delta != 0 {
			for _, k := range sg.Keys {
				if err := q.AddTaste(ctx, db.AddTasteParams{
					UserID: sg.User, Kind: k.Kind, Key: k.Key,
					Delta: delta * profile.Propagation(k.Kind), EventAt: sg.Event.At, HalfLifeSec: profile.HalfLife.Seconds(),
				}); err != nil {
					return fmt.Errorf("add taste: %w", err)
				}
			}
		}
		if sg.Source != "" {
			a, b := 0.0, 0.0
			switch sg.Event.Kind {
			case profile.KindWaveLike, profile.KindLike:
				a = 1
			case profile.KindWaveSkip, profile.KindSkip:
				b = 1
			case profile.KindPlay:
				if sg.Event.Completed {
					a = 0.5
				}
			}
			if a+b > 0 {
				if err := q.AddSourceStat(ctx, db.AddSourceStatParams{UserID: sg.User, Source: sg.Source, Alpha: a, Beta: b}); err != nil {
					return fmt.Errorf("source stat: %w", err)
				}
			}
		}
		return nil
	})
	return applied, err
}

// TrackKeys returns artist/album/genre keys for a track unknown to the snapshot.
func (s *Store) TrackKeys(ctx context.Context, id uuid.UUID) ([]profile.TasteKey, error) {
	r, err := s.q.TrackKeys(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	album := ""
	if r.AlbumID != nil {
		album = r.AlbumID.String()
	}
	return profile.KeysFor(r.ArtistID.String(), album, r.Genre, nil), nil
}

// CleanupIngested drops idempotency keys older than before.
func (s *Store) CleanupIngested(ctx context.Context, before time.Time) (int64, error) {
	return s.q.CleanupIngested(ctx, before)
}

// LoadUser builds the ranker's user view (decayed to now) against a snapshot.
func (s *Store) LoadUser(ctx context.Context, user uuid.UUID, m *model.Model, now time.Time) (*rank.User, error) {
	u := &rank.User{Taste: map[string]float64{}, Tracks: map[int]rank.UserTrack{}, Sources: map[string]rank.Beta{}}
	taste, err := s.q.ListUserTaste(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("taste: %w", err)
	}
	for _, t := range taste {
		u.Taste[rank.TasteKey(t.Kind, t.Key)] = profile.Decay(t.Weight, t.UpdatedAt, now)
	}
	uts, err := s.q.ListUserTracks(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("user tracks: %w", err)
	}
	for _, r := range uts {
		i, ok := m.Index[r.TrackID]
		if !ok {
			continue
		}
		ut := rank.UserTrack{
			Affinity: profile.Decay(r.Affinity, r.AffinityAt, now), Plays: int(r.Plays), Completes: int(r.Completes),
			EarlySkips: int(r.EarlySkips), Skips: int(r.Skips), Liked: r.Liked,
		}
		if r.LastPlayed != nil {
			ut.LastPlayed = *r.LastPlayed
		}
		u.Tracks[i] = ut
	}
	ss, err := s.q.ListSourceStats(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("source stats: %w", err)
	}
	for _, r := range ss {
		u.Sources[r.Source] = rank.Beta{Alpha: r.Alpha, Beta: r.Beta}
	}
	f, err := s.q.GetUserFactors(ctx, user)
	switch {
	case err == nil:
		u.Factors = f
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("user factors: %w", err)
	}
	return u, nil
}

// ProfileCounts summarizes a user's stored signals.
func (s *Store) ProfileCounts(ctx context.Context, user uuid.UUID) (tracks, plays, likes, earlySkips int64, err error) {
	r, err := s.q.ProfileCounts(ctx, user)
	return r.Tracks, r.Plays, r.Likes, r.EarlySkips, err
}

// Ping checks the database.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// normalizePopularity maps raw music.track_popularity scores to 0..1 (log scale).
func normalizePopularity(ts []model.Track) {
	mx := 0.0
	for _, t := range ts {
		mx = math.Max(mx, math.Log1p(math.Max(0, t.Popularity)))
	}
	for i := range ts {
		if mx > 0 {
			ts[i].Popularity = math.Log1p(math.Max(0, ts[i].Popularity)) / mx
		} else {
			ts[i].Popularity = 0
		}
	}
}
