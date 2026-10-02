// Package httpapi is the streaming-service HTTP API.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/streaming-service/internal/catalog"
	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
	"github.com/tapenest/tapenest/services/streaming-service/internal/hls"
	"github.com/tapenest/tapenest/services/streaming-service/internal/preview"
	"github.com/tapenest/tapenest/services/streaming-service/internal/repo"
	"github.com/tapenest/tapenest/services/streaming-service/internal/service"
	"github.com/tapenest/tapenest/services/streaming-service/internal/sign"
)

// Pinger is a readiness check.
type Pinger func(context.Context) error

// Deps are router dependencies.
type Deps struct {
	Log           *slog.Logger
	Cinema        *service.Cinema
	Store         *repo.Store
	Sign          *sign.Signer
	Preview       *preview.Library
	InternalToken string
	Ready         map[string]Pinger
	Now           func() time.Time
}

type handlers struct{ d Deps }

func (h *handlers) now() time.Time {
	if h.d.Now != nil {
		return h.d.Now()
	}
	return time.Now()
}

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.ready)
	r.Get("/hls/{id}/index.m3u8", h.playlist)
	r.Get("/hls/{id}/{seg}", h.segment)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Group(func(r chi.Router) {
			r.Use(h.requireUser)
			r.Get("/api/v1/cinema/titles", h.titles)
			r.Get("/api/v1/cinema/titles/{id}", h.title)
			r.Put("/api/v1/cinema/titles/{id}/watchlist", h.watch(true))
			r.Delete("/api/v1/cinema/titles/{id}/watchlist", h.watch(false))
			r.Get("/api/v1/cinema/titles/{id}/position", h.getPosition)
			r.Put("/api/v1/cinema/titles/{id}/position", h.putPosition)
			r.Get("/api/v1/cinema/continue", h.cont)
			r.Get("/api/v1/cinema/watchlist", h.watchlist)
			r.Post("/api/v1/cinema/streams", h.startStream)
			r.Get("/api/v1/cinema/streams/{id}", h.getStream)
			r.Delete("/api/v1/cinema/streams/{id}", h.stopStream)
			r.Group(func(r chi.Router) {
				r.Use(h.requireAdmin)
				r.Post("/api/v1/cinema/admin/titles", h.adminAdd)
				r.Get("/api/v1/cinema/admin/audit", h.adminAudit)
			})
		})
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
	})
	return r
}

func (h *handlers) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for name, p := range h.d.Ready {
		if err := p(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "dependency": name})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

type ctxKey struct{}

func (h *handlers) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get("X-User-Id"))
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "user required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func userFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(ctxKey{}).(uuid.UUID)
	return id
}

func (h *handlers) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Role") != "admin" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) titles(w http.ResponseWriter, r *http.Request) {
	q, kind, after, limit, ok := h.pageQuery(w, r, true)
	if !ok {
		return
	}
	items, err := h.d.Store.Search(r.Context(), userFrom(r.Context()), q, kind, after, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.writeTitlePage(w, items, limit, func(s domain.Summary) string { return encodeSort(s.Sort) })
}

func (h *handlers) title(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, "id", r)
	if !ok {
		return
	}
	t, err := h.d.Store.Get(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, titleDTO(t))
}

func (h *handlers) watch(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, "id", r)
		if !ok {
			return
		}
		if err := h.d.Store.SetWatch(r.Context(), userFrom(r.Context()), id, on); err != nil {
			h.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handlers) watchlist(w http.ResponseWriter, r *http.Request) {
	_, _, _, limit, ok := h.pageQuery(w, r, false)
	if !ok {
		return
	}
	after, afterID, has, ok := h.timeCursor(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.Watchlist(r.Context(), userFrom(r.Context()), after, afterID, has, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	var next any
	if len(items) > limit {
		last := items[limit-1]
		next = encodeTime(last.WatchedAt, last.ID)
		items = items[:limit]
	}
	out := make([]summaryDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toSummary(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (h *handlers) getPosition(w http.ResponseWriter, r *http.Request) {
	titleID, ok := pathID(w, "id", r)
	if !ok {
		return
	}
	fileID, err := uuid.Parse(r.URL.Query().Get("fileId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "fileId is required")
		return
	}
	p, err := h.d.Store.GetPosition(r.Context(), userFrom(r.Context()), titleID, fileID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, posDTO(p))
}

func (h *handlers) putPosition(w http.ResponseWriter, r *http.Request) {
	titleID, ok := pathID(w, "id", r)
	if !ok {
		return
	}
	var body struct {
		FileID      uuid.UUID `json:"fileId"`
		PositionSec float64   `json:"positionSec"`
		DurationSec float64   `json:"durationSec"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.FileID == uuid.Nil || body.PositionSec < 0 || body.DurationSec < 0 {
		writeError(w, http.StatusBadRequest, "INVALID", "fileId, positionSec and durationSec are required")
		return
	}
	err := h.d.Store.PutPosition(r.Context(), userFrom(r.Context()), domain.Position{
		TitleID: titleID, FileID: body.FileID, PositionSec: body.PositionSec, DurationSec: body.DurationSec,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) cont(w http.ResponseWriter, r *http.Request) {
	_, _, _, limit, ok := h.pageQuery(w, r, false)
	if !ok {
		return
	}
	after, afterID, has, ok := h.timeCursor(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.Continue(r.Context(), userFrom(r.Context()), after, afterID, has, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	var next any
	if len(items) > limit {
		last := items[limit-1]
		next = encodeTime(last.Position.UpdatedAt, last.File.ID)
		items = items[:limit]
	}
	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"title":    toSummary(it.Title),
			"file":     toFile(it.File),
			"position": posDTO(it.Position),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (h *handlers) startStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TitleID uuid.UUID `json:"titleId"`
		FileID  uuid.UUID `json:"fileId"`
	}
	if !decode(w, r, &body) {
		return
	}
	sess, err := h.d.Cinema.Start(r.Context(), userFrom(r.Context()), body.TitleID, body.FileID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.streamDTO(sess))
}

func (h *handlers) getStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, "id", r)
	if !ok {
		return
	}
	sess, err := h.d.Store.GetSession(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	_ = h.d.Store.TouchSession(r.Context(), id)
	writeJSON(w, http.StatusOK, h.streamDTO(sess))
}

func (h *handlers) stopStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, "id", r)
	if !ok {
		return
	}
	if err := h.d.Store.StopSession(r.Context(), userFrom(r.Context()), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) adminAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string   `json:"title"`
		OriginalTitle *string  `json:"originalTitle"`
		Kind          string   `json:"kind"`
		Year          *int     `json:"year"`
		Rating        *float64 `json:"rating"`
		Genres        []string `json:"genres"`
		Description   string   `json:"description"`
		RuntimeMin    *int     `json:"runtimeMin"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" || (body.Kind != "movie" && body.Kind != "series") {
		writeError(w, http.StatusBadRequest, "INVALID", "title and kind are required")
		return
	}
	if body.Genres == nil {
		body.Genres = []string{}
	}
	dur := 0.0
	if body.RuntimeMin != nil {
		dur = float64(*body.RuntimeMin * 60)
	}
	t := domain.Title{
		Summary: domain.Summary{
			ID: uuid.New(), Kind: domain.TitleKind(body.Kind), Title: strings.TrimSpace(body.Title),
			OriginalTitle: body.OriginalTitle, Year: body.Year, Rating: body.Rating, Genres: body.Genres,
		},
		Description: body.Description,
		RuntimeMin:  body.RuntimeMin,
		Files: []domain.File{{
			ID: uuid.New(), Name: "local.1080p.mkv", Quality: "1080p", SizeBytes: 0, DurationSec: &dur,
		}},
	}
	if err := h.d.Store.AddTitle(r.Context(), t, userFrom(r.Context())); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": t.ID.String()})
}

func (h *handlers) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "INVALID", "bad limit")
			return
		}
		limit = n
	}
	rows, err := h.d.Store.ListAudit(r.Context(), limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	if rows == nil {
		rows = []domain.Audit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *handlers) playlist(w http.ResponseWriter, r *http.Request) {
	id, sess, ok := h.hlsSession(w, r)
	if !ok {
		return
	}
	view := h.d.Cinema.ViewSession(sess, sign.Token{})
	if view.Status != "ready" {
		writeError(w, http.StatusConflict, "NOT_READY", "stream is not ready")
		return
	}
	raw, err := h.d.Preview.Playlist()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "preview unavailable")
		return
	}
	q := r.URL.RawQuery
	body := hls.Rewrite(raw, id.String(), q)
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, body) //nolint:gosec // G705: body is an HLS playlist, not HTML
}

func (h *handlers) segment(w http.ResponseWriter, r *http.Request) {
	_, _, ok := h.hlsSession(w, r)
	if !ok {
		return
	}
	f, err := h.d.Preview.OpenSegment(chi.URLParam(r, "seg"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "segment not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeContent(w, r, f.Name(), h.now(), f)
}

func (h *handlers) hlsSession(w http.ResponseWriter, r *http.Request) (uuid.UUID, domain.Session, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return uuid.Nil, domain.Session{}, false
	}
	exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err := h.d.Sign.Valid(id.String(), exp, r.URL.Query().Get("sig"), h.now()); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "bad stream token")
		return uuid.Nil, domain.Session{}, false
	}
	// Playlist is authorized by the signature, which is bound to the session id.
	// The session row is loaded without a user so <video> on iOS (no headers) still works.
	sess, err := h.d.Store.GetSessionAny(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return uuid.Nil, domain.Session{}, false
	}
	return id, sess, true
}

func (h *handlers) streamDTO(s domain.Session) map[string]any {
	tok := h.d.Sign.Sign(s.ID.String(), h.now())
	v := h.d.Cinema.ViewSession(s, tok)
	var hlsURL any
	if v.HLSPath != "" {
		hlsURL = v.HLSPath
	}
	var err any
	if v.Error != "" {
		err = v.Error
	}
	return map[string]any{
		"id": s.ID.String(), "titleId": s.TitleID.String(), "fileId": s.FileID.String(),
		"status": v.Status, "bufferedPct": v.BufferedPct, "peers": v.Peers, "speedBps": v.SpeedBps,
		"hlsUrl": hlsURL, "error": err,
	}
}

func (h *handlers) writeTitlePage(w http.ResponseWriter, items []domain.Summary, limit int, cursor func(domain.Summary) string) {
	var next any
	if len(items) > limit {
		if c := cursor(items[limit-1]); c != "" {
			next = c
		}
		items = items[:limit]
	}
	out := make([]summaryDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toSummary(it))
	}
	if next == "" {
		next = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (h *handlers) pageQuery(w http.ResponseWriter, r *http.Request, withSearch bool) (q, kind string, after, limit int, ok bool) {
	limit = 24
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			writeError(w, http.StatusBadRequest, "INVALID", "bad limit")
			return "", "", 0, 0, false
		}
		limit = n
	}
	if withSearch {
		q = strings.TrimSpace(r.URL.Query().Get("q"))
		if len([]rune(q)) > 100 {
			writeError(w, http.StatusBadRequest, "INVALID", "q is too long")
			return "", "", 0, 0, false
		}
		kind = r.URL.Query().Get("kind")
		if kind != "" && kind != "movie" && kind != "series" {
			writeError(w, http.StatusBadRequest, "INVALID", "bad kind")
			return "", "", 0, 0, false
		}
		n, err := decodeSort(r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID", "bad cursor")
			return "", "", 0, 0, false
		}
		after = n
	}
	return q, kind, after, limit, true
}

func (h *handlers) timeCursor(w http.ResponseWriter, r *http.Request) (time.Time, uuid.UUID, bool, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return time.Time{}, uuid.Nil, false, true
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "bad cursor")
		return time.Time{}, uuid.Nil, false, false
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "INVALID", "bad cursor")
		return time.Time{}, uuid.Nil, false, false
	}
	nsec, err := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := uuid.Parse(parts[1])
	if err != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "bad cursor")
		return time.Time{}, uuid.Nil, false, false
	}
	return time.Unix(0, nsec).UTC(), id, true, true
}

func (h *handlers) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, service.ErrUnavailable):
		w.Header().Set("X-Degraded-Dependency", "torrserver")
		w.Header().Set("Retry-After", "15")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"message": "torrserver is unavailable", "code": "SERVICE_UNAVAILABLE", "service": "streaming",
		})
	default:
		if h.d.Log != nil {
			h.d.Log.Error("cinema", "err", err)
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
	}
}

func pathID(w http.ResponseWriter, name string, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "bad id")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"message": msg, "code": code})
}

type summaryDTO struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	OriginalTitle *string  `json:"originalTitle"`
	Year          *int     `json:"year"`
	PosterURL     string   `json:"posterUrl"`
	Rating        *float64 `json:"rating"`
	Genres        []string `json:"genres"`
	InWatchlist   bool     `json:"inWatchlist"`
}

func toSummary(s domain.Summary) summaryDTO {
	g := s.Genres
	if g == nil {
		g = []string{}
	}
	return summaryDTO{
		ID: s.ID.String(), Kind: string(s.Kind), Title: s.Title, OriginalTitle: s.OriginalTitle,
		Year: s.Year, PosterURL: catalog.PosterDataURL(s.ID.String(), str(s.OriginalTitle, s.Title)),
		Rating: s.Rating, Genres: g, InWatchlist: s.InWatchlist,
	}
}

func titleDTO(t domain.Title) map[string]any {
	s := toSummary(t.Summary)
	files := make([]fileDTO, 0, len(t.Files))
	for _, f := range t.Files {
		files = append(files, toFile(f))
	}
	back := catalog.BackdropDataURL(t.ID.String())
	return map[string]any{
		"id": s.ID, "kind": s.Kind, "title": s.Title, "originalTitle": s.OriginalTitle,
		"year": s.Year, "posterUrl": s.PosterURL, "rating": s.Rating, "genres": s.Genres,
		"inWatchlist": s.InWatchlist, "description": t.Description, "backdropUrl": back,
		"runtimeMin": t.RuntimeMin, "files": files,
	}
}

type fileDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Season      *int     `json:"season"`
	Episode     *int     `json:"episode"`
	Quality     string   `json:"quality"`
	SizeBytes   int64    `json:"sizeBytes"`
	DurationSec *float64 `json:"durationSec"`
}

func toFile(f domain.File) fileDTO {
	return fileDTO{ID: f.ID.String(), Name: f.Name, Season: f.Season, Episode: f.Episode, Quality: f.Quality, SizeBytes: f.SizeBytes, DurationSec: f.DurationSec}
}

func posDTO(p domain.Position) map[string]any {
	return map[string]any{
		"titleId": p.TitleID.String(), "fileId": p.FileID.String(),
		"positionSec": p.PositionSec, "durationSec": p.DurationSec,
		"updatedAt": p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func str(p *string, fallback string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fallback
}

func encodeSort(n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(n)))
}

func decodeSort(s string) (int, error) {
	if s == "" {
		return -1, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(b))
}

func encodeTime(t time.Time, id uuid.UUID) string {
	raw := strconv.FormatInt(t.UTC().UnixNano(), 10) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}
