package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/ytm"
)

// mergeYouTube upserts YouTube Music hits as ordinary catalog rows and appends
// the ones the library does not already have (same normalized artist+title).
func (d *Discovery) mergeYouTube(ctx context.Context, user uuid.UUID, res *domain.SearchResult, cat ytm.Catalog) error {
	seenTrack := map[uuid.UUID]bool{}
	seenKey := map[string]bool{}
	for _, t := range res.Tracks {
		seenTrack[t.ID] = true
		seenKey[norm(t.Artist, t.Title)] = true
	}
	var ids []uuid.UUID
	for _, tr := range cat.Tracks {
		if !ytm.ValidVideoID(tr.VideoID) || seenKey[norm(tr.Artist, tr.Title)] {
			continue
		}
		var artistID *uuid.UUID
		if tr.Artist != "" {
			id, err := d.store.UpsertYouTubeArtist(ctx, ytm.NameKey(tr.Artist), tr.Artist, "")
			if err != nil {
				return err
			}
			artistID = &id
		}
		id, err := d.store.UpsertYouTubeTrack(ctx, tr.VideoID, nil, artistID, tr.Title, tr.Artist, tr.Album, 0, tr.DurationSec, ytm.VideoCover(tr.VideoID))
		if err != nil {
			return err
		}
		if seenTrack[id] {
			continue
		}
		seenTrack[id] = true
		seenKey[norm(tr.Artist, tr.Title)] = true
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		tracks, err := d.store.TracksByIDs(ctx, user, ids)
		if err != nil {
			return err
		}
		res.Tracks = append(res.Tracks, tracks...)
	}

	seenAlbum := map[string]bool{}
	for _, a := range res.Albums {
		seenAlbum[norm(a.Artist, a.Title)] = true
	}
	for _, al := range cat.Albums {
		if !ytm.ValidBrowseID(al.BrowseID) || seenAlbum[norm(al.Artist, al.Title)] {
			continue
		}
		var artistID *uuid.UUID
		if al.Artist != "" {
			id, err := d.store.UpsertYouTubeArtist(ctx, ytm.NameKey(al.Artist), al.Artist, "")
			if err != nil {
				return err
			}
			artistID = &id
		}
		id, err := d.store.UpsertYouTubeAlbum(ctx, al.BrowseID, artistID, al.Title, al.Year, ytm.EncodeThumb(al.Thumb))
		if err != nil {
			return err
		}
		row, err := d.store.Album(ctx, id)
		if err != nil {
			return err
		}
		seenAlbum[norm(row.Artist, row.Title)] = true
		res.Albums = append(res.Albums, row)
	}

	seenArtist := map[string]bool{}
	for _, a := range res.Artists {
		seenArtist[norm(a.Name)] = true
	}
	for _, ar := range cat.Artists {
		if !ytm.ValidBrowseID(ar.BrowseID) || seenArtist[norm(ar.Name)] {
			continue
		}
		id, err := d.store.UpsertYouTubeArtist(ctx, ar.BrowseID, ar.Name, ytm.EncodeThumb(ar.Thumb))
		if err != nil {
			return err
		}
		row, err := d.store.Artist(ctx, id)
		if err != nil {
			return err
		}
		seenArtist[norm(row.Name)] = true
		res.Artists = append(res.Artists, row)
	}
	return nil
}
