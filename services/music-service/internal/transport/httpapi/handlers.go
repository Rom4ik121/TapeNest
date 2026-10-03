package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
	"github.com/tapenest/tapenest/services/music-service/internal/signer"
)

// TrackDTO is the contract's Track.
type TrackDTO struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       *string `json:"album"`
	CoverURL    *string `json:"coverUrl"`
	DurationSec int     `json:"durationSec"`
	Liked       bool    `json:"liked"`
	Remote      bool    `json:"remote,omitempty"` // acquired on first play (UI renders it like any track)
}

// PageDTO is Page<Track>.
type PageDTO struct {
	Items      []TrackDTO `json:"items"`
	NextCursor *string    `json:"nextCursor"`
}

// PlaylistDTO is the contract's Playlist.
type PlaylistDTO struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	TrackCount int       `json:"trackCount"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (h *handlers) track(t domain.Track) TrackDTO {
	d := TrackDTO{ID: t.ID.String(), Title: t.Title, Artist: t.Artist, DurationSec: t.DurationSec, Liked: t.Liked, Remote: t.Remote}
	if t.Album != "" {
		a := t.Album
		d.Album = &a
	}
	if u := h.d.Streamer.CoverURL(t.CoverArtID); u != "" {
		d.CoverURL = &u
	}
	return d
}

func (h *handlers) tracks(ts []domain.Track) []TrackDTO {
	out := make([]TrackDTO, 0, len(ts))
	for _, t := range ts {
		out = append(out, h.track(t))
	}
	return out
}

func playlistDTO(p domain.Playlist) PlaylistDTO {
	return PlaylistDTO{ID: p.ID.String(), Title: p.Title, TrackCount: p.TrackCount, CreatedAt: p.CreatedAt}
}

func pathID(w http.ResponseWriter, r *http.Request, name, what string) (uuid.UUID, bool) {
	id, err := domain.ParseID(chi.URLParam(r, name), what)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
		return false
	}
	return true
}

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

func (h *handlers) page(w http.ResponseWriter, p domain.Page) {
	writeJSON(w, http.StatusOK, PageDTO{Items: h.tracks(p.Items), NextCursor: p.Next})
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	p, err := h.d.Library.List(r.Context(), userFrom(r.Context()), service.ListKind(chi.URLParam(r, "kind")), r.URL.Query().Get("cursor"), limitParam(r))
	if err != nil {
		h.serviceError(w, r, "list", err)
		return
	}
	h.page(w, p)
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := h.d.Library.Search(r.Context(), userFrom(r.Context()), q.Get("q"), q.Get("cursor"), limitParam(r))
	if err != nil {
		h.serviceError(w, r, "search", err)
		return
	}
	h.page(w, p)
}

func (h *handlers) streamURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	u, exp, err := h.d.Streamer.StreamURL(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "stream-url", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "expiresAt": exp})
}

func (h *handlers) like(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	if err := h.d.Library.Like(r.Context(), userFrom(r.Context()), id); err != nil {
		h.serviceError(w, r, "like", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) unlike(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	if err := h.d.Library.Unlike(r.Context(), userFrom(r.Context()), id); err != nil {
		h.serviceError(w, r, "unlike", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) getPosition(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	p, err := h.d.Library.Position(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "get position", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trackId": p.TrackID.String(), "positionSec": p.PositionSec, "updatedAt": p.UpdatedAt})
}

func (h *handlers) putPosition(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	var body struct {
		PositionSec *float64 `json:"positionSec"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PositionSec == nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "positionSec is required")
		return
	}
	if err := h.d.Library.SavePosition(r.Context(), userFrom(r.Context()), id, *body.PositionSec); err != nil {
		h.serviceError(w, r, "put position", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) playlists(w http.ResponseWriter, r *http.Request) {
	ps, err := h.d.Library.Playlists(r.Context(), userFrom(r.Context()))
	if err != nil {
		h.serviceError(w, r, "playlists", err)
		return
	}
	out := make([]PlaylistDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, playlistDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

type titleBody struct {
	Title string `json:"title"`
}

func (h *handlers) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var b titleBody
	if !decode(w, r, &b) {
		return
	}
	p, err := h.d.Library.CreatePlaylist(r.Context(), userFrom(r.Context()), b.Title)
	if err != nil {
		h.serviceError(w, r, "create playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDTO(p))
}

func (h *handlers) playlist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "playlist id")
	if !ok {
		return
	}
	p, ts, err := h.d.Library.Playlist(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlist": playlistDTO(p), "tracks": h.tracks(ts)})
}

func (h *handlers) renamePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "playlist id")
	if !ok {
		return
	}
	var b titleBody
	if !decode(w, r, &b) {
		return
	}
	p, err := h.d.Library.RenamePlaylist(r.Context(), userFrom(r.Context()), id, b.Title)
	if err != nil {
		h.serviceError(w, r, "rename playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDTO(p))
}

func (h *handlers) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "playlist id")
	if !ok {
		return
	}
	if err := h.d.Library.DeletePlaylist(r.Context(), userFrom(r.Context()), id); err != nil {
		h.serviceError(w, r, "delete playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) addTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "playlist id")
	if !ok {
		return
	}
	var b struct {
		TrackID string `json:"trackId"`
	}
	if !decode(w, r, &b) {
		return
	}
	tid, err := domain.ParseID(b.TrackID, "trackId")
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	if err := h.d.Library.AddToPlaylist(r.Context(), userFrom(r.Context()), id, tid); err != nil {
		h.serviceError(w, r, "add track", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) removeTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "playlist id")
	if !ok {
		return
	}
	tid, ok := pathID(w, r, "trackId", "track id")
	if !ok {
		return
	}
	if err := h.d.Library.RemoveFromPlaylist(r.Context(), userFrom(r.Context()), id, tid); err != nil {
		h.serviceError(w, r, "remove track", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReasonDTO is the optional explainability of a wave track (ADR 0010 §7).
type ReasonDTO struct {
	Kind       string `json:"kind"`
	RefTrackID string `json:"refTrackId,omitempty"`
	RefTitle   string `json:"refTitle,omitempty"`
	RefArtist  string `json:"refArtist,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// WaveTrackDTO is Track plus an optional reason.
type WaveTrackDTO struct {
	TrackDTO
	Reason *ReasonDTO `json:"reason,omitempty"`
}

// StrategyHeader tells which engine produced the batch (reco | fallback).
const StrategyHeader = "X-Wave-Strategy"

func (h *handlers) waveTracks(b domain.WaveBatch) []WaveTrackDTO {
	out := make([]WaveTrackDTO, 0, len(b.Tracks))
	for _, t := range b.Tracks {
		d := WaveTrackDTO{TrackDTO: h.track(t.Track)}
		if r := t.Reason; r != nil {
			d.Reason = &ReasonDTO{
				Kind: r.Kind, RefTrackID: r.RefTrackID, RefTitle: r.RefTitle, RefArtist: r.RefArtist,
				Artist: r.Artist, Genre: r.Genre, Tag: r.Tag,
			}
		}
		out = append(out, d)
	}
	return out
}

func (h *handlers) waveStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if r.ContentLength != 0 {
		dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
		if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
			return
		}
	}
	id, b, err := h.d.Wave.Start(r.Context(), userFrom(r.Context()), body.Mode)
	if err != nil {
		h.serviceError(w, r, "wave start", err)
		return
	}
	w.Header().Set(StrategyHeader, b.Strategy)
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": id.String(), "tracks": h.waveTracks(b), "strategy": b.Strategy, "mode": b.Mode})
}

func (h *handlers) waveNext(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "session id")
	if !ok {
		return
	}
	b, err := h.d.Wave.Next(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "wave next", err)
		return
	}
	w.Header().Set(StrategyHeader, b.Strategy)
	writeJSON(w, http.StatusOK, h.waveTracks(b))
}

func (h *handlers) waveFeedback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "session id")
	if !ok {
		return
	}
	var b struct {
		TrackID string `json:"trackId"`
		Action  string `json:"action"`
	}
	if !decode(w, r, &b) {
		return
	}
	tid, err := domain.ParseID(b.TrackID, "trackId")
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	if err := h.d.Wave.Feedback(r.Context(), userFrom(r.Context()), id, tid, b.Action); err != nil {
		h.serviceError(w, r, "wave feedback", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) listened(w http.ResponseWriter, r *http.Request) {
	var b struct {
		TrackID     string   `json:"trackId"`
		PositionSec *float64 `json:"positionSec"`
		Completed   bool     `json:"completed"`
	}
	if !decode(w, r, &b) {
		return
	}
	tid, err := domain.ParseID(b.TrackID, "trackId")
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	if b.PositionSec == nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "positionSec is required")
		return
	}
	if err := h.d.Events.TrackListened(r.Context(), userFrom(r.Context()), tid, *b.PositionSec, b.Completed); err != nil {
		h.serviceError(w, r, "track listened", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) skipped(w http.ResponseWriter, r *http.Request) {
	var b struct {
		TrackID     string   `json:"trackId"`
		PositionSec *float64 `json:"positionSec"`
	}
	if !decode(w, r, &b) {
		return
	}
	tid, err := domain.ParseID(b.TrackID, "trackId")
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	if b.PositionSec == nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "positionSec is required")
		return
	}
	if err := h.d.Events.TrackSkipped(r.Context(), userFrom(r.Context()), tid, *b.PositionSec); err != nil {
		h.serviceError(w, r, "track skipped", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) stream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "track id")
	if !ok {
		return
	}
	if _, err := h.d.Streamer.Signer().VerifyStream(id, r.URL.Query(), h.d.Now()); err != nil {
		writeError(w, http.StatusForbidden, CodeUnauthorized, signer.ErrBadSignature.Error())
		return
	}
	if err := h.d.Streamer.ServeStream(w, r, id); err != nil {
		h.serviceError(w, r, "stream", err)
	}
}

func (h *handlers) cover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	size, err := h.d.Streamer.Signer().VerifyCover(id, r.URL.Query())
	if err != nil {
		writeError(w, http.StatusForbidden, CodeUnauthorized, signer.ErrBadSignature.Error())
		return
	}
	if err := h.d.Streamer.ServeCover(w, r, id, size); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.serviceError(w, r, "cover", err)
	}
}
