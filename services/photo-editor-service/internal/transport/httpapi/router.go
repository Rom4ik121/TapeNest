// Package httpapi is the REST transport of photo-editor-service.
// It is only reachable from api-gateway: X-Internal-Token plus X-User-Id.
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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
	"github.com/tapenest/tapenest/services/photo-editor-service/internal/service"
)

const (
	codeInvalid  = "INVALID"
	codeUnauth   = "UNAUTHORIZED"
	codeNotFound = "NOT_FOUND"
	codeTooLarge = "TOO_LARGE"
	codeInternal = "INTERNAL"
)

// Pinger checks a dependency.
type Pinger func(ctx context.Context) error

// Deps wires the router.
type Deps struct {
	API           *service.Service
	InternalToken string
	MaxBytes      int64
	Log           *slog.Logger
	Ready         map[string]Pinger
}

type ctxKey int

const ctxUser ctxKey = 1

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.MaxBytes <= 0 {
		d.MaxBytes = 15 << 20
	}
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.ready)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Route("/api/v1/photos", func(r chi.Router) {
			r.Use(h.requireUser)
			r.Post("/", h.upload)
			r.Get("/", h.list)
			r.Get("/exports/{id}/file", h.exportFile)
			r.Get("/{id}", h.get)
			r.Delete("/{id}", h.delete)
			r.Get("/{id}/file", h.file)
			r.Post("/{id}/exports", h.export)
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
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeInvalid, err.Error())
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

type photoDTO struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	MimeType  string    `json:"mimeType"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}

func toPhoto(p domain.Photo) photoDTO {
	return photoDTO{
		ID: p.ID.String(), Title: p.Title, Width: p.Width, Height: p.Height,
		MimeType: p.MimeType, SizeBytes: p.SizeBytes, CreatedAt: p.CreatedAt,
	}
}

func (h *handlers) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.d.MaxBytes+2048)
	if err := r.ParseMultipartForm(h.d.MaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge, "file is too large")
			return
		}
		writeError(w, http.StatusBadRequest, codeInvalid, "expected a multipart file")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "file is required")
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, h.d.MaxBytes+1))
	if err != nil {
		h.fail(w, err)
		return
	}
	p, err := h.d.API.Upload(r.Context(), userFrom(r.Context()), r.FormValue("title"), data)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPhoto(p))
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, errors.New("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, u, nil
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	var after time.Time
	var afterID uuid.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		t, id, err := decodeCursor(c)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalid, "invalid cursor")
			return
		}
		after, afterID = t, id
	}
	items, err := h.d.API.List(r.Context(), userFrom(r.Context()), after, afterID, 25)
	if err != nil {
		h.fail(w, err)
		return
	}
	var next *string
	if len(items) > 24 {
		items = items[:24]
		last := items[len(items)-1]
		c := encodeCursor(last.CreatedAt, last.ID)
		next = &c
	}
	out := make([]photoDTO, 0, len(items))
	for _, p := range items {
		out = append(out, toPhoto(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := h.d.API.Get(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPhoto(p))
}

func (h *handlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.d.API.Delete(r.Context(), userFrom(r.Context()), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	h.sendURL(w, r, u)
}

func (h *handlers) export(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Recipe domain.Recipe `json:"recipe"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalid, "invalid json")
		return
	}
	e, u, err := h.d.API.Export(r.Context(), userFrom(r.Context()), id, body.Recipe)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": e.ID.String(), "url": u, "width": e.Width, "height": e.Height, "mimeType": e.MimeType,
	})
}

func (h *handlers) exportFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.d.API.ExportURL(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.sendURL(w, r, u)
}

func (h *handlers) sendURL(w http.ResponseWriter, r *http.Request, u string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("redirect") == "false" {
		writeJSON(w, http.StatusOK, map[string]string{"url": u})
		return
	}
	http.Redirect(w, r, u, http.StatusFound) //nolint:gosec // u is our own presigned URL
}
