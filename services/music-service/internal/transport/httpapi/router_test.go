package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
	"github.com/tapenest/tapenest/services/music-service/internal/transport/httpapi"
)

const token = "internal-token-internal-token-123"

var userA = uuid.MustParse("11111111-1111-4111-8111-111111111111")

type env struct {
	srv   *httptest.Server
	mem   *testutil.Mem
	nd    *testutil.Navidrome
	ndc   *navidrome.Client
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	ready error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: testutil.NewMem(15)}
	var songs []navidrome.Song
	for _, tr := range e.mem.Tracks {
		songs = append(songs, navidrome.Song{ID: tr.NavidromeID, Title: tr.Title})
	}
	e.nd = testutil.NewNavidrome(songs)
	t.Cleanup(e.nd.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var err error
	e.ndc, err = navidrome.New(e.nd.URL, "admin", "pw", log)
	if err != nil {
		t.Fatal(err)
	}
	e.mr = miniredis.RunT(t)
	e.rdb = redis.NewClient(&redis.Options{Addr: e.mr.Addr()})
	sig := signer.New(strings.Repeat("k", 32), "")
	h := httpapi.NewRouter(httpapi.Deps{
		Library:       service.NewLibrary(e.mem),
		Wave:          service.NewWave(e.mem, e.rdb),
		Events:        service.NewEvents(e.rdb),
		Streamer:      service.NewStreamer(e.mem, e.ndc, sig, time.Hour, log),
		Internal:      service.NewInternal(e.mem),
		InternalToken: token,
		Log:           log,
		Ready:         map[string]httpapi.Pinger{"postgres": func(context.Context) error { return e.ready }},
		Optional:      map[string]httpapi.Pinger{"navidrome": e.ndc.Ping},
	})
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

type res struct {
	status int
	header http.Header
	raw    string
	body   map[string]any
	list   []any
}

func (e *env) do(t *testing.T, method, path, body string, hdr ...string) res {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", userA.String())
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := res{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	_ = json.Unmarshal(raw, &out.list)
	return out
}

func want(t *testing.T, r res, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.raw)
	}
}

func TestHealthAndAuth(t *testing.T) {
	e := newEnv(t)
	want(t, e.do(t, "GET", "/healthz", ""), 200)
	r := e.do(t, "GET", "/readyz", "")
	if r.status != 200 || r.body["navidrome"] != "ok" || r.body["postgres"] != "ok" {
		t.Fatalf("readyz: %s", r.raw)
	}
	e.nd.Down.Store(true)
	e.ready = testutil.ErrBoom
	r = e.do(t, "GET", "/readyz", "")
	if r.status != 503 || r.body["navidrome"] != "degraded" || r.body["postgres"] != "down" {
		t.Fatalf("readyz degraded: %d %s", r.status, r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/tracks/popular", "", "X-Internal-Token", ""), 401)
	want(t, e.do(t, "GET", "/api/v1/tracks/popular", "", "X-Internal-Token", "wrong"), 401)
	want(t, e.do(t, "GET", "/api/v1/tracks/popular", "", "X-User-Id", ""), 401)
	want(t, e.do(t, "GET", "/api/v1/tracks/popular", "", "X-User-Id", "not-a-uuid"), 401)
	want(t, e.do(t, "GET", "/api/v1/nope", ""), 404)
	want(t, e.do(t, "PUT", "/api/v1/playlists", ""), 405)
}

func TestCatalogAndLikes(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, "GET", "/api/v1/tracks/popular?limit=5", "")
	want(t, r, 200)
	items := r.body["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 5 || r.body["nextCursor"] == nil || first["title"] != "Track 00" {
		t.Fatalf("popular: %s", r.raw)
	}
	if cover, _ := first["coverUrl"].(string); !strings.HasPrefix(cover, "/api/v1/stream/covers/al-0?") {
		t.Fatalf("signed cover url: %v", first["coverUrl"])
	}
	r2 := e.do(t, "GET", "/api/v1/tracks/popular?limit=5&cursor="+r.body["nextCursor"].(string), "")
	if r2.body["items"].([]any)[0].(map[string]any)["title"] != "Track 05" {
		t.Fatalf("page 2: %s", r2.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/tracks/popular?cursor=!!notbase64", ""), 400)
	want(t, e.do(t, "GET", "/api/v1/tracks/popular?limit=abc", ""), 200) // default limit
	if r := e.do(t, "GET", "/api/v1/tracks/search?q=track%2001", ""); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("search: %s", r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/tracks/search?q=", ""), 400)
	want(t, e.do(t, "GET", "/api/v1/tracks/search?q="+strings.Repeat("x", 300), ""), 400)

	id := first["id"].(string)
	want(t, e.do(t, "POST", "/api/v1/tracks/"+id+"/like", ""), 204)
	want(t, e.do(t, "POST", "/api/v1/tracks/"+id+"/like", ""), 204) // idempotent
	liked := e.do(t, "GET", "/api/v1/tracks/liked", "")
	if it := liked.body["items"].([]any); len(it) != 1 || it[0].(map[string]any)["liked"] != true {
		t.Fatalf("liked: %s", liked.raw)
	}
	want(t, e.do(t, "POST", "/api/v1/tracks/"+uuid.NewString()+"/like", ""), 404)
	want(t, e.do(t, "POST", "/api/v1/tracks/nope/like", ""), 400)
	want(t, e.do(t, "DELETE", "/api/v1/tracks/"+id+"/like", ""), 204)
	want(t, e.do(t, "DELETE", "/api/v1/tracks/bad/like", ""), 400)
	if r := e.do(t, "GET", "/api/v1/tracks/liked", ""); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("unliked: %s", r.raw)
	}
	e.mem.Err = testutil.ErrBoom
	want(t, e.do(t, "GET", "/api/v1/tracks/recent", ""), 500)
	want(t, e.do(t, "GET", "/api/v1/tracks/search?q=a", ""), 500)
	want(t, e.do(t, "POST", "/api/v1/tracks/"+id+"/like", ""), 500)
	want(t, e.do(t, "DELETE", "/api/v1/tracks/"+id+"/like", ""), 500)
	want(t, e.do(t, "GET", "/api/v1/playlists", ""), 500)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions", ""), 500)
}

func TestPositionAndEvents(t *testing.T) {
	e := newEnv(t)
	id := e.mem.Tracks[3].ID.String()
	want(t, e.do(t, "GET", "/api/v1/tracks/"+id+"/position", ""), 404)
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+id+"/position", `{"positionSec":12.5}`), 204)
	r := e.do(t, "GET", "/api/v1/tracks/"+id+"/position", "")
	if r.status != 200 || r.body["positionSec"] != 12.5 || r.body["trackId"] != id {
		t.Fatalf("position: %s", r.raw)
	}
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+id+"/position", `{"positionSec":-1}`), 400)
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+id+"/position", `{"positionSec":99999999}`), 400)
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+id+"/position", `{}`), 400)
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+id+"/position", `{bad`), 400)
	want(t, e.do(t, "PUT", "/api/v1/tracks/x/position", `{"positionSec":1}`), 400)
	want(t, e.do(t, "GET", "/api/v1/tracks/x/position", ""), 400)
	want(t, e.do(t, "PUT", "/api/v1/tracks/"+uuid.NewString()+"/position", `{"positionSec":1}`), 404)
	// recent reflects the saved position
	if r := e.do(t, "GET", "/api/v1/tracks/recent", ""); r.body["items"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatalf("recent: %s", r.raw)
	}

	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `{"trackId":"`+id+`","positionSec":30,"completed":true}`), 204)
	if n, _ := e.rdb.XLen(context.Background(), "music:play_events").Result(); n != 1 {
		t.Fatalf("event published: %d", n)
	}
	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `{"trackId":"x","positionSec":30}`), 400)
	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `{"trackId":"`+id+`"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `{"trackId":"`+id+`","positionSec":-5}`), 400)
	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `nope`), 400)
	e.mr.Close()
	want(t, e.do(t, "POST", "/api/v1/events/track-listened", `{"trackId":"`+id+`","positionSec":1}`), 500)
}

func TestPlaylists(t *testing.T) {
	e := newEnv(t)
	tid := e.mem.Tracks[0].ID.String()
	r := e.do(t, "POST", "/api/v1/playlists", `{"title":"  Road  "}`)
	want(t, r, 200)
	pid := r.body["id"].(string)
	if r.body["title"] != "Road" || r.body["trackCount"] != 0.0 {
		t.Fatalf("create: %s", r.raw)
	}
	want(t, e.do(t, "POST", "/api/v1/playlists", `{"title":""}`), 400)
	want(t, e.do(t, "POST", "/api/v1/playlists", `{"title":"`+strings.Repeat("я", 101)+`"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/playlists", `[`), 400)
	want(t, e.do(t, "POST", "/api/v1/playlists/"+pid+"/tracks", `{"trackId":"`+tid+`"}`), 204)
	want(t, e.do(t, "POST", "/api/v1/playlists/"+pid+"/tracks", `{"trackId":"`+tid+`"}`), 204)
	want(t, e.do(t, "POST", "/api/v1/playlists/"+pid+"/tracks", `{"trackId":"`+uuid.NewString()+`"}`), 404)
	want(t, e.do(t, "POST", "/api/v1/playlists/"+pid+"/tracks", `{"trackId":"bad"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/playlists/"+pid+"/tracks", `x`), 400)
	want(t, e.do(t, "POST", "/api/v1/playlists/bad/tracks", `{}`), 400)
	r = e.do(t, "GET", "/api/v1/playlists/"+pid, "")
	if r.status != 200 || len(r.body["tracks"].([]any)) != 1 || r.body["playlist"].(map[string]any)["trackCount"] != 1.0 {
		t.Fatalf("get: %s", r.raw)
	}
	want(t, e.do(t, "GET", "/api/v1/playlists/bad", ""), 400)
	if r := e.do(t, "PATCH", "/api/v1/playlists/"+pid, `{"title":"Night"}`); r.status != 200 || r.body["title"] != "Night" {
		t.Fatalf("rename: %s", r.raw)
	}
	want(t, e.do(t, "PATCH", "/api/v1/playlists/"+pid, `{"title":" "}`), 400)
	want(t, e.do(t, "PATCH", "/api/v1/playlists/"+pid, `{`), 400)
	want(t, e.do(t, "PATCH", "/api/v1/playlists/bad", `{"title":"x"}`), 400)
	want(t, e.do(t, "PATCH", "/api/v1/playlists/"+uuid.NewString(), `{"title":"x"}`), 404)
	// another user cannot see it
	other := "22222222-2222-4222-8222-222222222222"
	want(t, e.do(t, "GET", "/api/v1/playlists/"+pid, "", "X-User-Id", other), 404)
	if r := e.do(t, "GET", "/api/v1/playlists", "", "X-User-Id", other); len(r.list) != 0 {
		t.Fatalf("isolation: %s", r.raw)
	}
	if r := e.do(t, "GET", "/api/v1/playlists", ""); len(r.list) != 1 {
		t.Fatalf("list: %s", r.raw)
	}
	want(t, e.do(t, "DELETE", "/api/v1/playlists/"+pid+"/tracks/"+tid, ""), 204)
	want(t, e.do(t, "DELETE", "/api/v1/playlists/"+pid+"/tracks/bad", ""), 400)
	want(t, e.do(t, "DELETE", "/api/v1/playlists/bad/tracks/"+tid, ""), 400)
	want(t, e.do(t, "DELETE", "/api/v1/playlists/"+pid, ""), 204)
	want(t, e.do(t, "DELETE", "/api/v1/playlists/"+pid, ""), 404)
	want(t, e.do(t, "DELETE", "/api/v1/playlists/bad", ""), 400)
	want(t, e.do(t, "GET", "/api/v1/playlists/"+pid, ""), 404)
}

func TestPlaylistLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < service.MaxPlaylists; i++ {
		want(t, e.do(t, "POST", "/api/v1/playlists", `{"title":"p"}`), 200)
	}
	want(t, e.do(t, "POST", "/api/v1/playlists", `{"title":"p"}`), 400)
}

func TestWave(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, "POST", "/api/v1/wave/sessions", "")
	want(t, r, 200)
	sid := r.body["sessionId"].(string)
	tracks := r.body["tracks"].([]any)
	if len(tracks) != service.WaveBatch {
		t.Fatalf("batch: %d", len(tracks))
	}
	for i := 1; i < len(tracks); i++ {
		if tracks[i].(map[string]any)["artist"] == tracks[i-1].(map[string]any)["artist"] {
			t.Fatalf("same artist twice in a row at %d", i)
		}
	}
	last := tracks[len(tracks)-1].(map[string]any)["id"].(string)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"`+last+`","action":"like"}`), 204)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"`+last+`","action":"skip"}`), 204)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"`+uuid.NewString()+`","action":"skip"}`), 204)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"`+last+`","action":"love"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"x","action":"like"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `x`), 400)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/bad/feedback", `{}`), 400)
	next := e.do(t, "GET", "/api/v1/wave/sessions/"+sid+"/tracks?after="+last, "")
	want(t, next, 200)
	seen := map[string]bool{}
	for _, tr := range tracks {
		seen[tr.(map[string]any)["id"].(string)] = true
	}
	for _, tr := range next.list {
		if seen[tr.(map[string]any)["id"].(string)] {
			t.Fatal("next batch repeats served tracks")
		}
	}
	if len(next.list) != 5 {
		t.Fatalf("remaining: %d", len(next.list))
	}
	// catalog exhausted → new round (excluding the last batch)
	third := e.do(t, "GET", "/api/v1/wave/sessions/"+sid+"/tracks?after=x", "")
	if len(third.list) == 0 {
		t.Fatalf("new round: %s", third.raw)
	}
	other := "22222222-2222-4222-8222-222222222222"
	want(t, e.do(t, "GET", "/api/v1/wave/sessions/"+sid+"/tracks", "", "X-User-Id", other), 404)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions/"+sid+"/feedback", `{"trackId":"`+last+`","action":"like"}`, "X-User-Id", other), 404)
	want(t, e.do(t, "GET", "/api/v1/wave/sessions/"+uuid.NewString()+"/tracks", ""), 404)
	want(t, e.do(t, "GET", "/api/v1/wave/sessions/bad/tracks", ""), 400)
	e.mr.Close()
	want(t, e.do(t, "POST", "/api/v1/wave/sessions", ""), 500)
	want(t, e.do(t, "GET", "/api/v1/wave/sessions/"+sid+"/tracks", ""), 500)
}

func TestStreaming(t *testing.T) {
	e := newEnv(t)
	id := e.mem.Tracks[2].ID.String()
	r := e.do(t, "GET", "/api/v1/tracks/"+id+"/stream-url", "")
	want(t, r, 200)
	if r.header.Get("Cache-Control") != "no-store" || r.body["expiresAt"] == nil {
		t.Fatalf("stream-url: %s", r.raw)
	}
	u := r.body["url"].(string)
	// full + range + HEAD
	full := e.do(t, "GET", u, "")
	if full.status != 200 || len(full.raw) != 10000 || full.header.Get("Accept-Ranges") != "bytes" || full.header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("full: %d %v", full.status, full.header)
	}
	part := e.do(t, "GET", u, "", "Range", "bytes=10-19")
	if part.status != 206 || part.raw != "0123456789" || part.header.Get("Content-Range") != "bytes 10-19/10000" {
		t.Fatalf("range: %d %q %v", part.status, part.raw, part.header)
	}
	if h := e.do(t, "HEAD", u, ""); h.status != 200 || h.header.Get("Content-Length") != "10000" || h.raw != "" {
		t.Fatalf("head: %d %v", h.status, h.header)
	}
	// stream routes need the internal token but not a user
	if r := e.do(t, "GET", u, "", "X-User-Id", "", "Range", "bytes=0-0"); r.status != 206 {
		t.Fatalf("no user: %d", r.status)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	q.Set("sig", "AAAA")
	want(t, e.do(t, "GET", pu.Path+"?"+q.Encode(), ""), 403)
	want(t, e.do(t, "GET", "/api/v1/stream/tracks/"+uuid.NewString()+"?"+pu.RawQuery, ""), 403)
	want(t, e.do(t, "GET", "/api/v1/stream/tracks/bad", ""), 400)
	want(t, e.do(t, "GET", "/api/v1/tracks/bad/stream-url", ""), 400)
	want(t, e.do(t, "GET", "/api/v1/tracks/"+uuid.NewString()+"/stream-url", ""), 404)

	// cover
	cover := e.do(t, "GET", "/api/v1/tracks/popular?limit=1", "").body["items"].([]any)[0].(map[string]any)["coverUrl"].(string)
	c := e.do(t, "GET", cover, "", "X-User-Id", "")
	if c.status != 200 || c.header.Get("Content-Type") != "image/jpeg" || !strings.Contains(c.header.Get("Cache-Control"), "immutable") {
		t.Fatalf("cover: %d %v", c.status, c.header)
	}
	want(t, e.do(t, "HEAD", cover, ""), 200)
	want(t, e.do(t, "GET", strings.Replace(cover, "size=300", "size=301", 1), ""), 403)
	sig := signer.New(strings.Repeat("k", 32), "")
	want(t, e.do(t, "GET", sig.CoverURL("missing", 300), ""), 404)

	// deleted file in Navidrome → 404
	e.mem.Tracks[2].NavidromeID = "gone"
	want(t, e.do(t, "GET", u, ""), 404)

	// Navidrome down: breaker opens after 3 failures, stream-url says 503 streaming
	e.nd.Down.Store(true)
	for i := 0; i < 3; i++ {
		r := e.do(t, "GET", cover, "")
		if r.status != 503 || r.header.Get(httpapi.DegradedHeader) != "navidrome" {
			t.Fatalf("down #%d: %d %v", i, r.status, r.header)
		}
	}
	r = e.do(t, "GET", "/api/v1/tracks/"+id+"/stream-url", "")
	if r.status != 503 || r.body["service"] != "streaming" || r.header.Get("Retry-After") == "" || r.header.Get(httpapi.DegradedHeader) != "navidrome" {
		t.Fatalf("degraded stream-url: %d %s", r.status, r.raw)
	}
	// catalog keeps working
	want(t, e.do(t, "GET", "/api/v1/tracks/popular", ""), 200)
}
