package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

type catalogDTO struct {
	Artists       int64      `json:"artists"`
	Albums        int64      `json:"albums"`
	Tracks        int64      `json:"tracks"`
	DeletedTracks int64      `json:"deletedTracks"`
	YouTubeTracks int64      `json:"youtubeTracks"`
	LibraryTracks int64      `json:"libraryTracks"`
	LastSyncedAt  *time.Time `json:"lastSyncedAt"`
}

type adminTrackDTO struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Artist string    `json:"artist"`
	Album  string    `json:"album,omitempty"`
	At     time.Time `json:"at"`
}

type tasteDTO struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

func tracksDTO(in []repo.AdminTrack) []adminTrackDTO {
	out := make([]adminTrackDTO, 0, len(in))
	for _, t := range in {
		out = append(out, adminTrackDTO{ID: t.ID.String(), Title: t.Title, Artist: t.Artist, Album: t.Album, At: t.At})
	}
	return out
}

func tasteList(in []repo.TasteCount) []tasteDTO {
	out := make([]tasteDTO, 0, len(in))
	for _, t := range in {
		out = append(out, tasteDTO{Name: t.Name, Count: t.Count})
	}
	return out
}

func (h *handlers) adminCatalog(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		writeError(w, http.StatusNotImplemented, CodeUnavailable, "catalog admin is not configured")
		return
	}
	c, err := h.d.Admin.Catalog(r.Context())
	if err != nil {
		h.serviceError(w, r, "admin catalog", err)
		return
	}
	writeJSON(w, http.StatusOK, catalogDTO{
		Artists: c.Artists, Albums: c.Albums, Tracks: c.Tracks, DeletedTracks: c.DeletedTracks,
		YouTubeTracks: c.YouTubeTracks, LibraryTracks: c.LibraryTracks, LastSyncedAt: c.LastSyncedAt,
	})
}

func (h *handlers) adminUserMusic(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		writeError(w, http.StatusNotImplemented, CodeUnavailable, "catalog admin is not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid user id")
		return
	}
	m, err := h.d.Admin.User(r.Context(), id)
	if err != nil {
		h.serviceError(w, r, "admin user music", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userId":  id.String(),
		"likes":   tracksDTO(m.Likes),
		"recent":  tracksDTO(m.Recent),
		"artists": tasteList(m.Artists),
		"genres":  tasteList(m.Genres),
	})
}
