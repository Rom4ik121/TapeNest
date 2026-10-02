package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/acq"
	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

// Acquirer is the acquisition-service port (acq.Client; faked in tests).
type Acquirer interface {
	Search(ctx context.Context, q string, limit int) (acq.SearchResult, error)
	Album(ctx context.Context, rg uuid.UUID) (acq.Album, error)
	Artist(ctx context.Context, id uuid.UUID) (acq.ArtistView, error)
	Acquire(ctx context.Context, r acq.AcquireRequest) (acq.Status, error)
	Stream(ctx context.Context, rec uuid.UUID, h http.Header, method string) (*http.Response, error)
	Admin(ctx context.Context, path string, q url.Values) (json.RawMessage, error)
}

// DiscoverStore is the persistence port of the unified catalog.
type DiscoverStore interface {
	Search(ctx context.Context, user uuid.UUID, q, pattern string, cur *domain.Cursor, limit int) (domain.Page, error)
	SearchAlbums(ctx context.Context, q, pattern string, limit int) ([]domain.Album, error)
	SearchArtists(ctx context.Context, q, pattern string, limit int) ([]domain.Artist, error)
	TracksByIDs(ctx context.Context, user uuid.UUID, ids []uuid.UUID) ([]domain.Track, error)
	UpsertRemoteArtist(ctx context.Context, mbid uuid.UUID, name string) (uuid.UUID, error)
	UpsertRemoteAlbum(ctx context.Context, a repo.RemoteAlbum) (uuid.UUID, error)
	UpsertRemoteTrack(ctx context.Context, t repo.RemoteTrack, albumID, artistID *uuid.UUID) (uuid.UUID, error)
	Album(ctx context.Context, id uuid.UUID) (domain.Album, error)
	AlbumTracks(ctx context.Context, user, album uuid.UUID) ([]domain.Track, error)
	Artist(ctx context.Context, id uuid.UUID) (domain.Artist, error)
	ArtistAlbums(ctx context.Context, id uuid.UUID) ([]domain.Album, error)
	ArtistTracks(ctx context.Context, user, id uuid.UUID, limit int) ([]domain.Track, error)
	AddAcquiredFiles(ctx context.Context, files []repo.AcquiredFile) error
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

// Discovery is the unified catalog: the local library, then YouTube Music
// (ADR 0012), then MusicBrainz/torrents only when YouTube Music returns nothing
// (ADR 0011). External items are placeholders with stable UUIDs.
type Discovery struct {
	store         DiscoverStore
	acq           Acquirer // nil: no torrent fallback
	yt            YouTube  // nil: no YouTube Music
	log           *slog.Logger
	searchTimeout time.Duration
	notify        func(ctx context.Context) error
}

// YouTube is the YouTube Music catalog port (ytm.Client; faked in tests).
type YouTube interface {
	Search(ctx context.Context, q string) (ytm.Catalog, error)
	Album(ctx context.Context, browseID string) ([]ytm.Track, error)
}

// NewDiscovery creates the service; a may be nil (acquisition not configured).
func NewDiscovery(store DiscoverStore, a Acquirer, log *slog.Logger) *Discovery {
	return &Discovery{store: store, acq: a, log: log, searchTimeout: 3500 * time.Millisecond, notify: func(context.Context) error { return nil }}
}

// WithRefreshNotifier sets how a catalog refresh reaches the worker.
func (d *Discovery) WithRefreshNotifier(fn func(ctx context.Context) error) *Discovery {
	if fn != nil {
		d.notify = fn
	}
	return d
}

// Enabled reports whether external search/acquisition is configured.
func (d *Discovery) Enabled() bool { return d.acq != nil || d.yt != nil }

// WithYouTube makes YouTube Music the primary external catalog.
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

// Search returns tracks, albums and artists. External results are best effort:
// on timeout/error the library half is returned alone.
func (d *Discovery) Search(ctx context.Context, user uuid.UUID, q string) (domain.SearchResult, error) {
	query, pattern, err := domain.SearchQuery(q)
	if err != nil {
		return domain.SearchResult{}, err
	}
	var (
		wg      sync.WaitGroup
		ext     acq.SearchResult
		extErr  error
		page    domain.Page
		albums  []domain.Album
		artists []domain.Artist
		errs    [3]error
	)
	var (
		ytCat ytm.Catalog
		ytErr error
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
	if !ytHit && d.acq != nil {
		ectx, cancel := context.WithTimeout(ctx, d.searchTimeout)
		ext, extErr = d.acq.Search(ectx, q, 10)
		cancel()
		if extErr != nil && d.log != nil {
			d.log.Warn("external search failed, library only", "err", extErr)
		}
	}
	libTracks := page.Items
	if d.yt != nil && len(libTracks) > 8 {
		libTracks = libTracks[:8]
	}
	res := domain.SearchResult{Tracks: libTracks, Albums: albums, Artists: artists}
	if ytHit {
		if err := d.mergeYouTube(ctx, user, &res, ytCat); err != nil {
			return domain.SearchResult{}, err
		}
	} else if extErr == nil && d.acq != nil {
		if err := d.merge(ctx, user, &res, ext); err != nil {
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

// merge mirrors external results as placeholders and appends the ones the
// library does not already have (same UUID or same normalized artist+title).
func (d *Discovery) merge(ctx context.Context, user uuid.UUID, res *domain.SearchResult, ext acq.SearchResult) error {
	seenTrack := map[uuid.UUID]bool{}
	seenTrackKey := map[string]bool{}
	for _, t := range res.Tracks {
		seenTrack[t.ID] = true
		seenTrackKey[norm(t.Artist, t.Title)] = true
	}
	var extIDs []uuid.UUID
	for _, r := range ext.Recordings {
		if seenTrackKey[norm(r.Artist, r.Title)] || r.ReleaseGroupMBID == uuid.Nil {
			continue
		}
		albumID, err := d.store.UpsertRemoteAlbum(ctx, repo.RemoteAlbum{MBID: r.ReleaseGroupMBID, ArtistMBID: r.ArtistMBID, Artist: r.Artist, Title: r.Album, Year: r.Year})
		if err != nil {
			return err
		}
		var artistID *uuid.UUID
		if r.ArtistMBID != uuid.Nil {
			id, err := d.store.UpsertRemoteArtist(ctx, r.ArtistMBID, r.Artist)
			if err != nil {
				return err
			}
			artistID = &id
		}
		id, err := d.store.UpsertRemoteTrack(ctx, repo.RemoteTrack{
			Recording: r.MBID, ReleaseGroup: r.ReleaseGroupMBID, ArtistMBID: r.ArtistMBID, Title: r.Title,
			Artist: r.Artist, Album: r.Album, DurationSec: r.LengthMS / 1000,
		}, &albumID, artistID)
		if err != nil {
			return err
		}
		if !seenTrack[id] {
			seenTrack[id] = true
			seenTrackKey[norm(r.Artist, r.Title)] = true
			extIDs = append(extIDs, id)
		}
	}
	if len(extIDs) > 0 {
		tracks, err := d.store.TracksByIDs(ctx, user, extIDs)
		if err != nil {
			return err
		}
		res.Tracks = append(res.Tracks, tracks...)
	}

	seenAlbum := map[uuid.UUID]bool{}
	seenAlbumKey := map[string]bool{}
	for _, a := range res.Albums {
		seenAlbum[a.ID] = true
		seenAlbumKey[norm(a.Artist, a.Title)] = true
	}
	for _, rg := range ext.Albums {
		if seenAlbumKey[norm(rg.Artist, rg.Title)] {
			continue
		}
		id, err := d.store.UpsertRemoteAlbum(ctx, repo.RemoteAlbum{MBID: rg.MBID, ArtistMBID: rg.ArtistMBID, Artist: rg.Artist, Title: rg.Title, Year: rg.Year})
		if err != nil {
			return err
		}
		if seenAlbum[id] {
			continue
		}
		seenAlbum[id] = true
		seenAlbumKey[norm(rg.Artist, rg.Title)] = true
		// the stored row: a release group already in the library renders with
		// the library's own metadata/cover
		al, err := d.store.Album(ctx, id)
		if err != nil {
			return err
		}
		seenAlbumKey[norm(al.Artist, al.Title)] = true
		res.Albums = append(res.Albums, al)
	}

	seenArtist := map[uuid.UUID]bool{}
	seenArtistKey := map[string]bool{}
	for _, a := range res.Artists {
		seenArtist[a.ID] = true
		seenArtistKey[norm(a.Name)] = true
	}
	for _, a := range ext.Artists {
		if seenArtistKey[norm(a.Name)] {
			continue
		}
		id, err := d.store.UpsertRemoteArtist(ctx, a.MBID, a.Name)
		if err != nil {
			return err
		}
		if seenArtist[id] {
			continue
		}
		seenArtist[id] = true
		seenArtistKey[norm(a.Name)] = true
		ar, err := d.store.Artist(ctx, id)
		if err != nil {
			return err
		}
		res.Artists = append(res.Artists, ar)
	}
	return nil
}

// AlbumView is an album with its tracks.
type AlbumView struct {
	Album  domain.Album
	Tracks []domain.Track
}

// Album returns an album; a remote album's tracklist is filled from MusicBrainz.
func (d *Discovery) Album(ctx context.Context, user, id uuid.UUID) (AlbumView, error) {
	a, err := d.store.Album(ctx, id)
	if err != nil {
		return AlbumView{}, err
	}
	var ordered []uuid.UUID // tracklist order (recordings may live on another album)
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
	if len(ordered) == 0 && a.Remote && a.MBID != nil && d.acq != nil {
		actx, cancel := context.WithTimeout(ctx, 6*time.Second)
		mb, err := d.acq.Album(actx, *a.MBID)
		cancel()
		if err == nil {
			var artistID *uuid.UUID
			if mb.ArtistMBID != uuid.Nil {
				aid, err := d.store.UpsertRemoteArtist(ctx, mb.ArtistMBID, mb.Artist)
				if err != nil {
					return AlbumView{}, err
				}
				artistID = &aid
			}
			for _, t := range mb.Tracks {
				artist := t.Artist
				if artist == "" {
					artist = mb.Artist
				}
				tid, err := d.store.UpsertRemoteTrack(ctx, repo.RemoteTrack{
					Recording: t.RecordingMBID, ReleaseGroup: mb.MBID, ArtistMBID: mb.ArtistMBID, Title: t.Title, Artist: artist,
					Album: mb.Title, TrackNo: (t.Disc-1)*100 + t.Position, DurationSec: t.LengthMS / 1000,
				}, &id, artistID)
				if err != nil {
					return AlbumView{}, err
				}
				ordered = append(ordered, tid)
			}
		} else if d.log != nil {
			d.log.Warn("album tracklist lookup failed", "err", err)
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

// Artist returns an artist; the discography comes from MusicBrainz when known.
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
	} else if ar.MBID != nil && d.acq != nil {
		actx, cancel := context.WithTimeout(ctx, 6*time.Second)
		v, err := d.acq.Artist(actx, *ar.MBID)
		cancel()
		if err == nil {
			for _, rg := range v.Albums {
				if _, err := d.store.UpsertRemoteAlbum(ctx, repo.RemoteAlbum{MBID: rg.MBID, ArtistMBID: *ar.MBID, Artist: ar.Name, Title: rg.Title, Year: rg.Year}); err != nil {
					return ArtistView{}, err
				}
			}
		} else if d.log != nil {
			d.log.Warn("artist discography lookup failed", "err", err)
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

// RefreshFile is one imported file reported by acquisition-service.
type RefreshFile struct {
	Path          string    `json:"path"`
	RecordingMBID uuid.UUID `json:"recordingMbid"`
	ReleaseGroup  uuid.UUID `json:"releaseGroupMbid"`
	SizeBytes     int64     `json:"sizeBytes"`
}

// Refresh stores imported files and asks the worker to rescan + sync now.
func (d *Discovery) Refresh(ctx context.Context, files []RefreshFile) error {
	var af []repo.AcquiredFile
	for _, f := range files {
		p := strings.TrimSpace(f.Path)
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || f.RecordingMBID == uuid.Nil {
			return domain.Invalid("invalid file entry")
		}
		af = append(af, repo.AcquiredFile{Path: p, Recording: f.RecordingMBID, ReleaseGroup: f.ReleaseGroup, SizeBytes: f.SizeBytes})
	}
	if err := d.store.AddAcquiredFiles(ctx, af); err != nil {
		return err
	}
	return d.notify(ctx)
}

// Admin proxies read-only acquisition admin endpoints.
func (d *Discovery) Admin(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	if d.acq == nil {
		return nil, domain.ErrNotFound
	}
	out, err := d.acq.Admin(ctx, path, q)
	if errors.Is(err, acq.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	return out, err
}

// acquireErr maps acquisition errors to domain errors.
func acquireErr(err error) error {
	switch {
	case errors.Is(err, acq.ErrQuota):
		return domain.ErrAcquireQuota
	case errors.Is(err, acq.ErrDisabled):
		return domain.ErrAcquireDisabled
	case errors.Is(err, acq.ErrNotFound):
		return domain.ErrNoSources
	case errors.Is(err, acq.ErrStorageFull):
		return domain.ErrAcquireQuota
	}
	return err
}
