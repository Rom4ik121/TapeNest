package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tapenest/tapenest/services/streaming-service/internal/catalog"
	"github.com/tapenest/tapenest/services/streaming-service/internal/preview"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
	"github.com/tapenest/tapenest/services/streaming-service/internal/service"
	"github.com/tapenest/tapenest/services/streaming-service/internal/sign"
)

func TestCinemaAPI(t *testing.T) {
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
	store := repo.New(pool)
	if err := store.Seed(ctx, catalog.Build()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:4.0,\nseg00.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seg00.ts"), []byte("ts"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("k", 32)
	signer := sign.New(key, time.Hour)
	h := NewRouter(Deps{
		Cinema:        &service.Cinema{Store: store, Sign: signer, Warmup: 0},
		Store:         store,
		Sign:          signer,
		Preview:       preview.New(dir),
		InternalToken: "internal-token-0123456789",
	})
	user := uuid.New()
	tok := map[string]string{"X-Internal-Token": "internal-token-0123456789", "X-User-Id": user.String()}
	titles := catalog.Build()
	first := titles[0]

	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles?q=янтар", tok); rec.Code != 200 {
		t.Fatalf("search %d %s", rec.Code, rec.Body.String())
	} else if !strings.Contains(rec.Body.String(), first.Title) {
		t.Fatalf("body %s", rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles?kind=series&limit=5", tok); rec.Code != 200 || !strings.Contains(rec.Body.String(), "nextCursor") {
		t.Fatalf("series %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles?kind=nope", tok); rec.Code != 400 {
		t.Fatalf("kind %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles/"+first.ID.String(), tok); rec.Code != 200 || !strings.Contains(rec.Body.String(), "1080p") {
		t.Fatalf("card %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodPut, "/api/v1/cinema/titles/"+first.ID.String()+"/watchlist", tok); rec.Code != 204 {
		t.Fatalf("watch %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/watchlist", tok); rec.Code != 200 || !strings.Contains(rec.Body.String(), first.ID.String()) {
		t.Fatalf("list watch %s", rec.Body.String())
	}
	file := first.Files[1].ID.String()
	body := `{"fileId":"` + file + `","positionSec":120.4,"durationSec":3000}`
	if rec := doBody(h, http.MethodPut, "/api/v1/cinema/titles/"+first.ID.String()+"/position", tok, body); rec.Code != 204 {
		t.Fatalf("pos %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles/"+first.ID.String()+"/position?fileId="+file, tok); rec.Code != 200 || !strings.Contains(rec.Body.String(), "120.4") {
		t.Fatalf("get pos %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/continue", tok); rec.Code != 200 || !strings.Contains(rec.Body.String(), first.ID.String()) {
		t.Fatalf("continue %s", rec.Body.String())
	}
	if n, err := store.ExpireSessions(ctx, time.Now().Add(time.Hour)); err != nil || n < 0 {
		t.Fatal(err)
	}
	streamBody := `{"titleId":"` + first.ID.String() + `","fileId":"` + file + `"}`
	rec := doBody(h, http.MethodPost, "/api/v1/cinema/streams", tok, streamBody)
	if rec.Code != 201 {
		t.Fatalf("stream %d %s", rec.Code, rec.Body.String())
	}
	var sess map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if sess["status"] != "ready" || sess["hlsUrl"] == nil {
		t.Fatalf("sess %+v", sess)
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/streams/"+sess["id"].(string), tok); rec.Code != 200 {
		t.Fatalf("get stream %d %s", rec.Code, rec.Body.String())
	}
	hlsURL, _ := sess["hlsUrl"].(string)
	if rec := do(h, http.MethodGet, hlsURL, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "/hls/") {
		t.Fatalf("hls %d %s", rec.Code, rec.Body.String())
	} else if seg := rec.Body.String(); !strings.Contains(seg, "seg00.ts") {
		t.Fatal(seg)
	} else {
		line := ""
		for _, ln := range strings.Split(seg, "\n") {
			if strings.HasPrefix(ln, "/hls/") {
				line = ln
				break
			}
		}
		if rec := do(h, http.MethodGet, line, nil); rec.Code != 200 || rec.Body.String() != "ts" {
			t.Fatalf("seg %d %s", rec.Code, rec.Body.String())
		}
	}
	id, _ := sess["id"].(string)
	if rec := do(h, http.MethodDelete, "/api/v1/cinema/streams/"+id, tok); rec.Code != 204 {
		t.Fatalf("stop %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/streams/"+id, tok); rec.Code != 404 {
		t.Fatalf("gone %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles", nil); rec.Code != 401 {
		t.Fatalf("auth %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/v1/cinema/admin/titles", tok); rec.Code != 403 {
		t.Fatalf("admin %d", rec.Code)
	}
	admin := map[string]string{"X-Internal-Token": tok["X-Internal-Token"], "X-User-Id": user.String(), "X-User-Role": "admin"}
	if rec := doBody(h, http.MethodPost, "/api/v1/cinema/admin/titles", admin, `{"title":"Тестовая лента","kind":"movie","year":2026}`); rec.Code != 201 {
		t.Fatalf("add %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/admin/audit", admin); rec.Code != 200 || !strings.Contains(rec.Body.String(), "add_title") {
		t.Fatalf("audit %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/api/v1/cinema/titles?cursor=@@@", tok); rec.Code != 400 {
		t.Fatalf("cursor %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/healthz", nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := do(h, http.MethodGet, "/readyz", nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	return doBody(h, method, path, hdr, "")
}

func doBody(h http.Handler, method, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
