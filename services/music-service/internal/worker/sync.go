package worker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// Library is the Navidrome side of the sync (navidrome.Client).
type Library interface {
	StartScan(ctx context.Context) error
	Songs(ctx context.Context, offset, count int) ([]navidrome.Song, error)
}

// CatalogStore is the DB side of the sync (repo.Store).
type CatalogStore interface {
	UpsertCatalogTrack(ctx context.Context, c *repo.SyncCache, t repo.CatalogTrack, syncedAt time.Time) error
	MarkMissingDeleted(ctx context.Context, syncedBefore time.Time) (int64, error)
	CountActiveTracks(ctx context.Context) (int64, error)
	RefreshPopularity(ctx context.Context) error
	EnsurePartition(ctx context.Context, month time.Time) (string, error)
}

// Syncer mirrors Navidrome's library into music.artists/albums/tracks (UUID ids).
// The /music volume is filled by the Lidarr stack in production (spec §5.4) or by
// tools/dev/seed-music.py in dev; Navidrome scans it, we copy the metadata.
type Syncer struct {
	Lib      Library
	Store    CatalogStore
	PageSize int
	Log      *slog.Logger
	Now      func() time.Time
}

// SyncResult summarizes one run.
type SyncResult struct {
	Seen    int
	Deleted int64
}

// Sync copies all songs; tracks not seen in a complete listing are soft-deleted.
func (s *Syncer) Sync(ctx context.Context) (SyncResult, error) {
	if s.PageSize == 0 {
		s.PageSize = 500
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	started := now().UTC()
	cache := repo.NewSyncCache()
	var res SyncResult
	for offset := 0; ; offset += s.PageSize {
		songs, err := s.Lib.Songs(ctx, offset, s.PageSize)
		if err != nil {
			return res, err
		}
		for _, sg := range songs {
			if sg.ID == "" || sg.Title == "" {
				continue
			}
			err := s.Store.UpsertCatalogTrack(ctx, cache, repo.CatalogTrack{
				NavidromeID: sg.ID, Title: sg.Title, Artist: sg.Artist, ArtistNavID: sg.ArtistID, Album: sg.Album,
				AlbumNavID: sg.AlbumID, CoverArt: sg.CoverArt, ContentType: sg.ContentType, TrackNo: sg.Track,
				Year: sg.Year, Genre: firstGenre(sg.Genre), DurationSec: sg.Duration, Bitrate: sg.BitRate, SizeBytes: sg.Size, MBRecording: sg.MBID,
			}, started)
			if err != nil {
				return res, err
			}
			res.Seen++
		}
		if len(songs) < s.PageSize {
			break
		}
	}
	active, err := s.Store.CountActiveTracks(ctx)
	if err != nil {
		return res, err
	}
	if res.Seen == 0 && active > 0 {
		// an empty listing while we have tracks is more likely a scan in progress or a
		// misconfigured music folder than a wiped library: never mass-delete on it
		s.Log.Warn("catalog sync: navidrome returned no songs, skipping deletions", "active", active)
		return res, nil
	}
	res.Deleted, err = s.Store.MarkMissingDeleted(ctx, started)
	return res, err
}

// Maintainer keeps partitions ahead and refreshes aggregates.
type Maintainer struct {
	Store CatalogStore
	Log   *slog.Logger
	Now   func() time.Time
}

// EnsurePartitions creates this month's and the next two months' partitions.
func (m *Maintainer) EnsurePartitions(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	first := time.Date(now().Year(), now().Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := m.Store.EnsurePartition(ctx, first.AddDate(0, i, 0)); err != nil {
			return err
		}
	}
	return nil
}

// Loop runs fn now and then every interval until ctx is done; errors are logged.
func Loop(ctx context.Context, name string, every time.Duration, log *slog.Logger, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			log.Warn(name+" failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// firstGenre keeps the first of multi-valued genre tags ("Jazz; Blues" → "Jazz").
func firstGenre(g string) string {
	for _, sep := range []string{";", "/", ","} {
		if i := strings.Index(g, sep); i > 0 {
			g = g[:i]
		}
	}
	g = strings.TrimSpace(g)
	if len(g) > 40 {
		g = g[:40]
	}
	return g
}
