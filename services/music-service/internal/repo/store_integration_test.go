package repo_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// Integration test against a real PostgreSQL 16 (TEST_DATABASE_URL); CI runs it
// with a service container, locally tools/dev/infra-native.sh.
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
	for i := 0; i < 2; i++ { // idempotent
		if err := repo.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return repo.NewStore(pool, pool), pool
}

func TestCatalogLibraryFlow(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	run := uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM music.tracks WHERE navidrome_id LIKE $1", run+"%")
		_, _ = pool.Exec(ctx, "DELETE FROM music.albums WHERE navidrome_id LIKE $1", run+"%")
		_, _ = pool.Exec(ctx, "DELETE FROM music.artists WHERE navidrome_id LIKE $1", run+"%")
	})
	for _, m := range []time.Time{time.Now(), time.Now().AddDate(0, 1, 0)} {
		if name, err := s.EnsurePartition(ctx, m); err != nil || name == "" {
			t.Fatalf("partition: %q %v", name, err)
		}
	}
	synced := time.Now().UTC()
	cache := repo.NewSyncCache()
	titles := []string{"Nocturne " + run, "Waltz " + run, "Etude " + run}
	for i, title := range titles {
		err := s.UpsertCatalogTrack(ctx, cache, repo.CatalogTrack{
			NavidromeID: run + "-t" + string(rune('a'+i)), Title: title, Artist: "Chopin " + run, ArtistNavID: run + "-ar",
			Album: "Pieces " + run, AlbumNavID: run + "-al", CoverArt: "al-" + run, ContentType: "audio/mpeg",
			TrackNo: i + 1, DurationSec: 100 + i, Bitrate: 192, SizeBytes: 1000, Genre: "Romantic",
		}, synced)
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	// re-sync with a fresh cache (unchanged artist → fallback lookup), no album
	if err := s.UpsertCatalogTrack(ctx, repo.NewSyncCache(), repo.CatalogTrack{
		NavidromeID: run + "-ta", Title: titles[0], Artist: "Chopin " + run, ArtistNavID: run + "-ar", DurationSec: 100,
	}, synced); err != nil {
		t.Fatalf("resync: %v", err)
	}
	user, other := uuid.New(), uuid.New()
	found, err := s.Search(ctx, user, "waltz "+run, "%waltz "+run+"%", nil, 10)
	// fuzzy (word_similarity): best match first
	if err != nil || len(found.Items) == 0 || found.Items[0].Title != titles[1] || found.Items[0].Album != "Pieces "+run {
		t.Fatalf("search: %+v %v", found, err)
	}
	all, err := s.Search(ctx, user, run, "%"+run+"%", nil, 2)
	if err != nil || len(all.Items) != 2 || all.Next == nil {
		t.Fatalf("search page: %+v %v", all, err)
	}
	cur, _ := domain.DecodeCursor(*all.Next)
	rest, err := s.Search(ctx, user, run, "%"+run+"%", cur, 2)
	if err != nil || len(rest.Items) != 1 {
		t.Fatalf("search page 2: %+v %v", rest, err)
	}
	ids := []uuid.UUID{all.Items[0].ID, all.Items[1].ID, rest.Items[0].ID}
	tid := ids[0]

	if err := s.RefreshPopularity(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if p, err := s.Popular(ctx, user, nil, 100); err != nil || len(p.Items) < 3 {
		t.Fatalf("popular: %v %d", err, len(p.Items))
	}
	if st, err := s.TrackForStream(ctx, tid); err != nil || st.NavidromeID == "" {
		t.Fatalf("stream target: %+v %v", st, err)
	}
	if _, err := s.TrackForStream(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing stream: %v", err)
	}
	if ok, err := s.TrackActive(ctx, tid); !ok || err != nil {
		t.Fatal("active")
	}

	// likes
	if err := s.Like(ctx, user, tid); err != nil {
		t.Fatal(err)
	}
	if err := s.Like(ctx, user, tid); err != nil {
		t.Fatal("idempotent like", err)
	}
	if err := s.Like(ctx, user, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("like unknown: %v", err)
	}
	if err := s.Like(ctx, user, ids[1]); err != nil {
		t.Fatal(err)
	}
	liked, err := s.Liked(ctx, user, nil, 1)
	if err != nil || len(liked.Items) != 1 || !liked.Items[0].Liked || liked.Next == nil {
		t.Fatalf("liked: %+v %v", liked, err)
	}
	cur, _ = domain.DecodeCursor(*liked.Next)
	if l2, err := s.Liked(ctx, user, cur, 1); err != nil || len(l2.Items) != 1 || l2.Items[0].ID == liked.Items[0].ID {
		t.Fatalf("liked page 2: %+v %v", l2, err)
	}
	if err := s.Unlike(ctx, user, ids[1]); err != nil {
		t.Fatal(err)
	}
	byIDs, err := s.TracksByIDs(ctx, user, []uuid.UUID{ids[2], tid, uuid.New()})
	if err != nil || len(byIDs) != 2 || byIDs[0].ID != ids[2] || !byIDs[1].Liked {
		t.Fatalf("by ids (ordered): %+v %v", byIDs, err)
	}
	if byIDs[0].ArtistID == uuid.Nil {
		t.Fatal("TracksByIDs must return the artist id (wave diversity)")
	}

	// reco-service exports: keyset paging over the whole catalog finds our tracks with genre
	var after *uuid.UUID
	exported := map[uuid.UUID]repo.ExportTrack{}
	for page := 0; page < 1000; page++ {
		items, err := s.ExportCatalog(ctx, after, 50)
		if err != nil {
			t.Fatalf("export catalog: %v", err)
		}
		for _, it := range items {
			exported[it.ID] = it
		}
		if len(items) < 50 {
			break
		}
		last := items[len(items)-1].ID
		after = &last
	}
	var waltz *repo.ExportTrack // search ties are ordered by random ids: pick by title
	for _, ex := range exported {
		if ex.Title == titles[1] {
			ex := ex
			waltz = &ex
		}
	}
	if waltz == nil || waltz.Genre != "Romantic" || waltz.Artist != "Chopin "+run {
		t.Fatalf("exported track: %+v", waltz)
	}
	var likesSeen int
	if err := s.ExportInteractions(ctx, time.Now().Add(-time.Hour), func(i repo.Interaction) error {
		if i.UserID == user && i.Kind == "like" {
			likesSeen++
		}
		return nil
	}); err != nil || likesSeen != 1 {
		t.Fatalf("export interactions: %d %v", likesSeen, err)
	}
	if err := s.ExportInteractions(ctx, time.Now(), func(repo.Interaction) error { return errors.New("stop") }); err == nil {
		t.Fatal("emit error must propagate")
	}

	// positions + recent
	if _, err := s.Position(ctx, user, tid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no position: %v", err)
	}
	if err := s.SavePosition(ctx, user, tid, 33.5, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePosition(ctx, user, uuid.New(), 1, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("position unknown track: %v", err)
	}
	if p, err := s.Position(ctx, user, tid); err != nil || p.PositionSec != 33.5 {
		t.Fatalf("position: %+v %v", p, err)
	}
	n, err := s.InsertPlayEvents(ctx, []repo.PlayEvent{
		{EventID: run + "-1", UserID: user, TrackID: ids[2], PlayedAt: time.Now().UTC(), PositionSec: 10, Completed: true},
		{EventID: run + "-2", UserID: user, TrackID: tid, PlayedAt: time.Now().UTC().Add(-time.Minute), PositionSec: 5},
	})
	if err != nil || n != 2 {
		t.Fatalf("events: %d %v", n, err)
	}
	evs := []repo.PlayEvent{{EventID: run + "-1", UserID: user, TrackID: ids[2], PlayedAt: time.Now().UTC(), PositionSec: 10}}
	if n, err := s.InsertPlayEvents(ctx, evs); err != nil || n > 1 {
		t.Fatalf("redelivery: %d %v", n, err)
	}
	if n, err := s.InsertPlayEvents(ctx, nil); err != nil || n != 0 {
		t.Fatal("empty batch")
	}
	recent, err := s.Recent(ctx, user, nil, 1)
	if err != nil || len(recent.Items) != 1 || recent.Items[0].ID != ids[2] || recent.Next == nil {
		t.Fatalf("recent: %+v %v", recent, err)
	}
	cur, _ = domain.DecodeCursor(*recent.Next)
	if r2, err := s.Recent(ctx, user, cur, 5); err != nil || len(r2.Items) != 1 {
		t.Fatalf("recent 2: %+v %v", r2, err)
	}
	if err := s.RefreshPopularity(ctx); err != nil {
		t.Fatal(err)
	}
	pop, _ := s.Popular(ctx, user, nil, 1)
	if len(pop.Items) != 1 || pop.Next == nil {
		t.Fatalf("popular page: %+v", pop)
	}
	cur, _ = domain.DecodeCursor(*pop.Next)
	if _, err := s.Popular(ctx, user, cur, 1); err != nil {
		t.Fatal(err)
	}
	cands, err := s.WaveCandidates(ctx, user, 500)
	if err != nil || len(cands) < 3 {
		t.Fatalf("wave: %d %v", len(cands), err)
	}
	var sawLiked bool
	for _, c := range cands {
		if c.ID == tid && c.Liked && c.LikedArtist && c.LastPlayed != nil {
			sawLiked = true
		}
	}
	if !sawLiked {
		t.Fatal("wave candidate signals missing")
	}

	// playlists
	pl, err := s.CreatePlaylist(ctx, user, "Mix")
	if err != nil || pl.TrackCount != 0 {
		t.Fatal(err)
	}
	if c, _ := s.CountPlaylists(ctx, user); c != 1 {
		t.Fatalf("count %d", c)
	}
	for _, id := range []uuid.UUID{tid, ids[1], tid} {
		if err := s.AddPlaylistTrack(ctx, user, pl.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPlaylistTrack(ctx, user, pl.ID, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown track: %v", err)
	}
	if err := s.AddPlaylistTrack(ctx, other, pl.ID, tid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign playlist: %v", err)
	}
	if got, err := s.Playlist(ctx, user, pl.ID); err != nil || got.TrackCount != 2 {
		t.Fatalf("playlist: %+v %v", got, err)
	}
	if ts, err := s.PlaylistTracks(ctx, user, pl.ID); err != nil || len(ts) != 2 || ts[0].ID != tid {
		t.Fatalf("tracks: %+v %v", ts, err)
	}
	if err := s.RenamePlaylist(ctx, user, pl.ID, "Mix 2"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenamePlaylist(ctx, other, pl.ID, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign rename")
	}
	if ps, err := s.Playlists(ctx, user); err != nil || len(ps) != 1 || ps[0].Title != "Mix 2" {
		t.Fatalf("list: %+v %v", ps, err)
	}
	if err := s.RemovePlaylistTrack(ctx, user, pl.ID, tid); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePlaylistTrack(ctx, other, pl.ID, tid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign remove")
	}
	if ts, err := s.PlaylistTracks(ctx, other, pl.ID); err != nil || len(ts) != 0 {
		t.Fatal("foreign tracks are never returned (Library checks ownership first)")
	}
	if err := s.DeletePlaylist(ctx, other, pl.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign delete")
	}
	if err := s.DeletePlaylist(ctx, user, pl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Playlist(ctx, user, pl.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted")
	}

	// soft delete of tracks missing from a later full listing (scoped to this run)
	if _, err := pool.Exec(ctx, "UPDATE music.tracks SET synced_at = synced_at - interval '1 hour' WHERE navidrome_id = $1", run+"-tc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkMissingDeleted(ctx, synced.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.TrackActive(ctx, ids[2]); ok && ids[2] != tid {
		// ids[2] may or may not be "-tc" depending on search order; check by navidrome id instead
		var deleted bool
		_ = pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM music.tracks WHERE navidrome_id = $1", run+"-tc").Scan(&deleted)
		if !deleted {
			t.Fatal("missing track not soft-deleted")
		}
	}
	if n, err := s.CountActiveTracks(ctx); err != nil || n < 2 {
		t.Fatalf("active: %d %v", n, err)
	}
}
