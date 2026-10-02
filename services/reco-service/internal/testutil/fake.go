// Package testutil holds fakes for reco-service tests (excluded from coverage):
// an in-memory store and a fake music-service internal API.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
	"github.com/tapenest/tapenest/services/reco-service/internal/repo"
)

// Mem is an in-memory implementation of the ingest, jobs and httpapi ports.
type Mem struct {
	mu       sync.Mutex
	Tracks   map[uuid.UUID]repo.CatalogTrack
	Deleted  map[uuid.UUID]bool
	Features map[uuid.UUID][]float32
	Errors   map[uuid.UUID]string
	Signals  []repo.Signal
	keys     map[string]bool
	State    map[string]string
	Saved    []repo.Trained
	Affs     []repo.Affinity
	Version  int64
	Err      error
}

// NewMem creates an empty store.
func NewMem() *Mem {
	return &Mem{
		Tracks: map[uuid.UUID]repo.CatalogTrack{}, Deleted: map[uuid.UUID]bool{}, Features: map[uuid.UUID][]float32{},
		Errors: map[uuid.UUID]string{}, keys: map[string]bool{}, State: map[string]string{},
	}
}

// ApplySignal implements ingest.Store.
func (m *Mem) ApplySignal(_ context.Context, sg repo.Signal) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return false, m.Err
	}
	if m.keys[sg.Key] {
		return false, nil
	}
	m.keys[sg.Key] = true
	m.Signals = append(m.Signals, sg)
	return true, nil
}

// SignalCount is safe for concurrent use.
func (m *Mem) SignalCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Signals)
}

// TrackKeys implements ingest.Store.
func (m *Mem) TrackKeys(_ context.Context, id uuid.UUID) ([]profile.TasteKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.Tracks[id]
	if !ok {
		return nil, nil
	}
	return profile.KeysFor(t.ArtistID.String(), "", t.Genre, nil), nil
}

// GetState implements ingest.Store.
func (m *Mem) GetState(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.State[k], m.Err
}

// SetState implements ingest.Store.
func (m *Mem) SetState(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.State[k] = v
	return m.Err
}

// UpsertTracks implements jobs.Store.
func (m *Mem) UpsertTracks(_ context.Context, ts []repo.CatalogTrack, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range ts {
		m.Tracks[t.ID] = t
		delete(m.Deleted, t.ID)
	}
	return m.Err
}

// MarkMissingDeleted implements jobs.Store (nothing is missing in the fake).
func (m *Mem) MarkMissingDeleted(context.Context, time.Time) (int64, error) { return 0, m.Err }

// TracksNeedingAnalysis implements jobs.Store.
func (m *Mem) TracksNeedingAnalysis(_ context.Context, _, _, limit int) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for id := range m.Tracks {
		if _, ok := m.Features[id]; !ok && m.Errors[id] == "" && len(out) < limit {
			out = append(out, id)
		}
	}
	return out, m.Err
}

// SaveFeatures implements jobs.Store.
func (m *Mem) SaveFeatures(_ context.Context, id uuid.UUID, _ int, f repo.Features) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Features[id] = f.Raw
	return m.Err
}

// SaveAnalysisError implements jobs.Store.
func (m *Mem) SaveAnalysisError(_ context.Context, id uuid.UUID, _ int, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Errors[id] = msg
	return nil
}

// LoadModel implements jobs.Store / httpapi.ModelSource.
func (m *Mem) LoadModel(context.Context, int) (*model.Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ts []model.Track
	for id, t := range m.Tracks {
		mt := model.Track{
			ID: id, Title: t.Title, ArtistID: t.ArtistID, Artist: t.Artist, Genre: t.Genre, CreatedAt: t.CreatedAt,
			Popularity: float64(t.Popularity),
		}
		if r, ok := m.Features[id]; ok && len(r) == audio.RawDims {
			mt.Raw = r
		}
		ts = append(ts, mt)
	}
	md := model.New(ts)
	md.Version = m.Version
	return md, m.Err
}

// ModelVersion implements httpapi.ModelSource.
func (m *Mem) ModelVersion(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Version, m.Err
}

// PositiveAffinities implements jobs.Store.
func (m *Mem) PositiveAffinities(context.Context, time.Time) ([]repo.Affinity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Affs, m.Err
}

// SaveModel implements jobs.Store.
func (m *Mem) SaveModel(_ context.Context, t repo.Trained, _ time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Saved = append(m.Saved, t)
	m.Version++
	return m.Version, m.Err
}

// Users is a fake httpapi.UserStore.
type Users struct {
	U   *rank.User
	Err error
}

// LoadUser implements httpapi.UserStore.
func (u *Users) LoadUser(context.Context, uuid.UUID, *model.Model, time.Time) (*rank.User, error) {
	if u.U == nil {
		return &rank.User{}, u.Err
	}
	return u.U, u.Err
}

// ProfileCounts implements httpapi.UserStore.
func (u *Users) ProfileCounts(context.Context, uuid.UUID) (int64, int64, int64, int64, error) {
	return 3, 5, 1, 1, u.Err
}

// Ping implements httpapi.UserStore.
func (u *Users) Ping(context.Context) error { return u.Err }

// Music is a fake music-service internal API.
type Music struct {
	*httptest.Server
	Token        string
	Tracks       []map[string]any
	Interactions []map[string]any
	Audio        []byte
	Fail         bool
}

// NewMusic starts the fake server.
func NewMusic(token string) *Music {
	m := &Music{Token: token, Audio: []byte("RIFF")}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

func (m *Music) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Internal-Token") != m.Token {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	if m.Fail {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	switch {
	case r.URL.Path == "/internal/v1/catalog":
		after := r.URL.Query().Get("after")
		start := 0
		for i, t := range m.Tracks {
			if t["id"] == after {
				start = i + 1
			}
		}
		end := min(start+2, len(m.Tracks))
		var next any
		if end < len(m.Tracks) {
			next = m.Tracks[end-1]["id"]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": m.Tracks[start:end], "next": next})
	case r.URL.Path == "/internal/v1/interactions":
		for _, it := range m.Interactions {
			b, _ := json.Marshal(it)
			_, _ = fmt.Fprintf(w, "%s\n", b)
		}
		_, _ = io.WriteString(w, "not json\n")
	case strings.HasPrefix(r.URL.Path, "/internal/v1/tracks/"):
		_, _ = w.Write(m.Audio)
	default:
		http.NotFound(w, r)
	}
}
