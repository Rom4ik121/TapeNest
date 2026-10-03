package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
	"github.com/tapenest/tapenest/services/music-service/internal/transport/httpapi"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

// fakeYT is a YouTube Music catalog that never touches the network.
type fakeYT struct {
	cat   ytm.Catalog
	album []ytm.Track
}

func (f *fakeYT) Search(context.Context, string) (ytm.Catalog, error) { return f.cat, nil }
func (f *fakeYT) Album(context.Context, string) ([]ytm.Track, error)  { return f.album, nil }

type fakeAudio struct{}

func (fakeAudio) Proxy(w http.ResponseWriter, _ *http.Request, videoID string) error {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	_, err := w.Write([]byte("yt:" + videoID))
	return err
}

func TestYouTubeCatalog(t *testing.T) {
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
	if err := repo.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := repo.NewStore(pool, pool)
	run := uuid.NewString()[:8]
	video := "abcdefghijk"
	title := "Aria " + run
	artist := "Kimiko " + run
	albumTitle := "Goldberg " + run
	yt := &fakeYT{
		cat: ytm.Catalog{
			Tracks:  []ytm.Track{{VideoID: video, Title: title, Artist: artist, Album: albumTitle, DurationSec: 180}},
			Albums:  []ytm.Album{{BrowseID: "MPREb_goldberg1", Title: albumTitle, Artist: artist, Year: 2015}},
			Artists: []ytm.Artist{{BrowseID: "UCkimikoishizaka1", Name: artist}},
		},
		album: []ytm.Track{{VideoID: video, Title: title, Artist: artist, DurationSec: 180}},
	}
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM music.likes WHERE track_id IN (SELECT id FROM music.tracks WHERE title LIKE '%' || $1)",
			"DELETE FROM music.tracks WHERE title LIKE '%' || $1 OR artist LIKE '%' || $1",
			"DELETE FROM music.albums WHERE title LIKE '%' || $1",
			"DELETE FROM music.artists WHERE name LIKE '%' || $1",
		} {
			_, _ = pool.Exec(ctx, q, run)
		}
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nd := testutil.NewNavidrome(nil)
	t.Cleanup(nd.Close)
	ndc, err := navidrome.New(nd.URL, "admin", "pw", log)
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	sig := signer.New(strings.Repeat("k", 32), "")
	e := &env{mem: testutil.NewMem(1), nd: nd, ndc: ndc, mr: mr, rdb: rdb}
	e.srv = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Library:       service.NewLibrary(store),
		Wave:          service.NewWave(store, rdb),
		Events:        service.NewEvents(rdb),
		Streamer:      service.NewStreamer(store, ndc, sig, time.Hour, log).WithYouTubeAudio(fakeAudio{}),
		Discovery:     service.NewDiscovery(store, log).WithYouTube(yt),
		Internal:      service.NewInternal(store),
		InternalToken: token,
		Log:           log,
	}))
	t.Cleanup(e.srv.Close)

	r := e.do(t, "GET", "/api/v1/search?q="+title, "")
	want(t, r, 200)
	var sr struct {
		Tracks  []map[string]any `json:"tracks"`
		Albums  []map[string]any `json:"albums"`
		Artists []map[string]any `json:"artists"`
	}
	_ = json.Unmarshal([]byte(r.raw), &sr)
	if len(sr.Tracks) != 1 || len(sr.Albums) != 1 || len(sr.Artists) != 1 {
		t.Fatalf("search: %s", r.raw)
	}
	if sr.Tracks[0]["remote"] != true {
		t.Fatalf("youtube row: %s", r.raw)
	}
	again := e.do(t, "GET", "/api/v1/search?q="+title, "")
	if !strings.Contains(again.raw, sr.Tracks[0]["id"].(string)) {
		t.Fatal("youtube ids must be stable across searches")
	}
	want(t, e.do(t, "GET", "/api/v1/search?q=", ""), 400)

	albumID := sr.Albums[0]["id"].(string)
	r = e.do(t, "GET", "/api/v1/albums/"+albumID, "")
	want(t, r, 200)
	if !strings.Contains(r.raw, sr.Tracks[0]["id"].(string)) {
		t.Fatalf("album: %s", r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/albums/"+uuid.NewString(), ""), 404)
	r = e.do(t, "GET", "/api/v1/artists/"+sr.Artists[0]["id"].(string), "")
	want(t, r, 200)
	if !strings.Contains(r.raw, albumTitle) {
		t.Fatalf("artist: %s", r.raw)
	}

	song := sr.Tracks[0]["id"].(string)
	r = e.do(t, "GET", "/api/v1/tracks/"+song+"/stream-url", "")
	want(t, r, 200)
	audio := e.do(t, "GET", r.body["url"].(string), "")
	if audio.status != 200 || !strings.Contains(audio.raw, video) {
		t.Fatalf("youtube audio: %d %q", audio.status, audio.raw)
	}
	want(t, e.do(t, "POST", "/api/v1/tracks/"+song+"/like", ""), 204)
	if liked := e.do(t, "GET", "/api/v1/tracks/liked", ""); !strings.Contains(liked.raw, song) {
		t.Fatalf("liked: %s", liked.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/admin/acquisitions", "", "X-User-Role", "admin"), 404)
}
