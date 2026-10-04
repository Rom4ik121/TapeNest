package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/transport/httpapi"
)

type fakeAdminStore struct {
	health repo.CatalogHealth
	music  repo.UserMusic
}

func (f fakeAdminStore) CatalogHealth(context.Context) (repo.CatalogHealth, error) {
	return f.health, nil
}

func (f fakeAdminStore) UserMusic(context.Context, uuid.UUID) (repo.UserMusic, error) {
	return f.music, nil
}

func TestAdminMusicReads(t *testing.T) {
	e := newEnv(t)
	if r := e.do(t, "GET", "/api/v1/admin/catalog", "", "X-User-Role", "user"); r.status != 403 {
		t.Fatalf("non-admin catalog: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "GET", "/api/v1/admin/catalog", "", "X-User-Role", "admin"); r.status != 501 {
		t.Fatalf("unwired catalog: %d %s", r.status, r.raw)
	}

	trackID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	userID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	synced := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	h := httpapi.NewRouter(httpapi.Deps{
		Admin: service.NewAdmin(fakeAdminStore{
			health: repo.CatalogHealth{Artists: 2, Albums: 3, Tracks: 10, LibraryTracks: 7, YouTubeTracks: 3, LastSyncedAt: &synced},
			music: repo.UserMusic{
				Likes:   []repo.AdminTrack{{ID: trackID, Title: "Aria", Artist: "Bach", Album: "Suites"}},
				Artists: []repo.TasteCount{{Name: "Bach", Count: 4}},
				Genres:  []repo.TasteCount{{Name: "Classical", Count: 4}},
			},
		}),
		InternalToken: token,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/admin/catalog", nil)
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", userA.String())
	req.Header.Set("X-User-Role", "admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !containsAll(string(raw), `"tracks":10`, `"libraryTracks":7`) || containsAny(string(raw), "streamUrl", "signing", "navidrome") {
		t.Fatalf("catalog: %d %s", resp.StatusCode, raw)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/admin/users/"+userID.String()+"/music", nil)
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-User-Id", userA.String())
	req.Header.Set("X-User-Role", "admin")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !containsAll(string(raw), "Aria", "Bach", "Classical") {
		t.Fatalf("user music: %d %s", resp.StatusCode, raw)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if contains(s, p) {
			return true
		}
	}
	return false
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || len(s) > 0 && (stringIndex(s, part) >= 0)))
}

func stringIndex(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
