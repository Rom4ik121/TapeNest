package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CatalogHealth is a snapshot of the music catalog. It does not add a source.
type CatalogHealth struct {
	Artists       int64
	Albums        int64
	Tracks        int64
	DeletedTracks int64
	YouTubeTracks int64
	LibraryTracks int64
	LastSyncedAt  *time.Time
}

// AdminTrack is a like or a recent play, without stream URLs.
type AdminTrack struct {
	ID     uuid.UUID
	Title  string
	Artist string
	Album  string
	At     time.Time
}

// TasteCount is how often a name shows up in the user's likes.
type TasteCount struct {
	Name  string
	Count int64
}

// UserMusic is likes and listening preferences for one user. No secrets.
type UserMusic struct {
	Likes   []AdminTrack
	Recent  []AdminTrack
	Artists []TasteCount
	Genres  []TasteCount
}

func syncedAt(v any) *time.Time {
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return nil
		}
		return &t
	case *time.Time:
		if t == nil || t.IsZero() {
			return nil
		}
		return t
	default:
		return nil
	}
}

// CatalogHealth reads catalog counts from the primary.
func (s *Store) CatalogHealth(ctx context.Context) (CatalogHealth, error) {
	row, err := s.w.CatalogHealth(ctx)
	if err != nil {
		return CatalogHealth{}, fmt.Errorf("catalog health: %w", err)
	}
	return CatalogHealth{
		Artists: row.Artists, Albums: row.Albums, Tracks: row.Tracks, DeletedTracks: row.DeletedTracks,
		YouTubeTracks: row.YoutubeTracks, LibraryTracks: row.LibraryTracks, LastSyncedAt: syncedAt(row.LastSyncedAt),
	}, nil
}

// UserMusic loads likes, recent plays and taste counts derived from likes.
func (s *Store) UserMusic(ctx context.Context, user uuid.UUID) (UserMusic, error) {
	likes, err := s.w.AdminUserLikes(ctx, user)
	if err != nil {
		return UserMusic{}, fmt.Errorf("admin likes: %w", err)
	}
	recent, err := s.w.AdminUserRecent(ctx, user)
	if err != nil {
		return UserMusic{}, fmt.Errorf("admin recent: %w", err)
	}
	artists, err := s.w.AdminUserArtists(ctx, user)
	if err != nil {
		return UserMusic{}, fmt.Errorf("admin artists: %w", err)
	}
	genres, err := s.w.AdminUserGenres(ctx, user)
	if err != nil {
		return UserMusic{}, fmt.Errorf("admin genres: %w", err)
	}
	out := UserMusic{
		Likes: make([]AdminTrack, 0, len(likes)), Recent: make([]AdminTrack, 0, len(recent)),
		Artists: make([]TasteCount, 0, len(artists)), Genres: make([]TasteCount, 0, len(genres)),
	}
	for _, l := range likes {
		out.Likes = append(out.Likes, AdminTrack{ID: l.ID, Title: l.Title, Artist: l.ArtistName, Album: l.AlbumTitle, At: l.CreatedAt})
	}
	for _, r := range recent {
		out.Recent = append(out.Recent, AdminTrack{ID: r.ID, Title: r.Title, Artist: r.ArtistName, At: r.PlayedAt})
	}
	for _, a := range artists {
		out.Artists = append(out.Artists, TasteCount{Name: a.Name, Count: a.N})
	}
	for _, g := range genres {
		out.Genres = append(out.Genres, TasteCount{Name: g.Name, Count: g.N})
	}
	return out, nil
}
