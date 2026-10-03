package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

// DiscoverStore is the persistence port of the unified catalog.
type DiscoverStore interface {
	Search(ctx context.Context, user uuid.UUID, q, pattern string, cur *domain.Cursor, limit int) (domain.Page, error)
	SearchAlbums(ctx context.Context, q, pattern string, limit int) ([]domain.Album, error)
	SearchArtists(ctx context.Context, q, pattern string, limit int) ([]domain.Artist, error)
	TracksByIDs(ctx context.Context, user uuid.UUID, ids []uuid.UUID) ([]domain.Track, error)
	Album(ctx context.Context, id uuid.UUID) (domain.Album, error)
	AlbumTracks(ctx context.Context, user, album uuid.UUID) ([]domain.Track, error)
	Artist(ctx context.Context, id uuid.UUID) (domain.Artist, error)
	ArtistAlbums(ctx context.Context, id uuid.UUID) ([]domain.Album, error)
	ArtistTracks(ctx context.Context, user, id uuid.UUID, limit int) ([]domain.Track, error)
	UpsertYouTubeArtist(ctx context.Context, browseID, name, cover string) (uuid.UUID, error)
	UpsertYouTubeAlbum(ctx context.Context, browseID string, artistID *uuid.UUID, title string, year int, cover string) (uuid.UUID, error)
	UpsertYouTubeTrack(ctx context.Context, videoID string, albumID, artistID *uuid.UUID, title, artist, album string, trackNo, duration int, cover string) (uuid.UUID, error)
}

// Search limits.
const (
	searchTracks  = 20
	searchAlbums  = 12
	searchArtists = 8
)

// Discovery is the unified catalog: the local library, then YouTube Music (ADR 0012).
type Discovery struct {
	store DiscoverStore
	yt    YouTube // nil: library only
	log   *slog.Logger
}

// YouTube is the YouTube Music catalog port (ytm.Client; faked in tests).
type YouTube interface {
	Search(ctx context.Context, q string) (ytm.Catalog, error)
	Album(ctx context.Context, browseID string) ([]ytm.Track, error)
}

// NewDiscovery creates the service.
func NewDiscovery(store DiscoverStore, log *slog.Logger) *Discovery {
	return &Discovery{store: store, log: log}
}

// Enabled reports whether YouTube Music search is configured.
func (d *Discovery) Enabled() bool { return d.yt != nil }

// WithYouTube makes YouTube Music the external catalog.
func (d *Discovery) WithYouTube(y YouTube) *Discovery {
	d.yt = y
	return d
}

func norm(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		for _, t := range strings.FieldsFunc(strings.ToLower(p), func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r <= 127
		}) {
			b.WriteString(t)
			b.WriteByte(' ')
		}
		b.WriteByte('|')
	}
	return b.String()
}

// Search returns tracks, albums and artists. YouTube Music is best effort:
// on timeout or error the library half is returned alone.
func (d *Discovery) Search(ctx context.Context, user uuid.UUID, q string) (domain.SearchResult, error) {
	query, pattern, err := domain.SearchQuery(q)
	if err != nil {
		return domain.SearchResult{}, err
	}
	var (
		wg      sync.WaitGroup
		page    domain.Page
		albums  []domain.Album
		artists []domain.Artist
		errs    [3]error
		ytCat   ytm.Catalog
		ytErr   error
	)
	if d.yt != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ectx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			ytCat, ytErr = d.yt.Search(ectx, q)
		}()
	}
	wg.Add(3)
	go func() { defer wg.Done(); page, errs[0] = d.store.Search(ctx, user, query, pattern, nil, searchTracks) }()
	go func() { defer wg.Done(); albums, errs[1] = d.store.SearchAlbums(ctx, query, pattern, searchAlbums) }()
	go func() { defer wg.Done(); artists, errs[2] = d.store.SearchArtists(ctx, query, pattern, searchArtists) }()
	wg.Wait()
	if err := errors.Join(errs[:]...); err != nil {
		return domain.SearchResult{}, err
	}
	if ytErr != nil && d.log != nil {
		d.log.Warn("youtube music search failed", "err", ytErr)
	}
	ytHit := ytErr == nil && d.yt != nil && len(ytCat.Tracks)+len(ytCat.Albums)+len(ytCat.Artists) > 0
	libTracks := page.Items
	if d.yt != nil && len(libTracks) > 8 {
		libTracks = libTracks[:8]
	}
	res := domain.SearchResult{Tracks: libTracks, Albums: albums, Artists: artists}
	if ytHit {
		if err := d.mergeYouTube(ctx, user, &res, ytCat); err != nil {
			return domain.SearchResult{}, err
		}
	}
	res.Tracks = capSlice(res.Tracks, searchTracks)
	res.Albums = capSlice(res.Albums, searchAlbums)
	res.Artists = capSlice(res.Artists, searchArtists)
	return res, nil
}

func capSlice[T any](s []T, n int) []T {
	if s == nil {
		return []T{}
	}
	if len(s) > n {
		return s[:n]
	}
	return s
}

// AlbumView is an album with its tracks.
type AlbumView struct {
	Album  domain.Album
	Tracks []domain.Track
}

// Album returns an album. A YouTube Music album's tracklist is filled from YouTube.
func (d *Discovery) Album(ctx context.Context, user, id uuid.UUID) (AlbumView, error) {
	a, err := d.store.Album(ctx, id)
	if err != nil {
		return AlbumView{}, err
	}
	var ordered []uuid.UUID
	if strings.HasPrefix(a.YouTubeBrowse, "MPRE") && d.yt != nil {
		actx, cancel := context.WithTimeout(ctx, 8*time.Second)
		tracks, err := d.yt.Album(actx, a.YouTubeBrowse)
		cancel()
		if err != nil && d.log != nil {
			d.log.Warn("youtube album lookup failed", "err", err)
		}
		for i, tr := range tracks {
			artist := tr.Artist
			if artist == "" {
				artist = a.Artist
			}
			var artistID *uuid.UUID
			if artist != "" {
				key := ytm.NameKey(artist)
				aid, err := d.store.UpsertYouTubeArtist(ctx, key, artist, "")
				if err != nil {
					return AlbumView{}, err
				}
				artistID = &aid
			}
			tid, err := d.store.UpsertYouTubeTrack(ctx, tr.VideoID, &id, artistID, tr.Title, artist, a.Title, i+1, tr.DurationSec, ytm.VideoCover(tr.VideoID))
			if err != nil {
				return AlbumView{}, err
			}
			ordered = append(ordered, tid)
		}
	}
	if len(ordered) > 0 {
		tracks, err := d.store.TracksByIDs(ctx, user, ordered)
		if err != nil {
			return AlbumView{}, err
		}
		return AlbumView{Album: a, Tracks: inOrder(tracks, ordered)}, nil
	}
	tracks, err := d.store.AlbumTracks(ctx, user, id)
	if err != nil {
		return AlbumView{}, err
	}
	return AlbumView{Album: a, Tracks: tracks}, nil
}

// inOrder sorts tracks by ids (duplicates and unknown ids dropped).
func inOrder(tracks []domain.Track, ids []uuid.UUID) []domain.Track {
	byID := make(map[uuid.UUID]domain.Track, len(tracks))
	for _, t := range tracks {
		byID[t.ID] = t
	}
	out := make([]domain.Track, 0, len(ids))
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if t, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, t)
		}
	}
	return out
}

// ArtistView is an artist with albums and top tracks.
type ArtistView struct {
	Artist domain.Artist
	Albums []domain.Album
	Tracks []domain.Track
}

// Artist returns an artist. A YouTube Music artist's rows are filled from search.
func (d *Discovery) Artist(ctx context.Context, user, id uuid.UUID) (ArtistView, error) {
	ar, err := d.store.Artist(ctx, id)
	if err != nil {
		return ArtistView{}, err
	}
	if ar.YouTubeBrowse != "" && d.yt != nil {
		actx, cancel := context.WithTimeout(ctx, 8*time.Second)
		cat, err := d.yt.Search(actx, ar.Name)
		cancel()
		if err != nil && d.log != nil {
			d.log.Warn("youtube artist lookup failed", "err", err)
		} else if err == nil {
			aid := id
			for _, tr := range cat.Tracks {
				if _, err := d.store.UpsertYouTubeTrack(ctx, tr.VideoID, nil, &aid, tr.Title, ar.Name, tr.Album, 0, tr.DurationSec, ytm.VideoCover(tr.VideoID)); err != nil {
					return ArtistView{}, err
				}
			}
			for _, al := range cat.Albums {
				if _, err := d.store.UpsertYouTubeAlbum(ctx, al.BrowseID, &aid, al.Title, al.Year, ytm.EncodeThumb(al.Thumb)); err != nil {
					return ArtistView{}, err
				}
			}
		}
	}
	albums, err := d.store.ArtistAlbums(ctx, id)
	if err != nil {
		return ArtistView{}, err
	}
	tracks, err := d.store.ArtistTracks(ctx, user, id, 20)
	if err != nil {
		return ArtistView{}, err
	}
	for i := range albums {
		if albums[i].Artist == "" {
			albums[i].Artist = ar.Name
		}
	}
	return ArtistView{Artist: ar, Albums: albums, Tracks: tracks}, nil
}
