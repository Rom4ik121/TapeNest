package repo_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
)

// Integration test against PostgreSQL 16 (TEST_DATABASE_URL). The reco schema
// is dedicated to this service, so the test works on its own rows only except
// for the model tables, which it replaces (as the worker does).
func testStore(t *testing.T) (*repo.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for i := 0; i < 2; i++ {
		if err := repo.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return repo.NewStore(pool), pool
}

func TestStoreFlow(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	artist, album := uuid.New(), uuid.New()
	var ids []uuid.UUID
	var ts []repo.CatalogTrack
	for i := 0; i < 3; i++ {
		id := uuid.New()
		ids = append(ids, id)
		ts = append(ts, repo.CatalogTrack{
			ID: id, Title: "T", ArtistID: artist, Artist: "A", AlbumID: &album, Album: "Al",
			Genre: "Jazz", DurationSec: 120, Popularity: float32(i), CreatedAt: now,
		})
	}
	user := uuid.New()
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM reco.user_taste WHERE user_id=$1", "DELETE FROM reco.user_tracks WHERE user_id=$1",
			"DELETE FROM reco.source_stats WHERE user_id=$1", "DELETE FROM reco.user_factors WHERE user_id=$1",
		} {
			_, _ = pool.Exec(ctx, q, user)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM reco.tracks WHERE id = ANY($1)", ids)
	})
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTracks(ctx, ts, now); err != nil {
		t.Fatal(err)
	}
	pending, err := s.TracksNeedingAnalysis(ctx, audio.Version, 3, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, p := range pending {
		for _, id := range ids {
			if p == id {
				found++
			}
		}
	}
	if found != 3 {
		t.Fatalf("pending analysis: %d of 3", found)
	}
	raw := make([]float32, audio.RawDims)
	raw[0] = 1
	for _, id := range ids[:2] {
		if err := s.SaveFeatures(ctx, id, audio.Version, repo.Features{Tempo: 120, Energy: 0.3, Raw: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveAnalysisError(ctx, ids[2], audio.Version, "bad\x00"+strings.Repeat("é", 200)); err != nil {
		t.Fatal(err)
	}
	if a, f, n, err := s.FeatureStats(ctx, audio.Version); err != nil || a < 2 || f < 1 || n < 3 {
		t.Fatalf("stats %d %d %d %v", a, f, n, err)
	}
	keys, err := s.TrackKeys(ctx, ids[0])
	if err != nil || len(keys) != 3 {
		t.Fatalf("keys %v %v", keys, err)
	}
	if k, _ := s.TrackKeys(ctx, uuid.New()); k != nil {
		t.Fatal("unknown track keys")
	}

	// Signals: like + complete play + duplicate + wave skip with source.
	sig := func(key, kind string, completed bool, src string) repo.Signal {
		return repo.Signal{
			Key: key, User: user, Track: ids[0], Keys: keys, Source: src,
			Event: profile.Event{Kind: kind, Completed: completed, PositionSec: 10, At: now},
		}
	}
	key := "test:" + uuid.NewString()
	for i, sg := range []repo.Signal{
		sig(key+"1", profile.KindLike, false, ""), sig(key+"2", profile.KindPlay, true, "cf"),
		sig(key+"2", profile.KindPlay, true, "cf"), sig(key+"3", profile.KindWaveSkip, false, "content"),
	} {
		ok, err := s.ApplySignal(ctx, sg)
		if err != nil || ok == (i == 2) {
			t.Fatalf("signal %d ok=%v err=%v", i, ok, err)
		}
	}
	if tr, plays, likes, _, err := s.ProfileCounts(ctx, user); err != nil || tr != 1 || plays != 1 || likes != 1 {
		t.Fatalf("counts %d %d %d %v", tr, plays, likes, err)
	}
	affs, err := s.PositiveAffinities(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	hasUser := false
	for _, a := range affs {
		hasUser = hasUser || a.User == user
	}
	if !hasUser {
		t.Fatal("positive affinity missing")
	}

	// Model save/load round-trip.
	v0, _ := s.ModelVersion(ctx)
	v, err := s.SaveModel(ctx, repo.Trained{
		CF:           map[uuid.UUID][]repo.NeighborRow{ids[0]: {{ID: ids[1], Sim: 0.8}}},
		Content:      map[uuid.UUID][]repo.NeighborRow{ids[1]: {{ID: ids[0], Sim: 0.9}}},
		ItemFactors:  map[uuid.UUID][]float32{ids[0]: {0.1, 0.2}},
		UserFactors:  map[uuid.UUID][]float32{user: {0.3, 0.4}},
		TrainContent: true,
	}, now)
	if err != nil || v != v0+1 {
		t.Fatalf("save model v=%d v0=%d err=%v", v, v0, err)
	}
	m, err := s.LoadModel(ctx, audio.Version)
	if err != nil || m.Version != v {
		t.Fatalf("load model %v", err)
	}
	i0, ok := m.Index[ids[0]]
	if !ok || len(m.CF[i0]) != 1 || m.Tracks[i0].Genre != "Jazz" || m.Tracks[i0].Raw == nil || m.ItemFactors[i0] == nil {
		t.Fatalf("model contents: %+v", m.Tracks[i0])
	}
	if i2 := m.Index[ids[2]]; m.Tracks[i2].Raw != nil {
		t.Fatal("failed analysis must not have features")
	}
	u, err := s.LoadUser(ctx, user, m, now)
	if err != nil || !u.Tracks[i0].Liked || len(u.Factors) != 2 || u.Sources["content"].Beta != 1 || u.Sources["cf"].Alpha != 0.5 {
		t.Fatalf("user %+v %v", u, err)
	}
	if u.Taste["genre:Jazz"] <= 0 {
		t.Fatalf("taste %+v", u.Taste)
	}

	// State & cleanup.
	if err := s.SetState(ctx, "test_key", "x"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetState(ctx, "test_key"); v != "x" {
		t.Fatal("state")
	}
	if v, err := s.GetState(ctx, "missing_"+key); v != "" || err != nil {
		t.Fatal("missing state")
	}
	_, _ = pool.Exec(ctx, "DELETE FROM reco.state WHERE key='test_key'")
	if _, err := s.CleanupIngested(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MarkMissingDeleted(ctx, now.Add(-time.Hour)); err != nil || n < 0 {
		t.Fatal(err)
	}
}
