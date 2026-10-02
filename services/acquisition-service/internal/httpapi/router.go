// Package httpapi is acquisition-service's internal HTTP API
// (docs/api/acquisition.openapi.yaml). Only music-service calls it.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/match"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/repo/db"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/streamer"
)

// AdminStore is the read side for admin endpoints.
type AdminStore interface {
	ListRequests(ctx context.Context, arg db.ListRequestsParams) ([]db.AcquisitionRequest, error)
	RequestByID(ctx context.Context, id uuid.UUID) (db.AcquisitionRequest, error)
	RequestTracks(ctx context.Context, id uuid.UUID) ([]db.AcquisitionRequestTrack, error)
	RequestUsers(ctx context.Context, id uuid.UUID) ([]db.RequestUsersRow, error)
	RequestEvents(ctx context.Context, id uuid.UUID) ([]db.RequestEventsRow, error)
	CountByState(ctx context.Context) ([]db.CountByStateRow, error)
	Ping(ctx context.Context) error
}

// Health is an optional dependency check (name → ping).
type Health map[string]func(ctx context.Context) error

// StatusInfo supplies the admin status extras.
type StatusInfo interface {
	IndexerCount(ctx context.Context) (enabled int, err error)
	Disk(ctx context.Context) (core.DiskStats, error)
}

// Limits are reported by the admin status endpoint.
type Limits struct {
	ContentSources string  `json:"contentSources"`
	TorrentMaxGB   float64 `json:"torrentMaxGb"`
	LibraryMaxGB   float64 `json:"libraryMaxGb"`
	MaxActive      int     `json:"maxActive"`
	UserDaily      int     `json:"userDaily"`
	UserActive     int     `json:"userActive"`
	SeedRatio      float64 `json:"seedRatio"`
	SeedMinutes    int     `json:"seedMinutes"`
	MaxReleaseGB   float64 `json:"maxReleaseGb"`
}

// Deps wires the router.
type Deps struct {
	Service       *core.Service
	Admin         AdminStore
	Streamer      *streamer.Streamer
	Required      Health // readiness-critical (postgres, redis)
	Optional      Health // reported only (lidarr, prowlarr, qbittorrent)
	Info          StatusInfo
	Limits        Limits
	MusicDir      string
	InternalToken string
	Log           *slog.Logger
}

// ErrorBody is the JSON error shape.
type ErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type handlers struct{ d Deps }

// NewRouter builds the handler.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Use(h.logRequests)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.readyz)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Get("/internal/v1/search", h.search)
		r.Get("/internal/v1/albums/{rg}", h.album)
		r.Get("/internal/v1/artists/{id}", h.artist)
		r.Post("/internal/v1/acquisitions", h.acquire)
		r.Get("/internal/v1/recordings/{id}/status", h.recordingStatus)
		r.Get("/internal/v1/recordings/{id}/stream", h.stream)
		r.Head("/internal/v1/recordings/{id}/stream", h.stream)
		r.Get("/internal/v1/admin/acquisitions", h.adminList)
		r.Get("/internal/v1/admin/acquisitions/{id}", h.adminGet)
		r.Get("/internal/v1/admin/status", h.adminStatus)
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
	})
	return r
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// Flush keeps streaming responses flushable through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs method, route pattern and status — never query strings.
func (h *handlers) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || h.d.Log == nil {
			return
		}
		h.d.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorBody{Message: msg, Code: code})
}

func (h *handlers) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Internal-Token")
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(h.d.InternalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "internal token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) serviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "DISABLED", "acquisition is disabled")
	case errors.Is(err, core.ErrQuota):
		writeError(w, http.StatusTooManyRequests, "QUOTA", "acquisition quota exceeded")
	case errors.Is(err, core.ErrStorageFull):
		writeError(w, http.StatusInsufficientStorage, "STORAGE_FULL", "library storage limit reached")
	case errors.Is(err, core.ErrUnknownAlbum), errors.Is(err, repo.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, core.ErrInvalid):
		writeError(w, http.StatusBadRequest, "INVALID", "invalid request")
	case errors.Is(err, streamer.ErrRange):
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "RANGE", "range not satisfiable")
	case errors.Is(err, streamer.ErrNotReady), errors.Is(err, streamer.ErrStalled):
		writeError(w, http.StatusServiceUnavailable, "NOT_READY", "not buffered yet")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "UPSTREAM_TIMEOUT", "upstream timeout")
	default:
		if h.d.Log != nil {
			h.d.Log.Error("request failed", "err", redact.Error(err))
		}
		writeError(w, http.StatusBadGateway, "UPSTREAM", "upstream error")
	}
}

func (h *handlers) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out := map[string]string{}
	status := http.StatusOK
	for name, ping := range h.d.Required {
		if ping(ctx) != nil {
			out[name], status = "down", http.StatusServiceUnavailable
		} else {
			out[name] = "ok"
		}
	}
	for name, ping := range h.d.Optional {
		if ping(ctx) != nil {
			out[name] = "down"
		} else {
			out[name] = "ok"
		}
	}
	writeJSON(w, status, out)
}

func pathUUID(r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	return id, err == nil
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		writeError(w, http.StatusBadRequest, "INVALID", "query too long")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 10
	}
	res, err := h.d.Service.Search(r.Context(), q, limit)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) album(w http.ResponseWriter, r *http.Request) {
	rg, ok := pathUUID(r, "rg")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid id")
		return
	}
	a, err := h.d.Service.Album(r.Context(), rg)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *handlers) artist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid id")
		return
	}
	a, err := h.d.Service.Artist(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// AcquireRequest is the body of POST /internal/v1/acquisitions.
type AcquireRequest struct {
	UserID           uuid.UUID `json:"userId"`
	ReleaseGroupMBID uuid.UUID `json:"releaseGroupMbid"`
	RecordingMBID    uuid.UUID `json:"recordingMbid"`
	Title            string    `json:"title"`
	Reason           string    `json:"reason"`
}

func (h *handlers) acquire(w http.ResponseWriter, r *http.Request) {
	var in AcquireRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid body")
		return
	}
	if len(in.Title) > 300 {
		in.Title = in.Title[:300]
	}
	st, err := h.d.Service.Acquire(r.Context(), core.AcquireInput{
		UserID: in.UserID, ReleaseGroup: in.ReleaseGroupMBID, Recording: in.RecordingMBID, Title: in.Title, Reason: in.Reason,
	})
	if err != nil {
		h.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handlers) recordingStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid id")
		return
	}
	st, _, err := h.d.Service.RecordingStatus(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// stream serves a recording: from the library once imported, otherwise from
// the partially downloaded torrent file (Range-aware, waits for pieces).
func (h *handlers) stream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid id")
		return
	}
	_, row, err := h.d.Service.RecordingStatus(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	if row.LibraryPath != "" {
		p := filepath.Join(h.d.MusicDir, filepath.FromSlash(row.LibraryPath))
		if f, err := os.Open(p); err == nil { //nolint:gosec // p is MUSIC_DIR joined with a relative library path
			defer func() { _ = f.Close() }()
			if st, err := f.Stat(); err == nil {
				w.Header().Set("Content-Type", match.ContentType(p))
				http.ServeContent(w, r, "", st.ModTime(), f)
				return
			}
		}
	}
	if row.TorrentHash == "" || row.FileIndex == nil {
		writeError(w, http.StatusServiceUnavailable, "NOT_READY", "not located yet")
		return
	}
	if err := h.d.Streamer.Serve(w, r, row.TorrentHash, int(*row.FileIndex)); err != nil {
		h.serviceError(w, err)
	}
}

// RequestDTO is an acquisition as shown to admins.
type RequestDTO struct {
	ID               uuid.UUID  `json:"id"`
	ReleaseGroupMBID uuid.UUID  `json:"releaseGroupMbid"`
	Artist           string     `json:"artist"`
	Album            string     `json:"album"`
	Year             int32      `json:"year"`
	State            string     `json:"state"`
	ErrorCode        string     `json:"errorCode,omitempty"`
	Priority         int32      `json:"priority"`
	Progress         float32    `json:"progress"`
	Release          string     `json:"release,omitempty"`
	Indexer          string     `json:"indexer,omitempty"`
	Quality          string     `json:"quality,omitempty"`
	SizeBytes        int64      `json:"sizeBytes"`
	Seeders          int32      `json:"seeders"`
	ImportMode       string     `json:"importMode,omitempty"`
	Attempts         int32      `json:"attempts"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
}

func toDTO(r db.AcquisitionRequest) RequestDTO {
	return RequestDTO{
		ID: r.ID, ReleaseGroupMBID: r.ReleaseGroupMbid, Artist: r.ArtistName, Album: r.AlbumTitle, Year: r.Year,
		State: r.State, ErrorCode: r.ErrorCode, Priority: r.Priority, Progress: r.Progress, Release: r.ReleaseTitle,
		Indexer: r.Indexer, Quality: r.Quality, SizeBytes: r.SizeBytes, Seeders: r.Seeders, ImportMode: r.ImportMode,
		Attempts: r.Attempts, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CompletedAt: r.CompletedAt,
	}
}

func (h *handlers) adminList(w http.ResponseWriter, r *http.Request) {
	p := db.ListRequestsParams{Lim: 100}
	if s := r.URL.Query().Get("state"); s != "" {
		p.State = &s
	}
	rows, err := h.d.Admin.ListRequests(r.Context(), p)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	out := make([]RequestDTO, 0, len(rows))
	for _, x := range rows {
		out = append(out, toDTO(x))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handlers) adminGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid id")
		return
	}
	req, err := h.d.Admin.RequestByID(r.Context(), id)
	if err != nil {
		h.serviceError(w, repo.NotFound(err))
		return
	}
	tracks, err1 := h.d.Admin.RequestTracks(r.Context(), id)
	users, err2 := h.d.Admin.RequestUsers(r.Context(), id)
	events, err3 := h.d.Admin.RequestEvents(r.Context(), id)
	if err := errors.Join(err1, err2, err3); err != nil {
		h.serviceError(w, err)
		return
	}
	type trackDTO struct {
		RecordingMBID uuid.UUID `json:"recordingMbid"`
		Disc          int32     `json:"disc"`
		Position      int32     `json:"position"`
		Title         string    `json:"title"`
		File          string    `json:"file,omitempty"`
		SizeBytes     int64     `json:"sizeBytes"`
		LibraryPath   string    `json:"libraryPath,omitempty"`
		Wanted        bool      `json:"wanted"`
	}
	type eventDTO struct {
		Kind   string          `json:"kind"`
		At     time.Time       `json:"at"`
		Detail json.RawMessage `json:"detail"`
	}
	td := make([]trackDTO, 0, len(tracks))
	for _, t := range tracks {
		td = append(td, trackDTO{t.RecordingMbid, t.Disc, t.Position, t.Title, t.FileName, t.FileSize, t.LibraryPath, t.WantedAt != nil})
	}
	ed := make([]eventDTO, 0, len(events))
	for _, e := range events {
		ed = append(ed, eventDTO{e.Kind, e.At, json.RawMessage(e.Detail)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": toDTO(req), "tracks": td, "users": len(users), "events": ed})
}

func (h *handlers) adminStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	services := map[string]string{}
	for name, ping := range h.d.Optional {
		if ping(ctx) != nil {
			services[name] = "down"
		} else {
			services[name] = "ok"
		}
	}
	counts := map[string]int64{}
	if rows, err := h.d.Admin.CountByState(ctx); err == nil {
		for _, c := range rows {
			counts[c.State] = c.N
		}
	}
	out := map[string]any{"services": services, "requests": counts, "limits": h.d.Limits, "enabled": h.d.Limits.ContentSources == "p2p"}
	if h.d.Info != nil {
		if n, err := h.d.Info.IndexerCount(ctx); err == nil {
			out["indexers"] = n
		}
		if d, err := h.d.Info.Disk(ctx); err == nil {
			out["disk"] = d
		}
	}
	writeJSON(w, http.StatusOK, out)
}
