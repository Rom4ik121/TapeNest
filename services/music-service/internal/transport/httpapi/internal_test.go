package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
)

func TestInternalExports(t *testing.T) {
	e := newEnv(t)
	// user header not required for service-to-service routes
	r := e.do(t, "GET", "/internal/v1/catalog?limit=10", "", "X-User-Id", "")
	want(t, r, 200)
	if len(r.body["items"].([]any)) != 10 || r.body["next"] == nil {
		t.Fatalf("page 1: %s", r.raw)
	}
	item := r.body["items"].([]any)[0].(map[string]any)
	if item["genre"] != "Jazz" || item["artistId"] == "" {
		t.Fatalf("item: %v", item)
	}
	r2 := e.do(t, "GET", "/internal/v1/catalog?limit=10&after="+r.body["next"].(string), "", "X-User-Id", "")
	if len(r2.body["items"].([]any)) != 5 || r2.body["next"] != nil {
		t.Fatalf("page 2: %s", r2.raw)
	}
	want(t, e.do(t, "GET", "/internal/v1/catalog?after=x", ""), 400)
	want(t, e.do(t, "GET", "/internal/v1/catalog", "", "X-Internal-Token", "wrong"), 401)

	id := e.mem.Tracks[0].ID.String()
	if r := e.do(t, "POST", "/api/v1/tracks/"+id+"/like", ""); r.status >= 300 {
		t.Fatalf("like: %d", r.status)
	}
	r = e.do(t, "GET", "/internal/v1/interactions?days=7", "", "X-User-Id", "")
	want(t, r, 200)
	lines := strings.Split(strings.TrimSpace(r.raw), "\n")
	var first map[string]any
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &first) != nil || first["kind"] != "like" || first["trackId"] != id {
		t.Fatalf("interactions: %q", r.raw)
	}
	a := e.do(t, "GET", "/internal/v1/tracks/"+id+"/audio", "", "X-User-Id", "")
	if a.status != 200 && a.status != 206 {
		t.Fatalf("audio: %d %s", a.status, a.raw)
	}
	want(t, e.do(t, "GET", "/internal/v1/tracks/x/audio", ""), 400)
	want(t, e.do(t, "GET", "/internal/v1/tracks/"+uuid.NewString()+"/audio", ""), 404)
	e.mem.SetErr(testutil.ErrBoom)
	want(t, e.do(t, "GET", "/internal/v1/catalog", ""), 500)
	r = e.do(t, "GET", "/internal/v1/interactions", "")
	if r.status != 200 || r.raw != "" {
		t.Fatalf("failed stream is cut: %d %q", r.status, r.raw)
	}
}

func TestWaveModesAndSkips(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, "POST", "/api/v1/wave/sessions", `{"mode":"energetic"}`)
	want(t, r, 200)
	if r.body["mode"] != "energetic" || r.body["strategy"] != "fallback" || r.header.Get("X-Wave-Strategy") != "fallback" {
		t.Fatalf("start: %v %s", r.header, r.raw)
	}
	tr := r.body["tracks"].([]any)[0].(map[string]any)
	if tr["reason"].(map[string]any)["kind"] == "" {
		t.Fatalf("reason: %v", tr)
	}
	want(t, e.do(t, "POST", "/api/v1/wave/sessions", `{"mode":"party"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/wave/sessions", `{bad`), 400)

	id := e.mem.Tracks[2].ID.String()
	want(t, e.do(t, "POST", "/api/v1/events/track-skipped", `{"trackId":"`+id+`","positionSec":7}`), 204)
	if n, _ := e.rdb.XLen(context.Background(), "music:user_events").Result(); n != 1 {
		t.Fatalf("skip event: %d", n)
	}
	if n, _ := e.rdb.XLen(context.Background(), "music:play_events").Result(); n != 0 {
		t.Fatal("skips must not become play events")
	}
	want(t, e.do(t, "POST", "/api/v1/events/track-skipped", `{"trackId":"x","positionSec":7}`), 400)
	want(t, e.do(t, "POST", "/api/v1/events/track-skipped", `{"trackId":"`+id+`"}`), 400)
	want(t, e.do(t, "POST", "/api/v1/events/track-skipped", `{"trackId":"`+id+`","positionSec":-1}`), 400)
	e.mr.Close()
	want(t, e.do(t, "POST", "/api/v1/events/track-skipped", `{"trackId":"`+id+`","positionSec":1}`), 500)
}
