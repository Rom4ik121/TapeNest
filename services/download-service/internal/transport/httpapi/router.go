// Package httpapi is the REST transport of download-service (chi). It is only
// reachable from api-gateway: every /api and /internal request must carry
// X-Internal-Token; the caller identity comes from the gateway's X-User-Id.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/service"
)

// Error codes (docs/api/download.openapi.yaml).
const (
	CodeInvalid         = "INVALID"
	CodeInvalidURL      = "INVALID_URL"
	CodeUnsupported     = "UNSUPPORTED_SOURCE"
	CodePlaylist        = "PLAYLIST_NOT_SUPPORTED"
	CodeForbiddenHost   = "FORBIDDEN_HOST"
	CodeQuotaActive     = "QUOTA_ACTIVE"
	CodeQuotaDaily      = "QUOTA_DAILY"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeNotFound        = "NOT_FOUND"
	CodeNotReady        = "NOT_READY"
	CodeNoPublicURL     = "PUBLIC_LINKS_DISABLED"
	CodeEditFailed      = "EDIT_FAILED"
	CodeEditUnavailable = "EDIT_UNAVAILABLE"
	CodeTimeline        = "TIMELINE_INVALID"
	CodeInternal        = "INTERNAL"
	CodeUnavailableDeps = "SERVICE_UNAVAILABLE"
)

// ErrorBody is the JSON error shape shared with api-gateway: { message, code }.
type ErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// Pinger checks a dependency (readyz).
type Pinger func(ctx context.Context) error

// Deps wires the router.
type Deps struct {
	API           *service.API
	Bus           Subscriber
	InternalToken string
	Log           *slog.Logger
	Ready         map[string]Pinger
	SSEMax        time.Duration // max SSE connection time (default 30m)
	SSEHeartbeat  time.Duration // default 15s
}

type ctxKey int

const ctxUser ctxKey = iota

var reqIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.SSEMax == 0 {
		d.SSEMax = 30 * time.Minute
	}
	if d.SSEHeartbeat == 0 {
		d.SSEHeartbeat = 15 * time.Second
	}
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, h.requestLog)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.readyz)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Post("/internal/v1/downloads", h.createInternal)
		r.Route("/api/v1/downloads", func(r chi.Router) {
			r.Use(h.requireUser)
			r.Post("/", h.create)
			r.Post("/compose", h.compose)
			r.Get("/", h.list)
			r.Get("/{id}", h.get)
			r.Patch("/{id}", h.rename)
			r.Delete("/{id}", h.remove)
			r.Post("/{id}/trim", h.trim)
			r.Get("/{id}/events", h.events)
			r.Get("/{id}/file", h.file)
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
	writeJSON(w, status, ErrorBody{Message: msg, Code: code})
}

func (h *handlers) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Internal-Token")
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(h.d.InternalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get("X-User-Id"))
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "missing user identity")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, id)))
	})
}

func userFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(ctxUser).(uuid.UUID)
	return id
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (s *recorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush (SSE).
func (s *recorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (h *handlers) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if !reqIDPattern.MatchString(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		h.d.Log.InfoContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(), "request_id", id)
	})
}

func (h *handlers) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
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
