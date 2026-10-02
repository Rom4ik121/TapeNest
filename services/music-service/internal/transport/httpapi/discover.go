package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
)

// Acquisition error codes (player toast).
const (
	CodeNoSources     = "NO_SOURCES"
	CodeAcquireQuota  = "ACQUIRE_QUOTA"
	CodeNotAvailable  = "NOT_AVAILABLE"
	CodeForbidden     = "FORBIDDEN"
	defaultRetryAfter = 1500
)

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

// acquisitionError writes stream-url outcomes for remote tracks; false when err is not one.
func (h *handlers) acquisitionError(w http.ResponseWriter, err error) bool {
	var pending *domain.PendingError
	switch {
	case errors.As(err, &pending):
		retry := pending.RetryAfter
		if retry <= 0 {
			retry = defaultRetryAfter
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", strconv.Itoa(max(1, retry/1000)))
		writeJSON(w, http.StatusAccepted, map[string]any{"state": pending.State, "progress": pending.Progress, "retryAfterMs": retry})
	case errors.Is(err, domain.ErrNoSources):
		writeError(w, http.StatusNotFound, CodeNoSources, "no sources found for this track")
	case errors.Is(err, domain.ErrAcquireQuota):
		writeError(w, http.StatusTooManyRequests, CodeAcquireQuota, "too many new tracks requested, try again later")
	case errors.Is(err, domain.ErrAcquireDisabled):
		writeError(w, http.StatusNotFound, CodeNotAvailable, "this track is not available")
	default:
		return false
	}
	return true
}

func (h *handlers) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Role") != "admin" {
			writeError(w, http.StatusForbidden, CodeForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) adminProxy(path func(r *http.Request) (string, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := path(r)
		if !ok {
			writeError(w, http.StatusBadRequest, CodeInvalid, "invalid id")
			return
		}
		q := url.Values{}
		if s := r.URL.Query().Get("state"); s != "" {
			q.Set("state", s)
		}
		raw, err := h.d.Discovery.Admin(r.Context(), p, q)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, CodeNotFound, "not found")
				return
			}
			h.d.Log.WarnContext(r.Context(), "acquisition admin failed", "err", err)
			writeError(w, http.StatusBadGateway, CodeUnavailable, "acquisition service unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	}
}

func adminList(*http.Request) (string, bool)   { return "acquisitions", true }
func adminStatus(*http.Request) (string, bool) { return "status", true }
func adminGet(r *http.Request) (string, bool) {
	id, err := domain.ParseID(chi.URLParam(r, "id"), "id")
	return "acquisitions/" + id.String(), err == nil
}

func (h *handlers) catalogRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files []service.RefreshFile `json:"files"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || len(body.Files) > 2000 {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid body")
		return
	}
	if err := h.d.Discovery.Refresh(r.Context(), body.Files); err != nil {
		h.serviceError(w, r, "catalog refresh", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
