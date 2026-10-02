package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/mb"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/streamer"
)

type adminMem struct{ id uuid.UUID }

func (a adminMem) ListRequests(context.Context, db.ListRequestsParams) ([]db.AcquisitionRequest, error) {
	return []db.AcquisitionRequest{{ID: a.id, ArtistName: "Ada", AlbumTitle: "Songs", State: "queued", CreatedAt: time.Now(), UpdatedAt: time.Now()}}, nil
}

func (a adminMem) RequestByID(context.Context, uuid.UUID) (db.AcquisitionRequest, error) {
	return db.AcquisitionRequest{ID: a.id, State: "queued", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func (a adminMem) RequestTracks(context.Context, uuid.UUID) ([]db.AcquisitionRequestTrack, error) {
	return []db.AcquisitionRequestTrack{{Title: "Aria", RecordingMbid: uuid.New()}}, nil
}

func (a adminMem) RequestUsers(context.Context, uuid.UUID) ([]db.RequestUsersRow, error) {
	return []db.RequestUsersRow{{}}, nil
}

func (a adminMem) RequestEvents(context.Context, uuid.UUID) ([]db.RequestEventsRow, error) {
	return []db.RequestEventsRow{{Kind: "joined", At: time.Now(), Detail: []byte(`{"reason":"play"}`)}}, nil
}

func (a adminMem) CountByState(context.Context) ([]db.CountByStateRow, error) {
	return []db.CountByStateRow{{State: "queued", N: 1}}, nil
}
func (a adminMem) Ping(context.Context) error { return nil }

type info struct{}

func (info) IndexerCount(context.Context) (int, error) { return 1, nil }
func (info) Disk(context.Context) (core.DiskStats, error) {
	return core.DiskStats{LibraryBytes: 10}, nil
}

func TestRouter(t *testing.T) {
	rec := uuid.New()
	rg := uuid.New()
	dir := t.TempDir()
	libFile := filepath.Join(dir, "library", "a.mp3")
	_ = os.MkdirAll(filepath.Dir(libFile), 0o755)
	_ = os.WriteFile(libFile, []byte("mp3data"), 0o644)
	// minimal service using core tests' types is another package. Build a tiny meta via httptest? Use core.Service with a local meta.
	svc := &core.Service{Enabled: false, Meta: httpMeta{rg: rg, rec: rec}, Limits: core.Limits{UserDaily: 5, UserActive: 5, StreamStartBytes: 1024}}
	tok := strings.Repeat("t", 24)
	h := NewRouter(Deps{
		Service: svc, Admin: adminMem{id: uuid.New()}, Streamer: streamer.New(nil),
		Required: Health{"postgres": func(context.Context) error { return nil }},
		Optional: Health{"lidarr": func(context.Context) error { return io.EOF }},
		Info:     info{}, Limits: Limits{ContentSources: "p2p", TorrentMaxGB: 1, MaxActive: 1},
		MusicDir: dir, InternalToken: tok, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	get := func(path string, code int) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("X-Internal-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != code {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, b)
		}
		return string(b)
	}
	if !strings.Contains(get("/healthz", 200), "ok") {
		t.Fatal("health")
	}
	// unauth
	resp, _ := http.Get(srv.URL + "/internal/v1/search?q=ada")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
	if !strings.Contains(get("/internal/v1/search?q=ada", 200), "artists") {
		t.Fatal("search")
	}
	if get("/internal/v1/search?q="+strings.Repeat("a", 201), 400) == "" {
		t.Fatal("long")
	}
	body := `{"userId":"` + uuid.New().String() + `","releaseGroupMbid":"` + rg.String() + `","recordingMbid":"` + rec.String() + `","title":"Aria","reason":"play"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/v1/acquisitions", strings.NewReader(body))
	req.Header.Set("X-Internal-Token", tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("acquire %d %s", resp.StatusCode, b)
	}
	_ = get("/readyz", 200)
	_ = get("/internal/v1/admin/acquisitions", 200)
	_ = get("/internal/v1/admin/status", 200)

	id := uuid.New()
	_ = get("/internal/v1/albums/"+rg.String(), 404)
	_ = get("/internal/v1/artists/"+id.String(), 200)
	_ = get("/internal/v1/admin/acquisitions/"+id.String(), 200)
	_ = get("/internal/v1/albums/nope", 400)
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/v1/acquisitions", strings.NewReader("{"))
	req2.Header.Set("X-Internal-Token", tok)
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatal(resp2.StatusCode)
	}
	_ = get("/nope", 404)
}

type httpMeta struct{ rg, rec uuid.UUID }

func (h httpMeta) SearchArtists(context.Context, string, int) ([]mb.Artist, error) {
	return []mb.Artist{{Name: "Ada", MBID: uuid.New()}}, nil
}

func (h httpMeta) SearchReleaseGroups(context.Context, string, int) ([]mb.ReleaseGroup, error) {
	return []mb.ReleaseGroup{{Title: "Songs", MBID: h.rg, Artist: "Ada"}}, nil
}

func (h httpMeta) SearchRecordings(context.Context, string, int) ([]mb.Recording, error) {
	return []mb.Recording{{Title: "Aria", MBID: h.rec, ReleaseGroupMBID: h.rg, Artist: "Ada"}}, nil
}

func (h httpMeta) Album(context.Context, uuid.UUID) (mb.Album, error) {
	return mb.Album{}, mb.ErrNotFound
}

func (h httpMeta) Discography(context.Context, uuid.UUID, int) (mb.Artist, []mb.ReleaseGroup, error) {
	return mb.Artist{Name: "Ada"}, nil, nil
}

var _ = repo.ErrNotFound
