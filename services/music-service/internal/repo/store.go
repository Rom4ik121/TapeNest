package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo/db"
)

// Store implements the service ports on PostgreSQL. Writes and user-specific
// reads go to the primary; catalog reads (popular, search, wave candidates) go to
// the replica when DB_REPLICA_URL is set (spec §5.4 read-write splitting).
type Store struct {
	w *db.Queries
	r *db.Queries
}

// NewStore wires the pools; replica may be nil (primary only).
func NewStore(primary, replica db.DBTX) *Store {
	s := &Store{w: db.New(primary), r: db.New(primary)}
	if replica != nil {
		s.r = db.New(replica)
	}
	return s
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func track(id uuid.UUID, title, artist, album, cover string, dur int32, liked bool) domain.Track {
	return domain.Track{ID: id, Title: title, Artist: artist, Album: album, CoverArtID: cover, DurationSec: int(dur), Liked: liked}
}

func withRemote(t domain.Track, remote bool) domain.Track {
	t.Remote = remote
	return t
}

func ptr[T any](v T) *T { return &v }

func pageOf[T any](rows []T, limit int, conv func(T) domain.Track, cur func(T) domain.Cursor) domain.Page {
	p := domain.Page{Items: make([]domain.Track, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			c := cur(rows[limit-1]).Encode()
			p.Next = &c
			break
		}
		p.Items = append(p.Items, conv(r))
	}
	return p
}

func scoreCursor(c *domain.Cursor) (*float64, *uuid.UUID) {
	if c == nil || c.Score == nil {
		return nil, nil
	}
	id := c.ID
	return c.Score, &id
}

func timeCursor(c *domain.Cursor) (*time.Time, *uuid.UUID) {
	if c == nil || c.At == nil {
		return nil, nil
	}
	id := c.ID
	return c.At, &id
}

// Popular lists tracks by popularity (track_popularity), then id.
func (s *Store) Popular(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	sc, id := scoreCursor(cur)
	rows, err := s.r.ListPopular(ctx, db.ListPopularParams{UserID: user, CursorScore: sc, CursorID: id, Lim: int32(limit + 1)}) //nolint:gosec // limit ≤ 100
	if err != nil {
		return domain.Page{}, fmt.Errorf("popular: %w", err)
	}
	return pageOf(rows, limit, func(r db.ListPopularRow) domain.Track {
		return track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked)
	}, func(r db.ListPopularRow) domain.Cursor { v := r.Score; return domain.Cursor{Score: &v, ID: r.ID} }), nil
}

// Search is fuzzy search over title/artist/album (pg_trgm).
func (s *Store) Search(ctx context.Context, user uuid.UUID, q, pattern string, cur *domain.Cursor, limit int) (domain.Page, error) {
	sc, id := scoreCursor(cur)
	rows, err := s.r.SearchTracks(ctx, db.SearchTracksParams{Q: q, Pattern: pattern, UserID: user, CursorScore: sc, CursorID: id, Lim: int32(limit + 1)}) //nolint:gosec // limit ≤ 100
	if err != nil {
		return domain.Page{}, fmt.Errorf("search: %w", err)
	}
	return pageOf(rows, limit, func(r db.SearchTracksRow) domain.Track {
		return withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote)
	}, func(r db.SearchTracksRow) domain.Cursor { v := r.Score; return domain.Cursor{Score: &v, ID: r.ID} }), nil
}

// Liked lists the user's likes, newest first.
func (s *Store) Liked(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	at, id := timeCursor(cur)
	rows, err := s.w.ListLiked(ctx, db.ListLikedParams{UserID: user, CursorAt: at, CursorID: id, Lim: int32(limit + 1)}) //nolint:gosec // limit ≤ 100
	if err != nil {
		return domain.Page{}, fmt.Errorf("liked: %w", err)
	}
	return pageOf(rows, limit, func(r db.ListLikedRow) domain.Track {
		return withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote)
	}, func(r db.ListLikedRow) domain.Cursor { v := r.SortAt; return domain.Cursor{At: &v, ID: r.ID} }), nil
}

// Recent lists the user's recently played tracks.
func (s *Store) Recent(ctx context.Context, user uuid.UUID, cur *domain.Cursor, limit int) (domain.Page, error) {
	at, id := timeCursor(cur)
	rows, err := s.w.ListRecent(ctx, db.ListRecentParams{UserID: user, CursorAt: at, CursorID: id, Lim: int32(limit + 1)}) //nolint:gosec // limit ≤ 100
	if err != nil {
		return domain.Page{}, fmt.Errorf("recent: %w", err)
	}
	return pageOf(rows, limit, func(r db.ListRecentRow) domain.Track {
		return withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote)
	}, func(r db.ListRecentRow) domain.Cursor { v := r.SortAt; return domain.Cursor{At: &v, ID: r.ID} }), nil
}

// TracksByIDs returns tracks in the given order (missing ones are skipped).
func (s *Store) TracksByIDs(ctx context.Context, user uuid.UUID, ids []uuid.UUID) ([]domain.Track, error) {
	rows, err := s.w.TracksByIDs(ctx, db.TracksByIDsParams{UserID: user, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("tracks by ids: %w", err)
	}
	byID := make(map[uuid.UUID]domain.Track, len(rows))
	for _, r := range rows {
		t := track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked)
		t.Remote = r.Remote
		t.ArtistID = r.ArtistID
		byID[r.ID] = t
	}
	out := make([]domain.Track, 0, len(ids))
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// StreamTarget is what the stream proxy needs. NavidromeID == "" means the
// track is remote (acquired on demand, ADR 0011).
type StreamTarget struct {
	NavidromeID  string
	ContentType  string
	Title        string
	Recording    uuid.UUID
	ReleaseGroup uuid.UUID
	YouTubeID    string // set when the track is a YouTube Music stream (ADR 0012)
}

// TrackForStream resolves an active track to its Navidrome id.
func (s *Store) TrackForStream(ctx context.Context, id uuid.UUID) (StreamTarget, error) {
	r, err := s.r.TrackForStream(ctx, id)
	if err != nil {
		return StreamTarget{}, notFound(err)
	}
	t := StreamTarget{ContentType: r.ContentType, Title: r.Title}
	if r.NavidromeID != nil {
		t.NavidromeID = *r.NavidromeID
	}
	if r.MbRecordingID != nil {
		t.Recording = *r.MbRecordingID
	}
	if r.MbReleaseGroupID != nil {
		t.ReleaseGroup = *r.MbReleaseGroupID
	}
	t.YouTubeID = r.YoutubeVideoID
	return t, nil
}

// TrackActive reports whether the track exists and is not deleted.
func (s *Store) TrackActive(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.w.TrackActive(ctx, id)
}

// Like is idempotent; ErrNotFound for unknown tracks.
func (s *Store) Like(ctx context.Context, user, trackID uuid.UUID) error {
	n, err := s.w.LikeTrack(ctx, db.LikeTrackParams{UserID: user, TrackID: trackID})
	if err != nil {
		return fmt.Errorf("like: %w", err)
	}
	if n == 0 {
		ok, err := s.w.TrackActive(ctx, trackID)
		if err != nil {
			return fmt.Errorf("like: %w", err)
		}
		if !ok {
			return domain.ErrNotFound
		}
	}
	return nil
}

// Unlike is idempotent.
func (s *Store) Unlike(ctx context.Context, user, trackID uuid.UUID) error {
	return s.w.UnlikeTrack(ctx, db.UnlikeTrackParams{UserID: user, TrackID: trackID})
}

// SavePosition upserts the position and marks the track as recently played.
func (s *Store) SavePosition(ctx context.Context, user, trackID uuid.UUID, pos float64, at time.Time) error {
	n, err := s.w.UpsertPosition(ctx, db.UpsertPositionParams{UserID: user, TrackID: trackID, PositionSec: pos, At: at})
	if err != nil {
		return fmt.Errorf("save position: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	if err := s.w.TouchRecent(ctx, db.TouchRecentParams{UserID: user, TrackID: trackID, PlayedAt: at}); err != nil {
		return fmt.Errorf("touch recent: %w", err)
	}
	return nil
}

// Position returns the saved position or ErrNotFound.
func (s *Store) Position(ctx context.Context, user, trackID uuid.UUID) (domain.Position, error) {
	r, err := s.w.GetPosition(ctx, db.GetPositionParams{UserID: user, TrackID: trackID})
	if err != nil {
		return domain.Position{}, notFound(err)
	}
	return domain.Position{TrackID: r.TrackID, PositionSec: r.PositionSec, UpdatedAt: r.UpdatedAt}, nil
}

// Playlists lists the user's playlists, newest first.
func (s *Store) Playlists(ctx context.Context, user uuid.UUID) ([]domain.Playlist, error) {
	rows, err := s.w.ListPlaylists(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("playlists: %w", err)
	}
	out := make([]domain.Playlist, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Playlist{ID: r.ID, Title: r.Title, TrackCount: int(r.TrackCount), CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// CountPlaylists returns how many playlists the user has (limit check).
func (s *Store) CountPlaylists(ctx context.Context, user uuid.UUID) (int, error) {
	n, err := s.w.CountPlaylists(ctx, user)
	return int(n), err
}

// CreatePlaylist inserts a playlist.
func (s *Store) CreatePlaylist(ctx context.Context, user uuid.UUID, title string) (domain.Playlist, error) {
	r, err := s.w.CreatePlaylist(ctx, db.CreatePlaylistParams{UserID: user, ID: uuid.New(), Title: title})
	if err != nil {
		return domain.Playlist{}, fmt.Errorf("create playlist: %w", err)
	}
	return domain.Playlist{ID: r.ID, Title: r.Title, CreatedAt: r.CreatedAt}, nil
}

// Playlist returns one of the user's playlists.
func (s *Store) Playlist(ctx context.Context, user, id uuid.UUID) (domain.Playlist, error) {
	r, err := s.w.GetPlaylist(ctx, db.GetPlaylistParams{UserID: user, ID: id})
	if err != nil {
		return domain.Playlist{}, notFound(err)
	}
	return domain.Playlist{ID: r.ID, Title: r.Title, TrackCount: int(r.TrackCount), CreatedAt: r.CreatedAt}, nil
}

// RenamePlaylist renames; ErrNotFound when it is not the user's.
func (s *Store) RenamePlaylist(ctx context.Context, user, id uuid.UUID, title string) error {
	n, err := s.w.RenamePlaylist(ctx, db.RenamePlaylistParams{UserID: user, ID: id, Title: title})
	if err != nil {
		return fmt.Errorf("rename playlist: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeletePlaylist deletes; ErrNotFound when it is not the user's.
func (s *Store) DeletePlaylist(ctx context.Context, user, id uuid.UUID) error {
	n, err := s.w.DeletePlaylist(ctx, db.DeletePlaylistParams{UserID: user, ID: id})
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AddPlaylistTrack appends a track (idempotent). ErrNotFound: playlist or track missing.
func (s *Store) AddPlaylistTrack(ctx context.Context, user, playlist, trackID uuid.UUID) error {
	n, err := s.w.AddPlaylistTrack(ctx, db.AddPlaylistTrackParams{UserID: user, PlaylistID: playlist, TrackID: trackID})
	if err != nil {
		return fmt.Errorf("add playlist track: %w", err)
	}
	if n == 1 {
		return nil
	}
	// 0 rows: already there (fine) or playlist/track missing (404)
	if _, err := s.Playlist(ctx, user, playlist); err != nil {
		return err
	}
	exists, err := s.w.PlaylistTrackExists(ctx, db.PlaylistTrackExistsParams{PlaylistID: playlist, TrackID: trackID})
	if err != nil {
		return fmt.Errorf("add playlist track: %w", err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

// RemovePlaylistTrack removes a track (idempotent); ErrNotFound for foreign playlists.
func (s *Store) RemovePlaylistTrack(ctx context.Context, user, playlist, trackID uuid.UUID) error {
	if _, err := s.Playlist(ctx, user, playlist); err != nil {
		return err
	}
	return s.w.RemovePlaylistTrack(ctx, db.RemovePlaylistTrackParams{UserID: user, PlaylistID: playlist, TrackID: trackID})
}

// PlaylistTracks lists a playlist's tracks in order.
func (s *Store) PlaylistTracks(ctx context.Context, user, playlist uuid.UUID) ([]domain.Track, error) {
	rows, err := s.w.PlaylistTracks(ctx, db.PlaylistTracksParams{UserID: user, PlaylistID: playlist})
	if err != nil {
		return nil, fmt.Errorf("playlist tracks: %w", err)
	}
	out := make([]domain.Track, 0, len(rows))
	for _, r := range rows {
		out = append(out, withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote))
	}
	return out, nil
}

// WaveCandidate is one scored candidate for "My wave".
type WaveCandidate struct {
	ID          uuid.UUID
	ArtistID    uuid.UUID
	Popularity  float64
	Liked       bool
	LikedArtist bool
	LastPlayed  *time.Time
}

// WaveCandidates returns up to limit candidates with the user's signals.
func (s *Store) WaveCandidates(ctx context.Context, user uuid.UUID, limit int) ([]WaveCandidate, error) {
	rows, err := s.r.WaveCandidates(ctx, db.WaveCandidatesParams{UserID: user, Lim: int32(limit)}) //nolint:gosec // bounded by config
	if err != nil {
		return nil, fmt.Errorf("wave candidates: %w", err)
	}
	out := make([]WaveCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, WaveCandidate{ID: r.ID, ArtistID: r.ArtistID, Popularity: r.Popularity, Liked: r.Liked, LikedArtist: r.LikedArtist, LastPlayed: r.LastPlayed})
	}
	return out, nil
}

// PlayEvent is one listened event for the batcher.
type PlayEvent struct {
	EventID     string
	UserID      uuid.UUID
	TrackID     uuid.UUID
	PlayedAt    time.Time
	PositionSec float32
	Completed   bool
}

// InsertPlayEvents writes a batch (idempotent by event id) and updates recent_plays.
func (s *Store) InsertPlayEvents(ctx context.Context, evs []PlayEvent) (int64, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	p := db.InsertPlayEventsParams{}
	for _, e := range evs {
		p.UserIds = append(p.UserIds, e.UserID)
		p.TrackIds = append(p.TrackIds, e.TrackID)
		p.PlayedAts = append(p.PlayedAts, e.PlayedAt)
		p.Positions = append(p.Positions, e.PositionSec)
		p.CompletedFlags = append(p.CompletedFlags, e.Completed)
		p.EventIds = append(p.EventIds, e.EventID)
	}
	n, err := s.w.InsertPlayEvents(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("insert play events: %w", err)
	}
	if err := s.w.TouchRecentBatch(ctx, db.TouchRecentBatchParams{UserIds: p.UserIds, TrackIds: p.TrackIds, PlayedAts: p.PlayedAts}); err != nil {
		return n, fmt.Errorf("touch recent batch: %w", err)
	}
	return n, nil
}

// CatalogTrack is one synced track (from Navidrome).
type CatalogTrack struct {
	NavidromeID, Title, Artist, ArtistNavID, Album, AlbumNavID, CoverArt, ContentType, Genre string
	TrackNo, Year, DurationSec, Bitrate                                                      int
	SizeBytes                                                                                int64
	MBRecording                                                                              string // MusicBrainz recording id tag (claims placeholders)
}

// SyncCache memoizes artist/album ids within one sync run.
type SyncCache struct {
	artists  map[string]uuid.UUID
	albums   map[string]uuid.UUID
	acquired map[int64]*acquiredIDs // size → MusicBrainz ids (nil: ambiguous); loaded lazily
}

type acquiredIDs struct {
	rec uuid.UUID
	rg  *uuid.UUID
}

// NewSyncCache creates an empty cache.
func NewSyncCache() *SyncCache {
	return &SyncCache{artists: map[string]uuid.UUID{}, albums: map[string]uuid.UUID{}}
}

// UpsertCatalogTrack upserts artist, album and track (UUIDs are kept across syncs).
func (s *Store) UpsertCatalogTrack(ctx context.Context, c *SyncCache, t CatalogTrack, syncedAt time.Time) error {
	if err := s.claim(ctx, t); err != nil {
		return err
	}
	rec, rg, err := s.mbidsFor(ctx, c, t)
	if err != nil {
		return err
	}
	var artistID *uuid.UUID
	if t.ArtistNavID != "" {
		id, ok := c.artists[t.ArtistNavID]
		if !ok {
			var err error
			id, err = s.w.UpsertArtist(ctx, db.UpsertArtistParams{ID: uuid.New(), NavidromeID: ptr(t.ArtistNavID), Name: t.Artist})
			if errors.Is(err, pgx.ErrNoRows) { // unchanged name: WHERE clause skipped the update
				id, err = s.w.ArtistIDByNavidrome(ctx, ptr(t.ArtistNavID))
			}
			if err != nil {
				return fmt.Errorf("upsert artist: %w", err)
			}
			c.artists[t.ArtistNavID] = id
		}
		artistID = &id
	}
	var albumID *uuid.UUID
	if t.AlbumNavID != "" {
		id, ok := c.albums[t.AlbumNavID]
		if !ok {
			var err error
			id, err = s.w.UpsertAlbum(ctx, db.UpsertAlbumParams{
				ID: uuid.New(), NavidromeID: ptr(t.AlbumNavID), ArtistID: artistID, Title: t.Album, Year: int32(t.Year), CoverArtID: t.CoverArt, //nolint:gosec // small ints
			})
			if err != nil {
				return fmt.Errorf("upsert album: %w", err)
			}
			c.albums[t.AlbumNavID] = id
		}
		albumID = &id
	}
	var year *int32
	if t.Year > 0 {
		y := int32(t.Year) //nolint:gosec // small ints
		year = &y
	}
	_, err = s.w.UpsertTrack(ctx, db.UpsertTrackParams{
		Genre: t.Genre, Year: year, MbRecordingID: rec, MbReleaseGroupID: rg,
		ID: uuid.New(), NavidromeID: ptr(t.NavidromeID), AlbumID: albumID, ArtistID: artistID, Title: t.Title,
		ArtistName: t.Artist, AlbumTitle: t.Album, TrackNo: int32(t.TrackNo), DurationSec: int32(t.DurationSec), //nolint:gosec // small ints
		CoverArtID: t.CoverArt, ContentType: t.ContentType, SizeBytes: t.SizeBytes, Bitrate: int32(t.Bitrate), SyncedAt: syncedAt, //nolint:gosec // small ints
	})
	if err != nil {
		return fmt.Errorf("upsert track: %w", err)
	}
	if rg != nil && albumID != nil {
		if err := s.w.SetAlbumMBID(ctx, db.SetAlbumMBIDParams{Mbid: rg, ID: *albumID}); err != nil {
			return fmt.Errorf("album mbid: %w", err)
		}
	}
	return nil
}

// mbidsFor resolves a file's MusicBrainz recording (+ release group): its tag,
// else the acquired file with exactly the same size (Navidrome hides real paths).
func (s *Store) mbidsFor(ctx context.Context, c *SyncCache, t CatalogTrack) (*uuid.UUID, *uuid.UUID, error) {
	var tagged *uuid.UUID
	if rec, err := uuid.Parse(t.MBRecording); err == nil {
		tagged = &rec
	}
	if t.SizeBytes <= 0 {
		return tagged, nil, nil
	}
	if c.acquired == nil {
		rows, err := s.w.AcquiredFileSizes(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("acquired files: %w", err)
		}
		c.acquired = make(map[int64]*acquiredIDs, len(rows))
		for _, r := range rows {
			if prev, dup := c.acquired[r.SizeBytes]; dup && (prev == nil || prev.rec != r.MbRecordingID) {
				c.acquired[r.SizeBytes] = nil
				continue
			}
			c.acquired[r.SizeBytes] = &acquiredIDs{rec: r.MbRecordingID, rg: r.MbReleaseGroupID}
		}
	}
	a := c.acquired[t.SizeBytes]
	switch {
	case a == nil:
		return tagged, nil, nil
	case tagged != nil && *tagged != a.rec:
		return tagged, nil, nil
	default:
		rec := a.rec
		return &rec, a.rg, nil
	}
}

// MarkMissingDeleted soft-deletes tracks not seen since syncedBefore.
// Tracks with a known MusicBrainz recording are turned back into placeholders.
func (s *Store) MarkMissingDeleted(ctx context.Context, syncedBefore time.Time) (int64, error) {
	reverted, err := s.w.RevertUnsyncedToRemote(ctx, syncedBefore)
	if err != nil {
		return 0, fmt.Errorf("revert to remote: %w", err)
	}
	deleted, err := s.w.MarkUnsyncedTracksDeleted(ctx, syncedBefore)
	return reverted + deleted, err
}

// CountActiveTracks counts non-deleted tracks.
func (s *Store) CountActiveTracks(ctx context.Context) (int64, error) {
	return s.w.CountActiveTracks(ctx)
}

// RefreshPopularity refreshes the aggregate (CONCURRENTLY: reads never block).
func (s *Store) RefreshPopularity(ctx context.Context) error {
	return s.w.RefreshPopularity(ctx)
}

// EnsurePartition creates the monthly play_events partition containing month.
func (s *Store) EnsurePartition(ctx context.Context, month time.Time) (string, error) {
	return s.w.EnsurePlayEventsPartition(ctx, pgtype.Date{Time: month, Valid: true})
}

// ExportTrack is one row of the internal catalog export (reco-service).
type ExportTrack struct {
	ID          uuid.UUID
	Title       string
	ArtistID    uuid.UUID
	Artist      string
	AlbumID     *uuid.UUID
	Album       string
	Genre       string
	Year        *int32
	DurationSec int
	Popularity  float64
	CreatedAt   time.Time
}

// ExportCatalog pages active tracks by id (after = nil for the first page).
func (s *Store) ExportCatalog(ctx context.Context, after *uuid.UUID, limit int) ([]ExportTrack, error) {
	rows, err := s.r.ExportCatalog(ctx, db.ExportCatalogParams{After: after, Lim: int32(limit)}) //nolint:gosec // limit ≤ 1000
	if err != nil {
		return nil, fmt.Errorf("export catalog: %w", err)
	}
	out := make([]ExportTrack, len(rows))
	for i, r := range rows {
		out[i] = ExportTrack{
			ID: r.ID, Title: r.Title, ArtistID: r.ArtistID, Artist: r.ArtistName, AlbumID: r.AlbumID,
			Album: r.AlbumTitle, Genre: r.Genre, Year: r.Year, DurationSec: int(r.DurationSec), Popularity: r.Popularity, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

// Interaction is one exported user signal (backfill of reco-service).
type Interaction struct {
	Kind        string // like | playlist_add | play
	UserID      uuid.UUID
	TrackID     uuid.UUID
	At          time.Time
	PositionSec float64
	Completed   bool
	EventID     string
}

// ExportInteractions emits likes, playlist adds and play events since `since`.
func (s *Store) ExportInteractions(ctx context.Context, since time.Time, emit func(Interaction) error) error {
	likes, err := s.w.ExportLikes(ctx)
	if err != nil {
		return fmt.Errorf("export likes: %w", err)
	}
	for _, l := range likes {
		if err := emit(Interaction{Kind: "like", UserID: l.UserID, TrackID: l.TrackID, At: l.CreatedAt}); err != nil {
			return err
		}
	}
	adds, err := s.w.ExportPlaylistAdds(ctx)
	if err != nil {
		return fmt.Errorf("export playlist adds: %w", err)
	}
	for _, a := range adds {
		if err := emit(Interaction{Kind: "playlist_add", UserID: a.UserID, TrackID: a.TrackID, At: a.AddedAt}); err != nil {
			return err
		}
	}
	plays, err := s.w.ExportPlayEvents(ctx, since)
	if err != nil {
		return fmt.Errorf("export plays: %w", err)
	}
	for _, p := range plays {
		if err := emit(Interaction{
			Kind: "play", UserID: p.UserID, TrackID: p.TrackID, At: p.PlayedAt,
			PositionSec: float64(p.PositionSec), Completed: p.Completed, EventID: p.EventID,
		}); err != nil {
			return err
		}
	}
	return nil
}
