package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo/db"
)

// CoverPrefixCAA marks covers served from the Cover Art Archive (remote albums).
const CoverPrefixCAA = "caa-"

// CAACover is the cover id of a MusicBrainz release group.
func CAACover(rg uuid.UUID) string {
	if rg == uuid.Nil {
		return ""
	}
	return CoverPrefixCAA + rg.String()
}

func optUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// RemoteAlbum is a MusicBrainz release group to mirror as a placeholder.
type RemoteAlbum struct {
	MBID       uuid.UUID
	ArtistMBID uuid.UUID
	Artist     string
	Title      string
	Year       int
}

// RemoteTrack is a MusicBrainz recording to mirror as a placeholder.
type RemoteTrack struct {
	Recording    uuid.UUID
	ReleaseGroup uuid.UUID
	ArtistMBID   uuid.UUID
	Title        string
	Artist       string
	Album        string
	TrackNo      int
	DurationSec  int
}

// UpsertRemoteArtist mirrors a MusicBrainz artist (idempotent by MBID).
func (s *Store) UpsertRemoteArtist(ctx context.Context, mbid uuid.UUID, name string) (uuid.UUID, error) {
	if mbid == uuid.Nil {
		return uuid.Nil, domain.Invalid("artist mbid required")
	}
	return s.w.UpsertRemoteArtist(ctx, db.UpsertRemoteArtistParams{ID: uuid.New(), Mbid: &mbid, Name: name})
}

// UpsertRemoteAlbum mirrors a release group (and its artist).
func (s *Store) UpsertRemoteAlbum(ctx context.Context, a RemoteAlbum) (uuid.UUID, error) {
	var artistID *uuid.UUID
	if a.ArtistMBID != uuid.Nil {
		id, err := s.UpsertRemoteArtist(ctx, a.ArtistMBID, a.Artist)
		if err != nil {
			return uuid.Nil, fmt.Errorf("remote artist: %w", err)
		}
		artistID = &id
	}
	return s.w.UpsertRemoteAlbum(ctx, db.UpsertRemoteAlbumParams{
		ID: uuid.New(), Mbid: optUUID(a.MBID), ArtistID: artistID, Title: a.Title, Year: int32(a.Year), CoverArtID: CAACover(a.MBID), //nolint:gosec // small ints
	})
}

// UpsertRemoteTrack mirrors a recording; albumID/artistID may be nil.
func (s *Store) UpsertRemoteTrack(ctx context.Context, t RemoteTrack, albumID, artistID *uuid.UUID) (uuid.UUID, error) {
	return s.w.UpsertRemoteTrack(ctx, db.UpsertRemoteTrackParams{
		ID: uuid.New(), MbRecordingID: optUUID(t.Recording), MbReleaseGroupID: optUUID(t.ReleaseGroup), MbArtistID: optUUID(t.ArtistMBID),
		AlbumID: albumID, ArtistID: artistID, Title: t.Title, ArtistName: t.Artist, AlbumTitle: t.Album,
		TrackNo: int32(t.TrackNo), DurationSec: int32(max(t.DurationSec, 0)), CoverArtID: CAACover(t.ReleaseGroup), //nolint:gosec // small ints
	})
}

// SearchAlbums finds albums (library and placeholders) by title/artist.
func (s *Store) SearchAlbums(ctx context.Context, q, pattern string, limit int) ([]domain.Album, error) {
	rows, err := s.r.SearchAlbums(ctx, db.SearchAlbumsParams{Q: q, Pattern: pattern, Lim: int32(limit)}) //nolint:gosec // small
	if err != nil {
		return nil, fmt.Errorf("search albums: %w", err)
	}
	out := make([]domain.Album, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Album{ID: r.ID, Title: r.Title, Artist: r.ArtistName, ArtistID: r.ArtistID, Year: int(r.Year), CoverArtID: r.CoverArtID, Remote: r.Remote, MBID: r.Mbid})
	}
	return out, nil
}

// SearchArtists finds artists by name.
func (s *Store) SearchArtists(ctx context.Context, q, pattern string, limit int) ([]domain.Artist, error) {
	rows, err := s.r.SearchArtists(ctx, db.SearchArtistsParams{Q: q, Pattern: pattern, Lim: int32(limit)}) //nolint:gosec // small
	if err != nil {
		return nil, fmt.Errorf("search artists: %w", err)
	}
	out := make([]domain.Artist, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Artist{ID: r.ID, Name: r.Name, Remote: r.Remote, MBID: r.Mbid})
	}
	return out, nil
}

// Album returns one album.
func (s *Store) Album(ctx context.Context, id uuid.UUID) (domain.Album, error) {
	r, err := s.w.AlbumByID(ctx, id)
	if err != nil {
		return domain.Album{}, notFound(err)
	}
	return domain.Album{ID: r.ID, Title: r.Title, Artist: r.ArtistName, ArtistID: r.ArtistID, Year: int(r.Year), CoverArtID: r.CoverArtID, Remote: r.Remote, MBID: r.Mbid, YouTubeBrowse: r.YoutubeBrowseID}, nil
}

// AlbumTracks lists an album's tracks in order.
func (s *Store) AlbumTracks(ctx context.Context, user, album uuid.UUID) ([]domain.Track, error) {
	rows, err := s.w.AlbumTracks(ctx, db.AlbumTracksParams{UserID: user, AlbumID: &album})
	if err != nil {
		return nil, fmt.Errorf("album tracks: %w", err)
	}
	out := make([]domain.Track, 0, len(rows))
	for _, r := range rows {
		out = append(out, withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote))
	}
	return out, nil
}

// Artist returns one artist.
func (s *Store) Artist(ctx context.Context, id uuid.UUID) (domain.Artist, error) {
	r, err := s.w.ArtistByID(ctx, id)
	if err != nil {
		return domain.Artist{}, notFound(err)
	}
	return domain.Artist{ID: r.ID, Name: r.Name, Remote: r.Remote, MBID: r.Mbid, YouTubeBrowse: r.YoutubeBrowseID}, nil
}

// ArtistAlbums lists an artist's albums (newest first).
func (s *Store) ArtistAlbums(ctx context.Context, id uuid.UUID) ([]domain.Album, error) {
	rows, err := s.w.ArtistAlbums(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("artist albums: %w", err)
	}
	out := make([]domain.Album, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Album{ID: r.ID, Title: r.Title, ArtistID: &id, Year: int(r.Year), CoverArtID: r.CoverArtID, Remote: r.Remote, MBID: r.Mbid})
	}
	return out, nil
}

// ArtistTracks lists an artist's tracks (library first, by popularity).
func (s *Store) ArtistTracks(ctx context.Context, user, id uuid.UUID, limit int) ([]domain.Track, error) {
	rows, err := s.w.ArtistTracks(ctx, db.ArtistTracksParams{UserID: user, ArtistID: &id, Lim: int32(limit)}) //nolint:gosec // small
	if err != nil {
		return nil, fmt.Errorf("artist tracks: %w", err)
	}
	out := make([]domain.Track, 0, len(rows))
	for _, r := range rows {
		out = append(out, withRemote(track(r.ID, r.Title, r.ArtistName, r.AlbumTitle, r.CoverArtID, r.DurationSec, r.Liked), r.Remote))
	}
	return out, nil
}

// AcquiredFile is one file imported by acquisition-service.
type AcquiredFile struct {
	Path         string
	Recording    uuid.UUID
	ReleaseGroup uuid.UUID // optional
	SizeBytes    int64
}

// AddAcquiredFiles records imported files so the next sync can claim placeholders.
func (s *Store) AddAcquiredFiles(ctx context.Context, files []AcquiredFile) error {
	for _, f := range files {
		if err := s.w.InsertAcquiredFile(ctx, db.InsertAcquiredFileParams{Path: f.Path, MbRecordingID: f.Recording, MbReleaseGroupID: nilUUID(f.ReleaseGroup), SizeBytes: f.SizeBytes}); err != nil {
			return fmt.Errorf("acquired file: %w", err)
		}
	}
	return nil
}

// claim attaches a newly seen Navidrome song to its placeholder (same UUID),
// together with the placeholder album/artist, before the regular upsert.
func (s *Store) claim(ctx context.Context, t CatalogTrack) error {
	nid := t.NavidromeID
	exists, err := s.w.NavidromeTrackExists(ctx, &nid)
	if err != nil || exists {
		return err
	}
	var albumID, artistID *uuid.UUID
	claimed := false
	if rec, err := uuid.Parse(t.MBRecording); err == nil {
		r, err := s.w.ClaimByRecording(ctx, db.ClaimByRecordingParams{NavidromeID: &nid, MbRecordingID: &rec})
		switch {
		case err == nil:
			albumID, artistID, claimed = r.AlbumID, r.ArtistID, true
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("claim by recording: %w", err)
		}
	}
	if !claimed && t.SizeBytes > 0 {
		r, err := s.w.ClaimBySize(ctx, db.ClaimBySizeParams{NavidromeID: &nid, SizeBytes: t.SizeBytes})
		switch {
		case err == nil:
			albumID, artistID, claimed = r.AlbumID, r.ArtistID, true
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("claim by size: %w", err)
		}
	}
	if !claimed {
		return nil
	}
	if albumID != nil && t.AlbumNavID != "" {
		an := t.AlbumNavID
		if err := s.w.ClaimAlbum(ctx, db.ClaimAlbumParams{NavidromeID: &an, ID: *albumID}); err != nil {
			return fmt.Errorf("claim album: %w", err)
		}
	}
	if artistID != nil && t.ArtistNavID != "" {
		an := t.ArtistNavID
		if err := s.w.ClaimArtist(ctx, db.ClaimArtistParams{NavidromeID: &an, ID: *artistID}); err != nil {
			return fmt.Errorf("claim artist: %w", err)
		}
	}
	return nil
}

func nilUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func strPtr(s string) *string { return &s }

// UpsertYouTubeArtist mirrors a YouTube Music artist (idempotent by browse id).
func (s *Store) UpsertYouTubeArtist(ctx context.Context, browseID, name, cover string) (uuid.UUID, error) {
	if browseID == "" || name == "" {
		return uuid.Nil, domain.Invalid("artist required")
	}
	_ = cover // artist rows have no cover column; thumbnails live on albums and tracks
	return s.w.UpsertYouTubeArtist(ctx, db.UpsertYouTubeArtistParams{ID: uuid.New(), Name: name, YoutubeBrowseID: strPtr(browseID)})
}

// UpsertYouTubeAlbum mirrors a YouTube Music album.
func (s *Store) UpsertYouTubeAlbum(ctx context.Context, browseID string, artistID *uuid.UUID, title string, year int, cover string) (uuid.UUID, error) {
	if browseID == "" || title == "" {
		return uuid.Nil, domain.Invalid("album required")
	}
	return s.w.UpsertYouTubeAlbum(ctx, db.UpsertYouTubeAlbumParams{
		ID: uuid.New(), ArtistID: artistID, Title: title, Year: int32(year), CoverArtID: cover, YoutubeBrowseID: strPtr(browseID), //nolint:gosec // year is small
	})
}

// UpsertYouTubeTrack mirrors a YouTube Music song. Likes attach to this row.
func (s *Store) UpsertYouTubeTrack(ctx context.Context, videoID string, albumID, artistID *uuid.UUID, title, artist, album string, trackNo, duration int, cover string) (uuid.UUID, error) {
	if videoID == "" || title == "" {
		return uuid.Nil, domain.Invalid("track required")
	}
	return s.w.UpsertYouTubeTrack(ctx, db.UpsertYouTubeTrackParams{
		ID: uuid.New(), AlbumID: albumID, ArtistID: artistID, Title: title, ArtistName: artist, AlbumTitle: album,
		TrackNo: int32(trackNo), DurationSec: int32(max(duration, 0)), CoverArtID: cover, YoutubeVideoID: strPtr(videoID), //nolint:gosec // small ints
	})
}
