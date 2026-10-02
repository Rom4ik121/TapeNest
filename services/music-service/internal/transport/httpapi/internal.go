package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/repo"
)

// CatalogItemDTO is one track of the internal catalog export.
type CatalogItemDTO struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	ArtistID    string    `json:"artistId"`
	Artist      string    `json:"artist"`
	AlbumID     *string   `json:"albumId"`
	Album       string    `json:"album"`
	Genre       string    `json:"genre"`
	Year        *int32    `json:"year"`
	DurationSec int       `json:"durationSec"`
	Popularity  float64   `json:"popularity"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (h *handlers) exportCatalog(w http.ResponseWriter, r *http.Request) {
	var after *uuid.UUID
	if a := r.URL.Query().Get("after"); a != "" {
		id, err := uuid.Parse(a)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalid, "invalid after")
			return
		}
		after = &id
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	items, next, err := h.d.Internal.Catalog(r.Context(), after, limit)
	if err != nil {
		h.serviceError(w, r, "export catalog", err)
		return
	}
	out := make([]CatalogItemDTO, 0, len(items))
	for _, t := range items {
		d := CatalogItemDTO{
			ID: t.ID.String(), Title: t.Title, ArtistID: t.ArtistID.String(), Artist: t.Artist, Album: t.Album,
			Genre: t.Genre, Year: t.Year, DurationSec: t.DurationSec, Popularity: t.Popularity, CreatedAt: t.CreatedAt,
		}
		if t.AlbumID != nil {
			s := t.AlbumID.String()
			d.AlbumID = &s
		}
		out = append(out, d)
	}
	var nextS *string
	if next != nil {
		s := next.String()
		nextS = &s
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next": nextS})
}

// InteractionDTO is one NDJSON line of the interactions export.
type InteractionDTO struct {
	Kind        string    `json:"kind"`
	UserID      string    `json:"userId"`
	TrackID     string    `json:"trackId"`
	At          time.Time `json:"at"`
	PositionSec float64   `json:"positionSec,omitempty"`
	Completed   bool      `json:"completed,omitempty"`
	EventID     string    `json:"eventId,omitempty"`
}

func (h *handlers) exportInteractions(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 90
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	since := h.d.Now().Add(-time.Duration(days) * 24 * time.Hour)
	err := h.d.Internal.Interactions(r.Context(), since, func(i repo.Interaction) error {
		return enc.Encode(InteractionDTO{
			Kind: i.Kind, UserID: i.UserID.String(), TrackID: i.TrackID.String(), At: i.At,
			PositionSec: i.PositionSec, Completed: i.Completed, EventID: i.EventID,
		})
	})
	if err != nil {
		// headers are gone once streaming started; log and cut the stream
		h.d.Log.Warn("export interactions failed", "err", err)
	}
}

func (h *handlers) audio(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(chi.URLParam(r, "id"), "track id")
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	if err := h.d.Streamer.ServeStream(w, r, id); err != nil {
		h.serviceError(w, r, "audio", err)
	}
}
