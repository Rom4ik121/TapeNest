// Package testutil holds in-memory fakes for music-service tests (excluded
// from coverage): an in-memory Store and a fake Navidrome (Subsonic) server.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// MemTrack is a catalog row.
type MemTrack struct {
	domain.Track
	ArtistID    uuid.UUID
	NavidromeID string
	Popularity  float64
	Deleted     bool
}

type upKey struct{ u, t uuid.UUID }

type memPlaylist struct {
	domain.Playlist
	owner  uuid.UUID
	tracks []uuid.UUID
}

// Mem implements service.Store, worker.EventStore and worker.CatalogStore.
type Mem struct {
	mu        sync.Mutex
	Tracks    []*MemTrack
	likes     map[upKey]time.Time
	positions map[upKey]domain.Position
	recent    map[upKey]time.Time
	playlists map[uuid.UUID]*memPlaylist
	Events    []repo.PlayEvent
	Synced    map[string]repo.CatalogTrack
	syncedAt  map[string]time.Time
	Parts     []string
	Refreshes int
	Err       error // returned by every call when set
}

// NewMem creates a store with n tracks (artist i%3).
func NewMem(n int) *Mem {
	m := &Mem{
		likes: map[upKey]time.Time{}, positions: map[upKey]domain.Position{}, recent: map[upKey]time.Time{},
		playlists: map[uuid.UUID]*memPlaylist{}, Synced: map[string]repo.CatalogTrack{}, syncedAt: map[string]time.Time{},
	}
	artists := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i := 0; i < n; i++ {
		m.Tracks = append(m.Tracks, &MemTrack{
			Track: domain.Track{
				ID: uuid.New(), Title: fmt.Sprintf("Track %02d", i), Artist: fmt.Sprintf("Artist %d", i%3),
				Album: "Album", CoverArtID: fmt.Sprintf("al-%d", i%3), DurationSec: 60 + i,
			},
			ArtistID: artists[i%3], NavidromeID: fmt.Sprintf("nd-%d", i), Popularity: float64(n - i),
		})
	}
	return m
}

func (m *Mem) find(id uuid.UUID) *MemTrack {
	for _, t := range m.Tracks {
		if t.ID == id && !t.Deleted {
			return t
		}
	}
	return nil
}

func (m *Mem) view(u uuid.UUID, t *MemTrack) domain.Track {
	out := t.Track
	_, out.Liked = m.likes[upKey{u, t.ID}]
	return out
}

func (m *Mem) page(u uuid.UUID, ts []*MemTrack, cur *domain.Cursor, limit int) domain.Page {
	start := 0
	if cur != nil {
		for i, t := range ts {
			if t.ID == cur.ID {
				start = i + 1
			}
		}
	}
	p := domain.Page{Items: []domain.Track{}}
	for i := start; i < len(ts) && len(p.Items) < limit; i++ {
		p.Items = append(p.Items, m.view(u, ts[i]))
	}
	if start+limit < len(ts) && len(p.Items) > 0 {
		score, at := 0.0, time.Now()
		s := domain.Cursor{ID: p.Items[len(p.Items)-1].ID, Score: &score, At: &at}.Encode()
		p.Next = &s
	}
	return p
}

func (m *Mem) active() []*MemTrack {
	var out []*MemTrack
	for _, t := range m.Tracks {
		if !t.Deleted {
			out = append(out, t)
		}
	}
	return out
}

// Popular implements service.Store.
func (m *Mem) Popular(_ context.Context, u uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.active()
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Popularity > ts[j].Popularity })
	return m.page(u, ts, cur, limit), m.Err
}

// Search implements service.Store.
func (m *Mem) Search(_ context.Context, u uuid.UUID, q, _ string, cur *domain.Cursor, limit int) (domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ts []*MemTrack
	for _, t := range m.active() {
		if strings.Contains(strings.ToLower(t.Title+" "+t.Artist), q) {
			ts = append(ts, t)
		}
	}
	return m.page(u, ts, cur, limit), m.Err
}

func (m *Mem) byTime(u uuid.UUID, src map[upKey]time.Time) []*MemTrack {
	type kv struct {
		t  *MemTrack
		at time.Time
	}
	var rows []kv
	for k, at := range src {
		if k.u == u {
			if t := m.find(k.t); t != nil {
				rows = append(rows, kv{t, at})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	out := make([]*MemTrack, len(rows))
	for i, r := range rows {
		out[i] = r.t
	}
	return out
}

// Liked implements service.Store.
func (m *Mem) Liked(_ context.Context, u uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.page(u, m.byTime(u, m.likes), cur, limit), m.Err
}

// Recent implements service.Store.
func (m *Mem) Recent(_ context.Context, u uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.page(u, m.byTime(u, m.recent), cur, limit), m.Err
}

// TracksByIDs implements service.Store.
func (m *Mem) TracksByIDs(_ context.Context, u uuid.UUID, ids []uuid.UUID) ([]domain.Track, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.Track{}
	for _, id := range ids {
		if t := m.find(id); t != nil {
			v := m.view(u, t)
			v.ArtistID = t.ArtistID
			out = append(out, v)
		}
	}
	return out, m.Err
}

// TrackForStream implements service.Store.
func (m *Mem) TrackForStream(_ context.Context, id uuid.UUID) (repo.StreamTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.find(id)
	if t == nil {
		return repo.StreamTarget{}, domain.ErrNotFound
	}
	return repo.StreamTarget{NavidromeID: t.NavidromeID, ContentType: "audio/mpeg"}, m.Err
}

// TrackActive implements service.Store.
func (m *Mem) TrackActive(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.find(id) != nil, m.Err
}

// Like implements service.Store.
func (m *Mem) Like(_ context.Context, u, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	if m.find(id) == nil {
		return domain.ErrNotFound
	}
	if _, ok := m.likes[upKey{u, id}]; !ok {
		m.likes[upKey{u, id}] = time.Now()
	}
	return nil
}

// Unlike implements service.Store.
func (m *Mem) Unlike(_ context.Context, u, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.likes, upKey{u, id})
	return m.Err
}

// SavePosition implements service.Store.
func (m *Mem) SavePosition(_ context.Context, u, id uuid.UUID, pos float64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	if m.find(id) == nil {
		return domain.ErrNotFound
	}
	m.positions[upKey{u, id}] = domain.Position{TrackID: id, PositionSec: pos, UpdatedAt: at}
	m.recent[upKey{u, id}] = at
	return nil
}

// Position implements service.Store.
func (m *Mem) Position(_ context.Context, u, id uuid.UUID) (domain.Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.positions[upKey{u, id}]
	if !ok {
		return domain.Position{}, domain.ErrNotFound
	}
	return p, m.Err
}

func (m *Mem) pl(u, id uuid.UUID) (*memPlaylist, error) {
	p, ok := m.playlists[id]
	if !ok || p.owner != u {
		return nil, domain.ErrNotFound
	}
	return p, nil
}

func (p *memPlaylist) dto() domain.Playlist {
	out := p.Playlist
	out.TrackCount = len(p.tracks)
	return out
}

// Playlists implements service.Store.
func (m *Mem) Playlists(_ context.Context, u uuid.UUID) ([]domain.Playlist, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.Playlist{}
	for _, p := range m.playlists {
		if p.owner == u {
			out = append(out, p.dto())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, m.Err
}

// CountPlaylists implements service.Store.
func (m *Mem) CountPlaylists(ctx context.Context, u uuid.UUID) (int, error) {
	ps, err := m.Playlists(ctx, u)
	return len(ps), err
}

// CreatePlaylist implements service.Store.
func (m *Mem) CreatePlaylist(_ context.Context, u uuid.UUID, title string) (domain.Playlist, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return domain.Playlist{}, m.Err
	}
	p := &memPlaylist{Playlist: domain.Playlist{ID: uuid.New(), Title: title, CreatedAt: time.Now()}, owner: u}
	m.playlists[p.ID] = p
	return p.dto(), nil
}

// Playlist implements service.Store.
func (m *Mem) Playlist(_ context.Context, u, id uuid.UUID) (domain.Playlist, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.pl(u, id)
	if err != nil {
		return domain.Playlist{}, err
	}
	return p.dto(), nil
}

// RenamePlaylist implements service.Store.
func (m *Mem) RenamePlaylist(_ context.Context, u, id uuid.UUID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.pl(u, id)
	if err != nil {
		return err
	}
	p.Title = title
	return nil
}

// DeletePlaylist implements service.Store.
func (m *Mem) DeletePlaylist(_ context.Context, u, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pl(u, id); err != nil {
		return err
	}
	delete(m.playlists, id)
	return nil
}

// AddPlaylistTrack implements service.Store.
func (m *Mem) AddPlaylistTrack(_ context.Context, u, id, track uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.pl(u, id)
	if err != nil {
		return err
	}
	if m.find(track) == nil {
		return domain.ErrNotFound
	}
	for _, t := range p.tracks {
		if t == track {
			return nil
		}
	}
	p.tracks = append(p.tracks, track)
	return nil
}

// RemovePlaylistTrack implements service.Store.
func (m *Mem) RemovePlaylistTrack(_ context.Context, u, id, track uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.pl(u, id)
	if err != nil {
		return err
	}
	for i, t := range p.tracks {
		if t == track {
			p.tracks = append(p.tracks[:i], p.tracks[i+1:]...)
			break
		}
	}
	return nil
}

// PlaylistTracks implements service.Store.
func (m *Mem) PlaylistTracks(_ context.Context, u, id uuid.UUID) ([]domain.Track, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.pl(u, id)
	if err != nil {
		return nil, err
	}
	out := []domain.Track{}
	for _, tid := range p.tracks {
		if t := m.find(tid); t != nil {
			out = append(out, m.view(u, t))
		}
	}
	return out, nil
}

// WaveCandidates implements service.Store.
func (m *Mem) WaveCandidates(_ context.Context, u uuid.UUID, limit int) ([]repo.WaveCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	likedArtists := map[uuid.UUID]bool{}
	for k := range m.likes {
		if t := m.find(k.t); t != nil && k.u == u {
			likedArtists[t.ArtistID] = true
		}
	}
	var out []repo.WaveCandidate
	for _, t := range m.active() {
		if len(out) >= limit {
			break
		}
		c := repo.WaveCandidate{ID: t.ID, ArtistID: t.ArtistID, Popularity: t.Popularity, LikedArtist: likedArtists[t.ArtistID]}
		_, c.Liked = m.likes[upKey{u, t.ID}]
		if at, ok := m.recent[upKey{u, t.ID}]; ok {
			c.LastPlayed = &at
		}
		out = append(out, c)
	}
	return out, m.Err
}

// InsertPlayEvents implements worker.EventStore (idempotent by event id).
func (m *Mem) InsertPlayEvents(_ context.Context, evs []repo.PlayEvent) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return 0, m.Err
	}
	var n int64
outer:
	for _, e := range evs {
		for _, have := range m.Events {
			if have.EventID == e.EventID {
				continue outer
			}
		}
		m.Events = append(m.Events, e)
		m.recent[upKey{e.UserID, e.TrackID}] = e.PlayedAt
		n++
	}
	return n, nil
}

// UpsertCatalogTrack implements worker.CatalogStore.
func (m *Mem) UpsertCatalogTrack(_ context.Context, _ *repo.SyncCache, t repo.CatalogTrack, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.Synced[t.NavidromeID] = t
	m.syncedAt[t.NavidromeID] = at
	return nil
}

// MarkMissingDeleted implements worker.CatalogStore.
func (m *Mem) MarkMissingDeleted(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, at := range m.syncedAt {
		if at.Before(before) {
			delete(m.Synced, id)
			delete(m.syncedAt, id)
			n++
		}
	}
	return n, m.Err
}

// CountActiveTracks implements worker.CatalogStore.
func (m *Mem) CountActiveTracks(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.Synced)), m.Err
}

// RefreshPopularity implements worker.CatalogStore.
func (m *Mem) RefreshPopularity(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Refreshes++
	return m.Err
}

// EnsurePartition implements worker.CatalogStore.
func (m *Mem) EnsurePartition(_ context.Context, month time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := "play_events_" + month.Format("2006_01")
	m.Parts = append(m.Parts, name)
	return name, m.Err
}

// Navidrome is a fake Subsonic server: ping, startScan, search3 (paged), raw
// stream with Range (http.ServeContent) and cover art.
type Navidrome struct {
	*httptest.Server
	Admins atomic.Int32
	Songs  []navidrome.Song
	Audio  []byte
	Down   atomic.Bool // every request → 500
	Scans  atomic.Int32
}

// NewNavidrome starts the fake server with the given songs.
func NewNavidrome(songs []navidrome.Song) *Navidrome {
	n := &Navidrome{Songs: songs, Audio: bytes.Repeat([]byte("0123456789"), 1000)}
	n.Server = httptest.NewServer(http.HandlerFunc(n.serve))
	return n
}

func (n *Navidrome) ok(w http.ResponseWriter, extra map[string]any) {
	body := map[string]any{"status": "ok", "version": "1.16.1"}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": body})
}

func (n *Navidrome) serve(w http.ResponseWriter, r *http.Request) {
	if n.Down.Load() {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	if r.URL.Path == "/auth/createAdmin" {
		if n.Admins.Add(1) > 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"id":"1"}`))
		return
	}
	q := r.URL.Query()
	if q.Get("u") == "" || q.Get("t") == "" || q.Get("s") == "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed","error":{"code":40,"message":"Wrong username or password"}}}`))
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/rest/") {
	case "ping":
		n.ok(w, nil)
	case "startScan":
		n.Scans.Add(1)
		n.ok(w, map[string]any{"scanStatus": map[string]any{"scanning": true}})
	case "search3":
		var off, cnt int
		_, _ = fmt.Sscan(q.Get("songOffset"), &off)
		_, _ = fmt.Sscan(q.Get("songCount"), &cnt)
		songs := []navidrome.Song{}
		for i := off; i < len(n.Songs) && i < off+cnt; i++ {
			songs = append(songs, n.Songs[i])
		}
		n.ok(w, map[string]any{"searchResult3": map[string]any{"song": songs}})
	case "stream":
		for _, s := range n.Songs {
			if s.ID == q.Get("id") {
				w.Header().Set("Content-Type", "audio/mpeg")
				http.ServeContent(w, r, "a.mp3", time.Unix(1700000000, 0), bytes.NewReader(n.Audio))
				return
			}
		}
		n.notFound(w)
	case "getCoverArt":
		if q.Get("id") == "missing" {
			n.notFound(w)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff jpeg"))
	default:
		http.NotFound(w, r)
	}
}

func (n *Navidrome) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed","error":{"code":70,"message":"not found"}}}`))
}

// ErrBoom is a generic injected failure.
var ErrBoom = errors.New("boom")

// EventCount returns the number of stored play events (race-safe).
func (m *Mem) EventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Events)
}

// SetErr sets the injected failure (race-safe).
func (m *Mem) SetErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Err = err
}

// ExportCatalog pages active tracks in id order (reco-service export).
func (m *Mem) ExportCatalog(_ context.Context, after *uuid.UUID, limit int) ([]repo.ExportTrack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	ts := m.active()
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID.String() < ts[j].ID.String() })
	var out []repo.ExportTrack
	for _, t := range ts {
		if after != nil && t.ID.String() <= after.String() {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, repo.ExportTrack{
			ID: t.ID, Title: t.Title, ArtistID: t.ArtistID, Artist: t.Artist, Album: t.Album,
			Genre: "Jazz", DurationSec: t.DurationSec, Popularity: t.Popularity, CreatedAt: time.Unix(0, 0).UTC(),
		})
	}
	return out, nil
}

// ExportInteractions emits likes and play events.
func (m *Mem) ExportInteractions(_ context.Context, since time.Time, emit func(repo.Interaction) error) error {
	m.mu.Lock()
	if m.Err != nil {
		m.mu.Unlock()
		return m.Err
	}
	var out []repo.Interaction
	for k, at := range m.likes {
		out = append(out, repo.Interaction{Kind: "like", UserID: k.u, TrackID: k.t, At: at})
	}
	for _, e := range m.Events {
		if !e.PlayedAt.Before(since) {
			out = append(out, repo.Interaction{
				Kind: "play", UserID: e.UserID, TrackID: e.TrackID, At: e.PlayedAt,
				PositionSec: float64(e.PositionSec), Completed: e.Completed, EventID: e.EventID,
			})
		}
	}
	m.mu.Unlock()
	for _, it := range out {
		if err := emit(it); err != nil {
			return err
		}
	}
	return nil
}
