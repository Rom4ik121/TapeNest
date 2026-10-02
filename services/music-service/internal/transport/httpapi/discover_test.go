package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/acq"
	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
	"github.com/tapenest/tapenest/services/music-service/internal/transport/httpapi"
)

// fakeAcq emulates acquisition-service's internal API (ADR 0011).
type fakeAcq struct {
	mu      sync.Mutex
	mode    string // "", "failed", "quota", "disabled"
	calls   map[uuid.UUID]int
	reasons []string
	rg1     uuid.UUID
	rg2     uuid.UUID
	artist  uuid.UUID
	recs    [3]uuid.UUID
	run     string
	audio   []byte
}

func newFakeAcq(run string) *fakeAcq {
	f := &fakeAcq{calls: map[uuid.UUID]int{}, rg1: uuid.New(), rg2: uuid.New(), artist: uuid.New(), run: run, audio: bytes.Repeat([]byte("ID3audio"), 64)}
	for i := range f.recs {
		f.recs[i] = uuid.New()
	}
	return f
}

func (f *fakeAcq) handler(t *testing.T) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Internal-Token") != "acq-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	js := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	artist := "Test Artist " + f.run
	r.Get("/internal/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		js(w, acq.SearchResult{
			Artists: []acq.Artist{{MBID: f.artist, Name: artist}},
			Albums: []acq.ReleaseGroup{
				{MBID: f.rg1, Title: "First Album " + f.run, Year: 2020, ArtistMBID: f.artist, Artist: artist},
				{MBID: f.rg2, Title: "Second Album " + f.run, Year: 2021, ArtistMBID: f.artist, Artist: artist},
			},
			Recordings: []acq.Recording{
				{MBID: f.recs[0], Title: "Song One " + f.run, LengthMS: 180000, ArtistMBID: f.artist, Artist: artist, ReleaseGroupMBID: f.rg1, Album: "First Album " + f.run},
				{MBID: f.recs[1], Title: "Song Two " + f.run, LengthMS: 200000, ArtistMBID: f.artist, Artist: artist, ReleaseGroupMBID: f.rg1, Album: "First Album " + f.run},
				{MBID: uuid.New(), Title: "No Album " + f.run, Artist: artist}, // skipped: no release group
			},
		})
	})
	r.Get("/internal/v1/albums/{rg}", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "rg") != f.rg1.String() {
			http.NotFound(w, r)
			return
		}
		a := acq.Album{ReleaseGroup: acq.ReleaseGroup{MBID: f.rg1, Title: "First Album " + f.run, ArtistMBID: f.artist, Artist: artist}}
		for i, rec := range f.recs {
			a.Tracks = append(a.Tracks, acq.Track{RecordingMBID: rec, Title: fmt.Sprintf("Song %d %s", i+1, f.run), Disc: 1, Position: i + 1, LengthMS: 100000})
		}
		js(w, a)
	})
	r.Get("/internal/v1/artists/{id}", func(w http.ResponseWriter, _ *http.Request) {
		js(w, acq.ArtistView{Artist: acq.Artist{MBID: f.artist, Name: artist}, Albums: []acq.ReleaseGroup{{MBID: f.rg1, Title: "First Album " + f.run, ArtistMBID: f.artist, Artist: artist}}})
	})
	r.Post("/internal/v1/acquisitions", func(w http.ResponseWriter, r *http.Request) {
		var in acq.AcquireRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ReleaseGroupMBID == uuid.Nil || in.UserID == uuid.Nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reasons = append(f.reasons, in.Reason)
		switch f.mode {
		case "quota":
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case "disabled":
			w.WriteHeader(http.StatusServiceUnavailable)
			js(w, map[string]string{"code": "DISABLED"})
			return
		case "failed":
			js(w, map[string]any{"state": "failed", "errorCode": "NO_SOURCES"})
			return
		}
		f.calls[in.RecordingMBID]++
		switch f.calls[in.RecordingMBID] {
		case 1:
			js(w, map[string]any{"state": "queued"})
		case 2:
			js(w, map[string]any{"state": "downloading", "progress": 0.4, "stream": map[string]bool{"ready": false}})
		default:
			js(w, map[string]any{"state": "downloading", "progress": 0.6, "stream": map[string]bool{"ready": true}})
		}
	})
	r.Get("/internal/v1/recordings/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.audio))
	})
	r.Get("/internal/v1/admin/status", func(w http.ResponseWriter, _ *http.Request) { js(w, map[string]any{"indexers": 1}) })
	r.Get("/internal/v1/admin/acquisitions", func(w http.ResponseWriter, _ *http.Request) { js(w, []any{}) })
	r.Get("/internal/v1/admin/acquisitions/{id}", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return r
}

func TestUnifiedCatalogAndInvisibleAcquisition(t *testing.T) {
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
	fa := newFakeAcq(run)
	acqSrv := httptest.NewServer(fa.handler(t))
	t.Cleanup(acqSrv.Close)
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM music.likes WHERE track_id IN (SELECT id FROM music.tracks WHERE title LIKE '%' || $1)",
			"DELETE FROM music.tracks WHERE title LIKE '%' || $1",
			"DELETE FROM music.albums WHERE title LIKE '%' || $1",
			"DELETE FROM music.artists WHERE name LIKE '%' || $1",
			"DELETE FROM music.acquired_files WHERE path LIKE $1 || '%'",
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
	ac := acq.New(acqSrv.URL, "acq-token")
	var refreshed atomic.Int32
	sig := signer.New(strings.Repeat("k", 32), "")
	e := &env{mem: testutil.NewMem(1), nd: nd, ndc: ndc, mr: mr, rdb: rdb}
	e.srv = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Library:  service.NewLibrary(store).WithAcquirer(ac, log).WithBackground(func(fn func()) { fn() }),
		Wave:     service.NewWave(store, rdb),
		Events:   service.NewEvents(rdb),
		Streamer: service.NewStreamer(store, ndc, sig, time.Hour, log).WithAcquirer(ac),
		Discovery: service.NewDiscovery(store, ac, log).WithRefreshNotifier(func(context.Context) error {
			refreshed.Add(1)
			return nil
		}),
		Internal:      service.NewInternal(store),
		InternalToken: token,
		Log:           log,
	}))
	t.Cleanup(e.srv.Close)

	// 1. unified search: external results become placeholders rendered like library items
	r := e.do(t, "GET", "/api/v1/search?q=Song+"+run, "")
	want(t, r, 200)
	var sr struct {
		Tracks  []map[string]any `json:"tracks"`
		Albums  []map[string]any `json:"albums"`
		Artists []map[string]any `json:"artists"`
	}
	_ = json.Unmarshal([]byte(r.raw), &sr)
	if len(sr.Tracks) != 2 || len(sr.Albums) != 2 || len(sr.Artists) != 1 {
		t.Fatalf("search: %s", r.raw)
	}
	if sr.Tracks[0]["remote"] != true || sr.Albums[0]["coverUrl"] == nil {
		t.Fatalf("remote track/cover: %s", r.raw)
	}
	again := e.do(t, "GET", "/api/v1/search?q=Song+"+run, "")
	if !strings.Contains(again.raw, sr.Tracks[0]["id"].(string)) {
		t.Fatal("placeholder ids must be stable across searches")
	}
	want(t, e.do(t, "GET", "/api/v1/search?q=", ""), 400)

	// 2. album view fills the tracklist in order; artist view lists the discography
	var albumID string
	for _, a := range sr.Albums {
		if strings.HasPrefix(a["title"].(string), "First Album") {
			albumID = a["id"].(string)
		}
	}
	r = e.do(t, "GET", "/api/v1/albums/"+albumID, "")
	want(t, r, 200)
	var av struct {
		Album  map[string]any   `json:"album"`
		Tracks []map[string]any `json:"tracks"`
	}
	_ = json.Unmarshal([]byte(r.raw), &av)
	if len(av.Tracks) != 3 || av.Tracks[0]["id"] != sr.Tracks[0]["id"] {
		t.Fatalf("album: %s", r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/albums/"+uuid.NewString(), ""), 404)
	want(t, e.do(t, "GET", "/api/v1/albums/nope", ""), 400)
	r = e.do(t, "GET", "/api/v1/artists/"+sr.Artists[0]["id"].(string), "")
	want(t, r, 200)
	if !strings.Contains(r.raw, "First Album "+run) {
		t.Fatalf("artist: %s", r.raw)
	}

	// 3. play a not-yet-available track: 202 (queued) → 202 (downloading) → 200 URL → partial stream
	song1 := sr.Tracks[0]["id"].(string)
	r = e.do(t, "GET", "/api/v1/tracks/"+song1+"/stream-url", "")
	want(t, r, 202)
	if r.body["state"] != "queued" || r.header.Get("Retry-After") == "" {
		t.Fatalf("pending: %s %v", r.raw, r.header)
	}
	r = e.do(t, "GET", "/api/v1/tracks/"+song1+"/stream-url", "")
	if r.status != 202 || r.body["retryAfterMs"] != float64(800) {
		t.Fatalf("downloading: %d %s", r.status, r.raw)
	}
	r = e.do(t, "GET", "/api/v1/tracks/"+song1+"/stream-url", "")
	want(t, r, 200)
	audio := e.do(t, "GET", r.body["url"].(string), "", "Range", "bytes=0-7")
	if audio.status != 206 || audio.raw != "ID3audio" || audio.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("partial stream: %d %q %v", audio.status, audio.raw, audio.header)
	}

	// 4. like of a remote track: saved now, acquisition in the background
	song2 := sr.Tracks[1]["id"].(string)
	want(t, e.do(t, "POST", "/api/v1/tracks/"+song2+"/like", ""), 204)
	fa.mu.Lock()
	lastReason := fa.reasons[len(fa.reasons)-1]
	fa.mu.Unlock()
	if lastReason != "like" {
		t.Fatalf("like must acquire in background, got reason %q", lastReason)
	}
	if liked := e.do(t, "GET", "/api/v1/tracks/liked", ""); !strings.Contains(liked.raw, song2) {
		t.Fatalf("liked: %s", liked.raw)
	}

	// 5. failures map to small, specific errors
	for mode, st := range map[string]struct {
		status int
		code   string
	}{"failed": {404, "NO_SOURCES"}, "quota": {429, "ACQUIRE_QUOTA"}, "disabled": {404, "NOT_AVAILABLE"}} {
		fa.mu.Lock()
		fa.mode = mode
		fa.mu.Unlock()
		r = e.do(t, "GET", "/api/v1/tracks/"+song2+"/stream-url", "")
		if r.status != st.status || r.body["code"] != st.code {
			t.Fatalf("%s: %d %s", mode, r.status, r.raw)
		}
	}
	fa.mu.Lock()
	fa.mode = ""
	fa.mu.Unlock()

	// 6. admin status (role from the gateway)
	want(t, e.do(t, "GET", "/api/v1/admin/acquisition-status", ""), 403)
	r = e.do(t, "GET", "/api/v1/admin/acquisition-status", "", "X-User-Role", "admin")
	if r.status != 200 || r.body["indexers"] != float64(1) {
		t.Fatalf("admin status: %d %s", r.status, r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/admin/acquisitions?state=failed", "", "X-User-Role", "admin"), 200)
	want(t, e.do(t, "GET", "/api/v1/admin/acquisitions/"+uuid.NewString(), "", "X-User-Role", "admin"), 404)
	want(t, e.do(t, "GET", "/api/v1/admin/acquisitions/bad", "", "X-User-Role", "admin"), 400)

	// 7. import: acquisition-service reports files → sync claims placeholders (same ids)
	body := fmt.Sprintf(`{"files":[{"path":"%s/a/01.mp3","recordingMbid":"%s","releaseGroupMbid":"%s","sizeBytes":424242},
	  {"path":"%s/a/03.mp3","recordingMbid":"%s","releaseGroupMbid":"%s","sizeBytes":434343}]}`,
		run, fa.recs[0], fa.rg1, run, fa.recs[2], fa.rg1)
	want(t, e.do(t, "POST", "/internal/v1/catalog/refresh", body), 202)
	want(t, e.do(t, "POST", "/internal/v1/catalog/refresh", `{"files":[{"path":"../etc/passwd","recordingMbid":"`+fa.recs[0].String()+`"}]}`), 400)
	want(t, e.do(t, "POST", "/internal/v1/catalog/refresh", `{`), 400)
	if refreshed.Load() != 1 {
		t.Fatalf("refresh notifications: %d", refreshed.Load())
	}
	past := time.Now().Add(-2 * time.Hour)
	cache := repo.NewSyncCache()
	for _, ct := range []repo.CatalogTrack{
		// no MusicBrainz tag (Navidrome hides paths): claimed by exact size
		{NavidromeID: run + "-n1", Title: "Song One " + run, Artist: "Tagged Artist " + run, ArtistNavID: run + "-ar", Album: "Tagged Album " + run, AlbumNavID: run + "-al", SizeBytes: 424242, DurationSec: 180},
		// placeholder of track 3 was created by the album view: claimed by size too
		{NavidromeID: run + "-n3", Title: "Song 3 " + run, Artist: "Tagged Artist " + run, ArtistNavID: run + "-ar", Album: "Tagged Album " + run, AlbumNavID: run + "-al", SizeBytes: 434343, DurationSec: 100},
		// unrelated library file
		{NavidromeID: run + "-n9", Title: "Other " + run, Artist: "Tagged Artist " + run, ArtistNavID: run + "-ar", SizeBytes: 1, DurationSec: 60},
	} {
		if err := store.UpsertCatalogTrack(ctx, cache, ct, past); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}
	st, err := store.TrackForStream(ctx, uuid.MustParse(song1))
	if err != nil || st.NavidromeID != run+"-n1" || st.Recording != fa.recs[0] {
		t.Fatalf("claimed track keeps its id: %+v %v", st, err)
	}
	var libAlbumMBID *uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT mbid FROM music.albums WHERE navidrome_id = $1", run+"-al").Scan(&libAlbumMBID); err != nil {
		t.Fatal(err)
	}
	if libAlbumMBID == nil || *libAlbumMBID != fa.rg1 {
		t.Fatalf("library album must carry the release group: %v", libAlbumMBID)
	}
	// a like survives the hand-off (same id)
	want(t, e.do(t, "POST", "/api/v1/tracks/"+song1+"/like", ""), 204)

	// 8. file vanished → the track becomes a placeholder again (likes kept, re-acquirable)
	if _, err := store.MarkMissingDeleted(ctx, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, err = store.TrackForStream(ctx, uuid.MustParse(song1))
	if err != nil || st.NavidromeID != "" || st.Recording != fa.recs[0] {
		t.Fatalf("reverted to remote: %+v %v", st, err)
	}
	if liked := e.do(t, "GET", "/api/v1/tracks/liked", ""); !strings.Contains(liked.raw, song1) {
		t.Fatalf("like must survive: %s", liked.raw)
	}
}
