package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/httpapi"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
	"github.com/tapenest/tapenest/services/reco-service/internal/testutil"
)

const token = "internal-token-0123456789abcdef"

var log = slog.New(slog.NewTextHandler(io.Discard, nil))

func snapshot() *model.Model {
	var ts []model.Track
	for i := 0; i < 12; i++ {
		raw := make([]float32, audio.RawDims)
		raw[(i/4)%audio.RawDims] = 3 // groups of 4 sound alike
		raw[2] = float32(i) / 10
		ts = append(ts, model.Track{
			ID: uuid.New(), Title: "Song", Artist: "Artist", ArtistID: uuid.New(), Genre: "Jazz",
			Popularity: float64(i) / 12, Raw: raw, CreatedAt: time.Now().AddDate(0, -1, 0),
		})
	}
	m := model.New(ts)
	m.Version = 7
	m.CF[0] = []model.Neighbor{{Idx: 1, Sim: 0.9}}
	return m
}

func setup(t *testing.T, users *testutil.Users, m *model.Model) http.Handler {
	t.Helper()
	models := &httpapi.Models{Log: log}
	if m != nil {
		models.Set(m)
	}
	return httpapi.NewRouter(httpapi.Deps{Models: models, Users: users, Weights: rank.DefaultWeights(), InternalToken: token, Log: log})
}

func do(h http.Handler, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if auth {
		req.Header.Set("X-Internal-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNext(t *testing.T) {
	m := snapshot()
	u := &rank.User{Tracks: map[int]rank.UserTrack{0: {Affinity: 3, Liked: true, Plays: 1}}, Taste: map[string]float64{"genre:Jazz": 2}}
	h := setup(t, &testutil.Users{U: u}, m)
	body := map[string]any{
		"userId": uuid.New(), "sessionId": uuid.New(), "limit": 5, "mode": "default",
		"exclude": []uuid.UUID{m.Tracks[0].ID, uuid.New()}, "recent": []uuid.UUID{m.Tracks[0].ID},
		"feedback": []map[string]any{{"trackId": m.Tracks[3].ID, "action": "like"}, {"trackId": m.Tracks[4].ID, "action": "meh"}},
	}
	rec := do(h, http.MethodPost, "/internal/v1/wave/next", body, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var out struct {
		ModelVersion int64 `json:"modelVersion"`
		Tracks       []struct {
			TrackID string `json:"trackId"`
			Source  string `json:"source"`
			Reason  *struct {
				Kind       string `json:"kind"`
				RefTrackID string `json:"refTrackId"`
				RefTitle   string `json:"refTitle"`
			} `json:"reason"`
		} `json:"tracks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ModelVersion != 7 || len(out.Tracks) != 5 {
		t.Fatalf("response %s", rec.Body)
	}
	refs := 0
	for _, tr := range out.Tracks {
		if tr.TrackID == m.Tracks[0].ID.String() {
			t.Fatal("excluded track returned")
		}
		if tr.Reason == nil || tr.Source == "" {
			t.Fatalf("missing reason/source %s", rec.Body)
		}
		if tr.Reason.RefTrackID != "" && tr.Reason.RefTitle == "Song" {
			refs++
		}
	}
	if refs == 0 {
		t.Fatalf("expected a 'because you liked' reference: %s", rec.Body)
	}
}

func TestNextErrors(t *testing.T) {
	m := snapshot()
	h := setup(t, &testutil.Users{}, m)
	if rec := do(h, http.MethodPost, "/internal/v1/wave/next", map[string]any{"userId": uuid.New()}, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/wave/next", bytes.NewBufferString("{"))
	req.Header.Set("X-Internal-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/internal/v1/wave/next", map[string]any{"userId": uuid.New(), "mode": "party"}, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/internal/v1/wave/next", map[string]any{"userId": uuid.New(), "limit": 500}, true); rec.Code != http.StatusOK {
		t.Fatalf("limit is clamped: %d", rec.Code)
	}
	empty := setup(t, &testutil.Users{}, nil)
	if rec := do(empty, http.MethodPost, "/internal/v1/wave/next", map[string]any{"userId": uuid.New()}, true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no model: %d", rec.Code)
	}
	failing := setup(t, &testutil.Users{Err: errors.New("db down")}, m)
	if rec := do(failing, http.MethodPost, "/internal/v1/wave/next", map[string]any{"userId": uuid.New()}, true); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error: %d", rec.Code)
	}
	if rec := do(failing, http.MethodGet, "/readyz", nil, false); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with db down: %d", rec.Code)
	}
	if rec := do(empty, http.MethodGet, "/nope", nil, true); rec.Code != http.StatusNotFound {
		t.Fatal("404")
	}
}

func TestProfileSimilarModel(t *testing.T) {
	m := snapshot()
	u := &rank.User{
		Taste:   map[string]float64{"artist:" + m.Tracks[1].ArtistID.String(): 3, "genre:Jazz": 1, "album:x": 1, "tag:calm": 0.5},
		Sources: map[string]rank.Beta{"cf": {Alpha: 2, Beta: 1}},
	}
	h := setup(t, &testutil.Users{U: u}, m)
	rec := do(h, http.MethodGet, "/internal/v1/users/"+uuid.NewString()+"/profile", nil, true)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"label":"Artist"`)) || bytes.Contains(rec.Body.Bytes(), []byte("album")) {
		t.Fatalf("profile %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/internal/v1/users/nope/profile", nil, true); rec.Code != http.StatusBadRequest {
		t.Fatal("bad user id")
	}
	rec = do(h, http.MethodGet, "/internal/v1/tracks/"+m.Tracks[0].ID.String()+"/similar?limit=3", nil, true)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"cf":[{`)) {
		t.Fatalf("similar %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/internal/v1/tracks/"+uuid.NewString()+"/similar", nil, true); rec.Code != http.StatusNotFound {
		t.Fatal("unknown track")
	}
	if rec := do(h, http.MethodGet, "/internal/v1/tracks/x/similar", nil, true); rec.Code != http.StatusBadRequest {
		t.Fatal("bad track id")
	}
	rec = do(h, http.MethodGet, "/internal/v1/model", nil, true)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"withAudioFeatures":12`)) {
		t.Fatalf("model %s", rec.Body)
	}
	if rec := do(h, http.MethodGet, "/readyz", nil, false); rec.Code != http.StatusOK {
		t.Fatalf("readyz %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/healthz", nil, false); rec.Code != http.StatusOK {
		t.Fatal("healthz")
	}
	empty := setup(t, &testutil.Users{}, nil)
	for _, p := range []string{"/internal/v1/model", "/internal/v1/tracks/" + uuid.NewString() + "/similar", "/internal/v1/users/" + uuid.NewString() + "/profile"} {
		if rec := do(empty, http.MethodGet, p, nil, true); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without model: %d", p, rec.Code)
		}
	}
	failing := setup(t, &testutil.Users{Err: errors.New("x")}, m)
	if rec := do(failing, http.MethodGet, "/internal/v1/users/"+uuid.NewString()+"/profile", nil, true); rec.Code != http.StatusInternalServerError {
		t.Fatal("profile error")
	}
}

func TestModelsRefresh(t *testing.T) {
	store := testutil.NewMem()
	store.Tracks[uuid.New()] = repo.CatalogTrack{Title: "a"}
	models := &httpapi.Models{Src: store, Log: log}
	if err := models.Refresh(context.Background(), time.Hour); err != nil || models.Current() == nil {
		t.Fatalf("first load %v", err)
	}
	first := models.Current()
	_ = models.Refresh(context.Background(), time.Hour)
	if models.Current() != first {
		t.Fatal("unchanged version must not reload")
	}
	store.Version = 2
	_ = models.Refresh(context.Background(), time.Hour)
	if models.Current().Version != 2 {
		t.Fatal("new version must reload")
	}
	store.Err = errors.New("db")
	if err := models.Refresh(context.Background(), time.Hour); err == nil {
		t.Fatal("error expected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	models.Run(ctx, time.Millisecond)
}
