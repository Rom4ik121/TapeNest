// Package service holds music-service use cases: catalog lists and search,
// library (likes, playlists, positions), "My wave", listen events and streaming.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/mq"
	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

// Store is the persistence port (implemented by repo.Store; faked in tests).
type Store interface {
	Popular(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error)
	Search(ctx context.Context, user uuid.UUID, q, pattern string, cur *domain.Cursor, limit int) (domain.Page, error)
	Liked(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error)
	Recent(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error)
	TracksByIDs(ctx context.Context, user uuid.UUID, ids []uuid.UUID) ([]domain.Track, error)
	TrackForStream(ctx context.Context, id uuid.UUID) (repo.StreamTarget, error)
	TrackActive(ctx context.Context, id uuid.UUID) (bool, error)
	Like(ctx context.Context, user, track uuid.UUID) error
	Unlike(ctx context.Context, user, track uuid.UUID) error
	SavePosition(ctx context.Context, user, track uuid.UUID, pos float64, at time.Time) error
	Position(ctx context.Context, user, track uuid.UUID) (domain.Position, error)
	Playlists(ctx context.Context, user uuid.UUID) ([]domain.Playlist, error)
	CountPlaylists(ctx context.Context, user uuid.UUID) (int, error)
	CreatePlaylist(ctx context.Context, user uuid.UUID, title string) (domain.Playlist, error)
	Playlist(ctx context.Context, user, id uuid.UUID) (domain.Playlist, error)
	RenamePlaylist(ctx context.Context, user, id uuid.UUID, title string) error
	DeletePlaylist(ctx context.Context, user, id uuid.UUID) error
	AddPlaylistTrack(ctx context.Context, user, playlist, track uuid.UUID) error
	RemovePlaylistTrack(ctx context.Context, user, playlist, track uuid.UUID) error
	PlaylistTracks(ctx context.Context, user, playlist uuid.UUID) ([]domain.Track, error)
	WaveCandidates(ctx context.Context, user uuid.UUID, limit int) ([]repo.WaveCandidate, error)
}

// MaxPlaylists per user (abuse guard; generous for real use).
const MaxPlaylists = 200

// Library is catalog + user library use cases.
type Library struct {
	store   Store
	now     func() time.Time
	publish func(ctx context.Context, e mq.UserEvent)
}

// NewLibrary creates the library service.
func NewLibrary(store Store) *Library {
	return &Library{store: store, now: time.Now, publish: func(context.Context, mq.UserEvent) {}}
}

// WithEvents publishes likes / playlist changes as user events (reco-service).
func (l *Library) WithEvents(pub func(ctx context.Context, e mq.UserEvent)) *Library {
	if pub != nil {
		l.publish = pub
	}
	return l
}

// ListKind is one of the contract's list endpoints.
type ListKind string

// List kinds (/tracks/{kind}).
const (
	ListRecent  ListKind = "recent"
	ListPopular ListKind = "popular"
	ListLiked   ListKind = "liked"
)

// List returns a cursor page of the given kind.
func (l *Library) List(ctx context.Context, user uuid.UUID, kind ListKind, cursor string, limit int) (domain.Page, error) {
	cur, err := domain.DecodeCursor(cursor)
	if err != nil {
		return domain.Page{}, err
	}
	limit = domain.Limit(limit)
	switch kind {
	case ListPopular:
		if cur != nil && cur.Score == nil {
			return domain.Page{}, domain.Invalid("invalid cursor")
		}
		return l.store.Popular(ctx, user, cur, limit)
	case ListLiked:
		if cur != nil && cur.At == nil {
			return domain.Page{}, domain.Invalid("invalid cursor")
		}
		return l.store.Liked(ctx, user, cur, limit)
	case ListRecent:
		if cur != nil && cur.At == nil {
			return domain.Page{}, domain.Invalid("invalid cursor")
		}
		return l.store.Recent(ctx, user, cur, limit)
	}
	return domain.Page{}, domain.ErrNotFound
}

// Search runs the fuzzy search.
func (l *Library) Search(ctx context.Context, user uuid.UUID, q, cursor string, limit int) (domain.Page, error) {
	query, pattern, err := domain.SearchQuery(q)
	if err != nil {
		return domain.Page{}, err
	}
	cur, err := domain.DecodeCursor(cursor)
	if err != nil {
		return domain.Page{}, err
	}
	if cur != nil && cur.Score == nil {
		return domain.Page{}, domain.Invalid("invalid cursor")
	}
	return l.store.Search(ctx, user, query, pattern, cur, domain.Limit(limit))
}

// Like / Unlike are idempotent.
func (l *Library) Like(ctx context.Context, user, track uuid.UUID) error {
	if err := l.store.Like(ctx, user, track); err != nil {
		return err
	}
	l.publish(ctx, mq.UserEvent{Kind: mq.KindLike, UserID: user, TrackID: track})
	return nil
}

// Unlike removes a like (idempotent, also for unknown tracks).
func (l *Library) Unlike(ctx context.Context, user, track uuid.UUID) error {
	if err := l.store.Unlike(ctx, user, track); err != nil {
		return err
	}
	l.publish(ctx, mq.UserEvent{Kind: mq.KindUnlike, UserID: user, TrackID: track})
	return nil
}

// SavePosition validates and stores a playback position.
func (l *Library) SavePosition(ctx context.Context, user, track uuid.UUID, pos float64) error {
	if math.IsNaN(pos) || pos < 0 || pos > domain.MaxPositionSec {
		return domain.Invalid("positionSec must be between 0 and %d", domain.MaxPositionSec)
	}
	return l.store.SavePosition(ctx, user, track, pos, l.now().UTC())
}

// Position returns the saved position (ErrNotFound when none).
func (l *Library) Position(ctx context.Context, user, track uuid.UUID) (domain.Position, error) {
	return l.store.Position(ctx, user, track)
}

// Playlists lists the user's playlists.
func (l *Library) Playlists(ctx context.Context, user uuid.UUID) ([]domain.Playlist, error) {
	return l.store.Playlists(ctx, user)
}

// CreatePlaylist validates the title and creates a playlist.
func (l *Library) CreatePlaylist(ctx context.Context, user uuid.UUID, title string) (domain.Playlist, error) {
	t, err := domain.PlaylistTitle(title)
	if err != nil {
		return domain.Playlist{}, err
	}
	n, err := l.store.CountPlaylists(ctx, user)
	if err != nil {
		return domain.Playlist{}, err
	}
	if n >= MaxPlaylists {
		return domain.Playlist{}, domain.Invalid("playlist limit reached (%d)", MaxPlaylists)
	}
	return l.store.CreatePlaylist(ctx, user, t)
}

// Playlist returns a playlist with its tracks.
func (l *Library) Playlist(ctx context.Context, user, id uuid.UUID) (domain.Playlist, []domain.Track, error) {
	p, err := l.store.Playlist(ctx, user, id)
	if err != nil {
		return domain.Playlist{}, nil, err
	}
	tracks, err := l.store.PlaylistTracks(ctx, user, id)
	if err != nil {
		return domain.Playlist{}, nil, err
	}
	p.TrackCount = len(tracks)
	return p, tracks, nil
}

// RenamePlaylist validates and renames.
func (l *Library) RenamePlaylist(ctx context.Context, user, id uuid.UUID, title string) (domain.Playlist, error) {
	t, err := domain.PlaylistTitle(title)
	if err != nil {
		return domain.Playlist{}, err
	}
	if err := l.store.RenamePlaylist(ctx, user, id, t); err != nil {
		return domain.Playlist{}, err
	}
	return l.store.Playlist(ctx, user, id)
}

// DeletePlaylist deletes a playlist.
func (l *Library) DeletePlaylist(ctx context.Context, user, id uuid.UUID) error {
	return l.store.DeletePlaylist(ctx, user, id)
}

// AddToPlaylist appends a track (idempotent).
func (l *Library) AddToPlaylist(ctx context.Context, user, playlist, track uuid.UUID) error {
	if err := l.store.AddPlaylistTrack(ctx, user, playlist, track); err != nil {
		return err
	}
	l.publish(ctx, mq.UserEvent{Kind: mq.KindPlaylistAdd, UserID: user, TrackID: track})
	return nil
}

// RemoveFromPlaylist removes a track (idempotent).
func (l *Library) RemoveFromPlaylist(ctx context.Context, user, playlist, track uuid.UUID) error {
	if err := l.store.RemovePlaylistTrack(ctx, user, playlist, track); err != nil {
		return err
	}
	l.publish(ctx, mq.UserEvent{Kind: mq.KindPlaylistRemove, UserID: user, TrackID: track})
	return nil
}

// Media is the streaming port (implemented by navidrome.Client).
type Media interface {
	Healthy() bool
	Stream(ctx context.Context, id string, h http.Header) (*http.Response, error)
	CoverArt(ctx context.Context, id string, size int) (*http.Response, error)
}

// Streamer issues signed stream URLs and proxies audio/covers from Navidrome.
type Streamer struct {
	store   Store
	media   Media
	ytAudio YouTubeAudio
	caaBase string
	signer  *signer.Signer
	ttl     time.Duration
	log     *slog.Logger
	now     func() time.Time
}

// NewStreamer creates the streaming service.
func NewStreamer(store Store, media Media, s *signer.Signer, ttl time.Duration, log *slog.Logger) *Streamer {
	return &Streamer{store: store, media: media, signer: s, ttl: ttl, log: log, now: time.Now}
}

// YouTubeAudio proxies a YouTube Music track without saving it (ADR 0012).
type YouTubeAudio interface {
	Proxy(w http.ResponseWriter, r *http.Request, videoID string) error
}

// WithYouTubeAudio streams YouTube Music tracks through yt-dlp.
func (s *Streamer) WithYouTubeAudio(a YouTubeAudio) *Streamer {
	s.ytAudio = a
	return s
}

// StreamURL returns a signed URL (TTL ≤ 1 h). ErrStreamingUnavailable when
// Navidrome or YouTube audio is down, so the player can say so instead of
// failing mid-play. A row with neither a library file nor a YouTube id is
// not playable (domain.ErrNoSources).
func (s *Streamer) StreamURL(ctx context.Context, user, track uuid.UUID) (string, time.Time, error) {
	t, err := s.store.TrackForStream(ctx, track)
	if err != nil {
		return "", time.Time{}, err
	}
	if t.NavidromeID == "" && t.YouTubeID != "" {
		if s.ytAudio == nil {
			return "", time.Time{}, domain.ErrStreamingUnavailable
		}
	} else if t.NavidromeID == "" {
		return "", time.Time{}, domain.ErrNoSources
	} else if !s.media.Healthy() {
		return "", time.Time{}, domain.ErrStreamingUnavailable
	}
	exp := s.now().Add(s.ttl).UTC().Truncate(time.Second)
	return s.signer.StreamURL(track, user, exp), exp, nil
}

// CoverURL is the signed cover URL for a track list item ("" when none).
func (s *Streamer) CoverURL(coverID string) string { return s.signer.CoverURL(coverID, 300) }

// Signer exposes signature checks to the transport.
func (s *Streamer) Signer() *signer.Signer { return s.signer }

// copied response headers for audio/cover proxying
var passResponseHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"}

// ServeStream proxies the original file with Range support (io.Copy, spec §5.4).
func (s *Streamer) ServeStream(w http.ResponseWriter, r *http.Request, track uuid.UUID) error {
	t, err := s.store.TrackForStream(r.Context(), track)
	if err != nil {
		return err
	}
	if t.NavidromeID == "" && t.YouTubeID != "" {
		if s.ytAudio == nil {
			return domain.ErrStreamingUnavailable
		}
		if err := s.ytAudio.Proxy(w, r, t.YouTubeID); err != nil {
			return fmt.Errorf("%w: %s", domain.ErrStreamingUnavailable, "youtube")
		}
		return nil
	}
	if t.NavidromeID == "" {
		return domain.ErrNotFound
	}
	resp, err := s.media.Stream(r.Context(), t.NavidromeID, r.Header)
	if err != nil {
		return mediaErr(err)
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "private, max-age=3600")
	return s.copy(w, r, resp)
}

// WithCoverArchive sets the Cover Art Archive base for remote album covers.
func (s *Streamer) WithCoverArchive(base string) *Streamer {
	s.caaBase = strings.TrimRight(base, "/")
	return s
}

// caaCover fetches a release-group front cover from the Cover Art Archive.
func (s *Streamer) caaCover(ctx context.Context, coverID string, size int) (*http.Response, error) {
	rg, err := uuid.Parse(strings.TrimPrefix(coverID, repo.CoverPrefixCAA))
	if err != nil || s.caaBase == "" {
		return nil, domain.ErrNotFound
	}
	px := 250
	if size > 250 {
		px = 500
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/release-group/%s/front-%d", s.caaBase, rg, px), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TapeNest/1.0")
	resp, err := caaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: cover archive", domain.ErrStreamingUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, domain.ErrNotFound
	}
	return resp, nil
}

var caaClient = &http.Client{Timeout: 15 * time.Second}

// transparentPNG is a 1×1 fully transparent PNG.
var transparentPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0b, 0x49, 0x44, 0x41, 0x54, 0x78, 0xda, 0x63, 0x60, 0x00, 0x02, 0x00,
	0x00, 0x05, 0x00, 0x01, 0xe9, 0xfa, 0xdc, 0xd8, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
	0xae, 0x42, 0x60, 0x82,
}

// ServeCover proxies cover art (public, immutable).
func (s *Streamer) ServeCover(w http.ResponseWriter, r *http.Request, coverID string, size int) error {
	if u, ok := ytmCover(coverID); ok {
		resp, err := s.fetchCover(r.Context(), u)
		if err != nil {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_, werr := w.Write(transparentPNG)
			return werr
		}
		defer resp.Body.Close()
		w.Header().Set("Cache-Control", "public, max-age=86400")
		return s.copy(w, r, resp)
	}
	if strings.HasPrefix(coverID, repo.CoverPrefixCAA) {
		resp, err := s.caaCover(r.Context(), coverID, size)
		if errors.Is(err, domain.ErrNotFound) {
			// many release groups have no art: a transparent pixel lets the
			// client's gradient placeholder show (no broken image, no 404 noise)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			_, werr := w.Write(transparentPNG)
			return werr
		}
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		w.Header().Set("Cache-Control", "public, max-age=604800")
		return s.copy(w, r, resp)
	}
	resp, err := s.media.CoverArt(r.Context(), coverID, size)
	if err != nil {
		return mediaErr(err)
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return s.copy(w, r, resp)
}

func (s *Streamer) copy(w http.ResponseWriter, r *http.Request, resp *http.Response) error {
	for _, k := range passResponseHeaders {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return nil
	}
	if _, err := io.Copy(w, resp.Body); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Debug("stream copy aborted", "err", err) // client went away mid-stream
	}
	return nil
}

func mediaErr(err error) error {
	switch {
	case errors.Is(err, navidrome.ErrNotFound):
		return domain.ErrNotFound
	case errors.Is(err, navidrome.ErrUnavailable):
		return fmt.Errorf("%w: %w", domain.ErrStreamingUnavailable, err)
	}
	return err
}

func ytmCover(id string) (string, bool) { return ytm.CoverURL(id) }

func (s *Streamer) fetchCover(ctx context.Context, raw string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TapeNest/1.0")
	resp, err := caaClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, domain.ErrNotFound
	}
	return resp, nil
}
