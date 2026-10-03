package httpapi

import (
	"net/http"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
)

// CodeNoSources is returned when a track cannot be played.
const CodeNoSources = "NO_SOURCES"

// AlbumDTO is an album of the unified catalog.
type AlbumDTO struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	ArtistID *string `json:"artistId"`
	Year     *int    `json:"year"`
	CoverURL *string `json:"coverUrl"`
}

// ArtistDTO is an artist of the unified catalog.
type ArtistDTO struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CoverURL *string `json:"coverUrl"`
}

func (h *handlers) album(a domain.Album) AlbumDTO {
	d := AlbumDTO{ID: a.ID.String(), Title: a.Title, Artist: a.Artist}
	if a.ArtistID != nil {
		s := a.ArtistID.String()
		d.ArtistID = &s
	}
	if a.Year > 0 {
		y := a.Year
		d.Year = &y
	}
	if u := h.d.Streamer.CoverURL(a.CoverArtID); u != "" {
		d.CoverURL = &u
	}
	return d
}

func (h *handlers) albums(as []domain.Album) []AlbumDTO {
	out := make([]AlbumDTO, 0, len(as))
	for _, a := range as {
		out = append(out, h.album(a))
	}
	return out
}

func artistDTO(a domain.Artist, cover string) ArtistDTO {
	d := ArtistDTO{ID: a.ID.String(), Name: a.Name}
	if cover != "" {
		d.CoverURL = &cover
	}
	return d
}

func (h *handlers) searchAll(w http.ResponseWriter, r *http.Request) {
	res, err := h.d.Discovery.Search(r.Context(), userFrom(r.Context()), r.URL.Query().Get("q"))
	if err != nil {
		h.serviceError(w, r, "search", err)
		return
	}
	artists := make([]ArtistDTO, 0, len(res.Artists))
	for _, a := range res.Artists {
		artists = append(artists, artistDTO(a, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": h.tracks(res.Tracks), "albums": h.albums(res.Albums), "artists": artists})
}

func (h *handlers) getAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "album id")
	if !ok {
		return
	}
	v, err := h.d.Discovery.Album(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "album", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"album": h.album(v.Album), "tracks": h.tracks(v.Tracks)})
}

func (h *handlers) getArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "artist id")
	if !ok {
		return
	}
	v, err := h.d.Discovery.Artist(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "artist", err)
		return
	}
	cover := ""
	for _, a := range v.Albums {
		if a.CoverArtID != "" && !a.Remote {
			cover = h.d.Streamer.CoverURL(a.CoverArtID)
			break
		}
	}
	if cover == "" && len(v.Albums) > 0 {
		cover = h.d.Streamer.CoverURL(v.Albums[0].CoverArtID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"artist": artistDTO(v.Artist, cover), "albums": h.albums(v.Albums), "tracks": h.tracks(v.Tracks)})
}
