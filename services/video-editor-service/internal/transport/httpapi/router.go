// Package httpapi is the REST transport of video-editor-service.
// It is only reachable from api-gateway: X-Internal-Token plus X-User-Id.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/video-editor-service/internal/service"
)

const (
	codeInvalid   = "INVALID"
	codeUnauth    = "UNAUTHORIZED"
	codeNotFound  = "NOT_FOUND"
	codeNotReady  = "NOT_READY"
	codeBusy      = "EXPORT_IN_PROGRESS"
	codeTooLarge  = "TOO_LARGE"
	codeInternal  = "INTERNAL"
	maxMusicBytes = 20 << 20
)

// Pinger checks a dependency.
type Pinger func(ctx context.Context) error

// Deps wires the router.
type Deps struct {
	API           *service.Service
	InternalToken string
	Log           *slog.Logger
	Ready         map[string]Pinger
}

type ctxKey int

const ctxUser ctxKey = 1

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.ready)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Route("/api/v1/video", func(r chi.Router) {
			r.Use(h.requireUser)
			r.Post("/projects", h.open)
			r.Get("/projects/{id}", h.project)
			r.Put("/projects/{id}", h.save)
			r.Post("/projects/{id}/music", h.music)
			r.Delete("/projects/{id}/music", h.clearMusic)
			r.Post("/projects/{id}/exports", h.export)
			r.Get("/exports/{id}", h.exportOf)
			r.Get("/exports/{id}/events", h.events)
			r.Get("/exports/{id}/file", h.file)
		})
	})
	return r
}

type handlers struct{ d Deps }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"message": msg, "code": code})
}

func (h *handlers) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Internal-Token")
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(h.d.InternalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, codeUnauth, "invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get("X-User-Id"))
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusUnauthorized, codeUnauth, "missing user identity")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, id)))
	})
}

func userFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(ctxUser).(uuid.UUID)
	return id
}

func (h *handlers) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]string{}
	ok := true
	for name, ping := range h.d.Ready {
		if err := ping(ctx); err != nil {
			out[name], ok = "down", false
			continue
		}
		out[name] = "ok"
	}
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found")
	case errors.Is(err, domain.ErrNotReady):
		writeError(w, http.StatusConflict, codeNotReady, "download is not finished")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeInvalid, err.Error())
	case errors.Is(err, domain.ErrBusy):
		writeError(w, http.StatusConflict, codeBusy, "an export is already running")
	case errors.Is(err, domain.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge, "file is too large")
	default:
		h.d.Log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, codeNotFound, "not found")
		return uuid.Nil, false
	}
	return id, true
}

type projectDTO struct {
	ID          string        `json:"id"`
	SourceJobID string        `json:"sourceJobId"`
	Title       string        `json:"title"`
	DurationSec float64       `json:"durationSec"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	Recipe      domain.Recipe `json:"recipe"`
	HasMusic    bool          `json:"hasMusic"`
	Export      *exportDTO    `json:"export,omitempty"`
}

type exportDTO struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func toProject(p domain.Project) projectDTO {
	d := projectDTO{
		ID: p.ID.String(), SourceJobID: p.SourceJobID.String(), Title: p.Title,
		DurationSec: p.Duration, Width: p.Width, Height: p.Height, Recipe: p.Recipe, HasMusic: p.MusicKey != "",
	}
	if p.Latest != nil {
		e := toExport(*p.Latest)
		d.Export = &e
	}
	return d
}

func toExport(e domain.Export) exportDTO {
	return exportDTO{ID: e.ID.String(), Status: string(e.Status), Error: e.Error, CreatedAt: e.CreatedAt}
}

func (h *handlers) open(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceJobID string `json:"sourceJobId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "invalid JSON body")
		return
	}
	job, err := uuid.Parse(body.SourceJobID)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "sourceJobId must be a UUID")
		return
	}
	p, err := h.d.API.Open(r.Context(), userFrom(r.Context()), job)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProject(p))
}

func (h *handlers) project(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := h.d.API.Project(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProject(p))
}

func (h *handlers) save(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Recipe domain.Recipe `json:"recipe"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "invalid JSON body")
		return
	}
	p, err := h.d.API.UpdateRecipe(r.Context(), userFrom(r.Context()), id, body.Recipe)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProject(p))
}

func (h *handlers) music(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMusicBytes+4096)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "expected an audio file")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "file field is required")
		return
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxMusicBytes+1))
	if err != nil {
		h.fail(w, err)
		return
	}
	if int64(len(body)) > maxMusicBytes {
		writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge, "music bed must be at most 20 MB")
		return
	}
	if !audioMagic(body) {
		writeError(w, http.StatusBadRequest, codeInvalid, "music bed must be mp3, m4a, wav, ogg or flac")
		return
	}
	_ = hdr
	p, err := h.d.API.AttachMusic(r.Context(), userFrom(r.Context()), id, bytesReader(body), int64(len(body)))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProject(p))
}

func (h *handlers) clearMusic(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := h.d.API.ClearMusic(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProject(p))
}

func (h *handlers) export(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := h.d.API.Export(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toExport(e))
}

func (h *handlers) exportOf(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := h.d.API.ExportOf(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toExport(e))
}

func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, codeInternal, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		e, err := h.d.API.ExportOf(r.Context(), userFrom(r.Context()), id)
		if err != nil {
			h.fail(w, err)
			return
		}
		raw, _ := json.Marshal(toExport(e))
		_, _ = w.Write([]byte("event: export\ndata: " + string(raw) + "\n\n"))
		fl.Flush()
		if e.Status.Terminal() {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (h *handlers) file(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.d.API.FileURL(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("redirect") == "false" {
		writeJSON(w, http.StatusOK, map[string]string{"url": u})
		return
	}
	http.Redirect(w, r, u, http.StatusFound) //nolint:gosec // u is our presigned URL
}

func audioMagic(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	switch {
	case string(b[:3]) == "ID3":
		return true
	case b[0] == 0xff && (b[1]&0xe0) == 0xe0:
		return true
	case string(b[:4]) == "OggS":
		return true
	case string(b[:4]) == "fLaC":
		return true
	case string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return true
	case string(b[4:8]) == "ftyp":
		return true
	default:
		return false
	}
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct{ b []byte }

func (s *sliceReader) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}
