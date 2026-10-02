// Package core holds the acquisition use cases: unified metadata search, the
// invisible "acquire on play/like" entry point, and the background worker.
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/mb"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/streamer"
)

// Request states.
const (
	StateQueued      = "queued"
	StateSearching   = "searching"
	StateDownloading = "downloading"
	StateImporting   = "importing"
	StateAvailable   = "available"
	StateFailed      = "failed"
)

// Error codes surfaced to clients (small toast in the player).
const (
	CodeNoSources   = "NO_SOURCES"
	CodeNoIndexers  = "NO_INDEXERS"
	CodeStalled     = "STALLED"
	CodeRemoved     = "TORRENT_REMOVED"
	CodeImport      = "IMPORT_FAILED"
	CodeTrackAbsent = "TRACK_NOT_IN_RELEASE"
)

// Acquire reasons and their priorities (play beats like beats playlist).
var reasonPriority = map[string]int32{"play": 10, "like": 5, "playlist": 3}

// Errors.
var (
	ErrDisabled     = errors.New("acquisition disabled (CONTENT_SOURCES=licensed)")
	ErrQuota        = errors.New("acquisition quota exceeded")
	ErrStorageFull  = errors.New("library storage limit reached")
	ErrUnknownAlbum = errors.New("release group not found")
	ErrInvalid      = errors.New("invalid request")
)

// Store is the repository subset used by the API side.
type Store interface {
	RequestByReleaseGroup(ctx context.Context, rg uuid.UUID) (db.AcquisitionRequest, error)
	RequestByID(ctx context.Context, id uuid.UUID) (db.AcquisitionRequest, error)
	CreateRequest(ctx context.Context, n repo.NewRequest) (bool, error)
	AddRequestUser(ctx context.Context, arg db.AddRequestUserParams) (int64, error)
	BumpPriority(ctx context.Context, arg db.BumpPriorityParams) error
	MarkWanted(ctx context.Context, arg db.MarkWantedParams) error
	RequeueFailed(ctx context.Context, id uuid.UUID) (int64, error)
	CountUserRequestsSince(ctx context.Context, arg db.CountUserRequestsSinceParams) (int64, error)
	CountUserActive(ctx context.Context, userID uuid.UUID) (int64, error)
	TrackByRecording(ctx context.Context, rec uuid.UUID) (db.TrackByRecordingRow, error)
	InsertRequestTrack(ctx context.Context, arg db.InsertRequestTrackParams) error
	Event(ctx context.Context, id uuid.UUID, kind string, user *uuid.UUID, detail map[string]any) error
}

// Metadata is the MusicBrainz subset.
type Metadata interface {
	SearchArtists(ctx context.Context, q string, limit int) ([]mb.Artist, error)
	SearchReleaseGroups(ctx context.Context, q string, limit int) ([]mb.ReleaseGroup, error)
	SearchRecordings(ctx context.Context, q string, limit int) ([]mb.Recording, error)
	Album(ctx context.Context, rg uuid.UUID) (mb.Album, error)
	Discography(ctx context.Context, artist uuid.UUID, limit int) (mb.Artist, []mb.ReleaseGroup, error)
}

// Notifier wakes the worker and exposes worker-computed disk stats.
type Notifier interface {
	Wake(ctx context.Context)
	LibraryBytes(ctx context.Context) int64
}

// Limits for the API side.
type Limits struct {
	UserDaily        int
	UserActive       int
	RetryAfter       time.Duration
	StreamStartBytes int64
	LibraryMaxBytes  int64
}

// Service implements the API use cases.
type Service struct {
	Enabled bool
	Store   Store
	Meta    Metadata
	Pieces  streamer.Source
	Notify  Notifier
	Limits  Limits
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ── search ───────────────────────────────────────────────────────────────────

// SearchResult is the external half of the unified search.
type SearchResult struct {
	Artists    []mb.Artist       `json:"artists"`
	Albums     []mb.ReleaseGroup `json:"albums"`
	Recordings []mb.Recording    `json:"recordings"`
	Partial    bool              `json:"partial"`
}

// Search queries artists, release groups and recordings in parallel; a
// failing facet degrades to a partial result, all three failing is an error.
func (s *Service) Search(ctx context.Context, q string, limit int) (SearchResult, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return SearchResult{Artists: []mb.Artist{}, Albums: []mb.ReleaseGroup{}, Recordings: []mb.Recording{}}, nil
	}
	limit = min(max(limit, 1), 25)
	var (
		wg   sync.WaitGroup
		res  SearchResult
		errs [3]error
	)
	wg.Add(3)
	go func() { defer wg.Done(); res.Artists, errs[0] = s.Meta.SearchArtists(ctx, q, min(limit, 8)) }()
	go func() { defer wg.Done(); res.Albums, errs[1] = s.Meta.SearchReleaseGroups(ctx, q, limit) }()
	go func() { defer wg.Done(); res.Recordings, errs[2] = s.Meta.SearchRecordings(ctx, q, limit) }()
	wg.Wait()
	if errs[0] != nil && errs[1] != nil && errs[2] != nil {
		return SearchResult{}, errors.Join(errs[:]...)
	}
	res.Partial = errs[0] != nil || errs[1] != nil || errs[2] != nil
	if res.Artists == nil {
		res.Artists = []mb.Artist{}
	}
	if res.Albums == nil {
		res.Albums = []mb.ReleaseGroup{}
	}
	if res.Recordings == nil {
		res.Recordings = []mb.Recording{}
	}
	return res, nil
}

// Album returns the release group tracklist.
func (s *Service) Album(ctx context.Context, rg uuid.UUID) (mb.Album, error) {
	a, err := s.Meta.Album(ctx, rg)
	if errors.Is(err, mb.ErrNotFound) {
		return mb.Album{}, ErrUnknownAlbum
	}
	return a, err
}

// ArtistView is an artist with its discography.
type ArtistView struct {
	Artist mb.Artist         `json:"artist"`
	Albums []mb.ReleaseGroup `json:"albums"`
}

// Artist returns an artist and its albums/EPs/singles.
func (s *Service) Artist(ctx context.Context, id uuid.UUID) (ArtistView, error) {
	a, rgs, err := s.Meta.Discography(ctx, id, 50)
	if errors.Is(err, mb.ErrNotFound) {
		return ArtistView{}, ErrUnknownAlbum
	}
	if rgs == nil {
		rgs = []mb.ReleaseGroup{}
	}
	return ArtistView{Artist: a, Albums: rgs}, err
}

// ── acquire ──────────────────────────────────────────────────────────────────

// AcquireInput is one play/like/playlist-add of a not-yet-available item.
type AcquireInput struct {
	UserID       uuid.UUID
	ReleaseGroup uuid.UUID
	Recording    uuid.UUID // optional (uuid.Nil = whole album)
	Title        string    // recording title hint when it is missing from the representative release
	Reason       string
}

// StreamStatus tells the player whether it can start.
type StreamStatus struct {
	Ready         bool  `json:"ready"`
	Imported      bool  `json:"imported"`
	BufferedBytes int64 `json:"bufferedBytes"`
	SizeBytes     int64 `json:"sizeBytes"`
}

// Status is the state of the request serving an item.
type Status struct {
	RequestID uuid.UUID     `json:"requestId"`
	State     string        `json:"state"`
	Progress  float64       `json:"progress"`
	ErrorCode string        `json:"errorCode,omitempty"`
	Stream    *StreamStatus `json:"stream,omitempty"`
}

// Acquire makes sure the album is (being) acquired and reports readiness of
// the wanted recording. Idempotent: repeated calls just poll.
func (s *Service) Acquire(ctx context.Context, in AcquireInput) (Status, error) {
	if !s.Enabled {
		return Status{}, ErrDisabled
	}
	prio, ok := reasonPriority[in.Reason]
	if !ok || in.UserID == uuid.Nil || in.ReleaseGroup == uuid.Nil {
		return Status{}, ErrInvalid
	}
	req, err := s.Store.RequestByReleaseGroup(ctx, in.ReleaseGroup)
	switch {
	case errors.Is(repo.NotFound(err), repo.ErrNotFound):
		if req, err = s.create(ctx, in, prio); err != nil {
			return Status{}, err
		}
	case err != nil:
		return Status{}, err
	default:
		if err := s.join(ctx, req, in, prio); err != nil {
			return Status{}, err
		}
		if req, err = s.Store.RequestByID(ctx, req.ID); err != nil {
			return Status{}, err
		}
	}
	return s.status(ctx, req, in.Recording)
}

func (s *Service) create(ctx context.Context, in AcquireInput, prio int32) (db.AcquisitionRequest, error) {
	since := s.now().Add(-24 * time.Hour)
	daily, err := s.Store.CountUserRequestsSince(ctx, db.CountUserRequestsSinceParams{UserID: in.UserID, Since: since})
	if err != nil {
		return db.AcquisitionRequest{}, err
	}
	active, err := s.Store.CountUserActive(ctx, in.UserID)
	if err != nil {
		return db.AcquisitionRequest{}, err
	}
	if daily >= int64(s.Limits.UserDaily) || active >= int64(s.Limits.UserActive) {
		return db.AcquisitionRequest{}, ErrQuota
	}
	if s.Limits.LibraryMaxBytes > 0 && s.Notify != nil && s.Notify.LibraryBytes(ctx) >= s.Limits.LibraryMaxBytes {
		return db.AcquisitionRequest{}, ErrStorageFull
	}
	album, err := s.Album(ctx, in.ReleaseGroup)
	if err != nil {
		return db.AcquisitionRequest{}, err
	}
	now := s.now()
	n := repo.NewRequest{UserID: in.UserID, Reason: in.Reason}
	n.Request = db.InsertRequestParams{
		ID: uuid.New(), ReleaseGroupMbid: in.ReleaseGroup, ArtistName: album.Artist, AlbumTitle: album.Title,
		Year: int32(album.Year), Priority: prio, RequestedBy: in.UserID, //nolint:gosec // year fits int32
	}
	if album.ReleaseMBID != uuid.Nil {
		rel := album.ReleaseMBID
		n.Request.ReleaseMbid = &rel
	}
	if album.ArtistMBID != uuid.Nil {
		am := album.ArtistMBID
		n.Request.ArtistMbid = &am
	}
	seen := map[uuid.UUID]bool{}
	for _, t := range album.Tracks {
		if seen[t.RecordingMBID] {
			continue // same recording twice on a release: one file is enough
		}
		seen[t.RecordingMBID] = true
		p := db.InsertRequestTrackParams{RecordingMbid: t.RecordingMBID, Disc: int32(t.Disc), Position: int32(t.Position), Title: t.Title, LengthMs: int32(t.LengthMS)} //nolint:gosec // disc/position/length are small MusicBrainz ints
		if t.RecordingMBID == in.Recording && in.Reason == "play" {
			p.WantedAt = &now
		}
		n.Tracks = append(n.Tracks, p)
	}
	if in.Recording != uuid.Nil && !seen[in.Recording] && strings.TrimSpace(in.Title) != "" {
		p := db.InsertRequestTrackParams{RecordingMbid: in.Recording, Title: in.Title}
		if in.Reason == "play" {
			p.WantedAt = &now
		}
		n.Tracks = append(n.Tracks, p)
	}
	created, err := s.Store.CreateRequest(ctx, n)
	if err != nil {
		return db.AcquisitionRequest{}, err
	}
	if !created { // lost the race: join the winner instead
		req, err := s.Store.RequestByReleaseGroup(ctx, in.ReleaseGroup)
		if err != nil {
			return db.AcquisitionRequest{}, err
		}
		return req, s.join(ctx, req, in, prio)
	}
	s.wake(ctx)
	return s.Store.RequestByID(ctx, n.Request.ID)
}

func (s *Service) join(ctx context.Context, req db.AcquisitionRequest, in AcquireInput, prio int32) error {
	uid := in.UserID
	if req.State == StateFailed && s.now().Sub(req.UpdatedAt) >= s.Limits.RetryAfter {
		if n, err := s.Store.RequeueFailed(ctx, req.ID); err != nil {
			return err
		} else if n > 0 {
			_ = s.Store.Event(ctx, req.ID, "retry", &uid, map[string]any{"previousError": req.ErrorCode})
		}
	}
	added, err := s.Store.AddRequestUser(ctx, db.AddRequestUserParams{RequestID: req.ID, UserID: in.UserID, Reason: in.Reason})
	if err != nil {
		return err
	}
	if added > 0 {
		_ = s.Store.Event(ctx, req.ID, "joined", &uid, map[string]any{"reason": in.Reason})
	}
	if err := s.Store.BumpPriority(ctx, db.BumpPriorityParams{Priority: prio, ID: req.ID}); err != nil {
		return err
	}
	if in.Recording != uuid.Nil {
		if _, err := s.Store.TrackByRecording(ctx, in.Recording); errors.Is(repo.NotFound(err), repo.ErrNotFound) && strings.TrimSpace(in.Title) != "" {
			if err := s.Store.InsertRequestTrack(ctx, db.InsertRequestTrackParams{RequestID: req.ID, RecordingMbid: in.Recording, Title: in.Title}); err != nil {
				return err
			}
		}
		if in.Reason == "play" && req.State != StateAvailable {
			if err := s.Store.MarkWanted(ctx, db.MarkWantedParams{RequestID: req.ID, RecordingMbid: in.Recording}); err != nil {
				return err
			}
		}
	}
	s.wake(ctx)
	return nil
}

func (s *Service) wake(ctx context.Context) {
	if s.Notify != nil {
		s.Notify.Wake(ctx)
	}
}

// RecordingStatus reports readiness of one recording (no side effects).
func (s *Service) RecordingStatus(ctx context.Context, rec uuid.UUID) (Status, db.TrackByRecordingRow, error) {
	row, err := s.Store.TrackByRecording(ctx, rec)
	if err != nil {
		return Status{}, row, repo.NotFound(err)
	}
	st := Status{RequestID: row.RequestID, State: row.State, Progress: float64(row.Progress), ErrorCode: row.ErrorCode, Stream: &StreamStatus{}}
	switch {
	case row.State == StateAvailable || row.LibraryPath != "":
		st.Stream = &StreamStatus{Ready: true, Imported: true, BufferedBytes: row.FileSize, SizeBytes: row.FileSize}
	case row.TorrentHash != "" && row.FileIndex != nil && s.Pieces != nil:
		l, err := streamer.Locate(ctx, s.Pieces, row.TorrentHash, int(*row.FileIndex))
		if err != nil {
			if errors.Is(err, qbt.ErrNotFound) || errors.Is(err, streamer.ErrNotReady) {
				return st, row, nil
			}
			return Status{}, row, fmt.Errorf("locate: %w", err)
		}
		states, err := s.Pieces.PieceStates(ctx, row.TorrentHash)
		if err != nil {
			return Status{}, row, fmt.Errorf("pieces: %w", err)
		}
		avail := streamer.Available(states, l, 0)
		need := min(s.Limits.StreamStartBytes, l.Size)
		st.Stream = &StreamStatus{Ready: l.Size > 0 && avail >= need, BufferedBytes: avail, SizeBytes: l.Size}
	}
	return st, row, nil
}

func (s *Service) status(ctx context.Context, req db.AcquisitionRequest, rec uuid.UUID) (Status, error) {
	if rec != uuid.Nil {
		st, row, err := s.RecordingStatus(ctx, rec)
		if err == nil {
			return st, nil
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return Status{}, err
		}
		_ = row
		// recording absent from this release and no title hint to match on
		return Status{RequestID: req.ID, State: req.State, Progress: float64(req.Progress), ErrorCode: CodeTrackAbsent, Stream: &StreamStatus{}}, nil
	}
	return Status{RequestID: req.ID, State: req.State, Progress: float64(req.Progress), ErrorCode: req.ErrorCode}, nil
}
