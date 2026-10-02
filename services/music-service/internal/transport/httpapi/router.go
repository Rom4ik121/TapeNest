// Package httpapi is the HTTP transport of music-service. Paths follow
// docs/api/waveplayer.openapi.yaml (the gateway forwards /api/v1/... unchanged)
// plus the signed /api/v1/stream/* routes for <audio>/<img>.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
	"github.com/tapenest/tapenest/services/music-service/internal/service"
)

// Error codes ({message, code, service?} like api-gateway, ADR 0006).
const (
	CodeInvalid      = "INVALID"
	CodeUnauthorized = "UNAUTHORIZED"
	CodeNotFound     = "NOT_FOUND"
	CodeInternal     = "INTERNAL"
	CodeUnavailable  = "SERVICE_UNAVAILABLE"
)

// DegradedHeader marks a 503 caused by an optional dependency (Navidrome):
// api-gateway does not count it against the music-service breaker, so the
// catalog stays reachable while streaming is down (spec §8).
const DegradedHeader = "X-Degraded-Dependency"

// ErrorBody is the JSON error shape.
type ErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
}

// Pinger checks a dependency (readyz).
type Pinger func(ctx context.Context) error

// Deps wires the router.
type Deps struct {
	Library       *service.Library
	Wave          *service.Wave
	Events        *service.Events
	Streamer      *service.Streamer
	Discovery     *service.Discovery
	Internal      *service.Internal // nil disables /internal/v1/*
	InternalToken string
	Log           *slog.Logger
	Ready         map[string]Pinger // required dependencies
	Optional      map[string]Pinger // reported, never fail readiness (navidrome)
	Now           func() time.Time
}

type ctxKey struct{}

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Use(h.requestLog)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.readyz)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		// signed, user-less routes (gateway: no JWT, see gateway.openapi.yaml)
		r.Get("/api/v1/stream/tracks/{id}", h.stream)
		r.Head("/api/v1/stream/tracks/{id}", h.stream)
		r.Get("/api/v1/stream/covers/{id}", h.cover)
		r.Head("/api/v1/stream/covers/{id}", h.cover)
		// service-to-service routes for reco-service (never routed by api-gateway)
		if d.Internal != nil {
			r.Get("/internal/v1/catalog", h.exportCatalog)
			r.Get("/internal/v1/interactions", h.exportInteractions)
			r.Get("/internal/v1/tracks/{id}/audio", h.audio)
			if d.Discovery != nil {
				r.Post("/internal/v1/catalog/refresh", h.catalogRefresh)
			}
		}
		r.Group(func(r chi.Router) {
			r.Use(requireUser)
			r.Get("/api/v1/tracks/search", h.search)
			if d.Discovery != nil {
				r.Get("/api/v1/search", h.searchAll)
				r.Get("/api/v1/albums/{id}", h.getAlbum)
				r.Get("/api/v1/artists/{id}", h.getArtist)
				r.Group(func(r chi.Router) {
					r.Use(h.requireAdmin)
					r.Get("/api/v1/admin/acquisitions", h.adminProxy(adminList))
					r.Get("/api/v1/admin/acquisitions/{id}", h.adminProxy(adminGet))
					r.Get("/api/v1/admin/acquisition-status", h.adminProxy(adminStatus))
				})
			}
			r.Get("/api/v1/tracks/{kind:recent|popular|liked}", h.list)
			r.Get("/api/v1/tracks/{id}/stream-url", h.streamURL)
			r.Post("/api/v1/tracks/{id}/like", h.like)
			r.Delete("/api/v1/tracks/{id}/like", h.unlike)
			r.Get("/api/v1/tracks/{id}/position", h.getPosition)
			r.Put("/api/v1/tracks/{id}/position", h.putPosition)
			r.Get("/api/v1/playlists", h.playlists)
			r.Post("/api/v1/playlists", h.createPlaylist)
			r.Get("/api/v1/playlists/{id}", h.playlist)
			r.Patch("/api/v1/playlists/{id}", h.renamePlaylist)
			r.Delete("/api/v1/playlists/{id}", h.deletePlaylist)
			r.Post("/api/v1/playlists/{id}/tracks", h.addTrack)
			r.Delete("/api/v1/playlists/{id}/tracks/{trackId}", h.removeTrack)
			r.Post("/api/v1/wave/sessions", h.waveStart)
			r.Get("/api/v1/wave/sessions/{id}/tracks", h.waveNext)
			r.Post("/api/v1/wave/sessions/{id}/feedback", h.waveFeedback)
			r.Post("/api/v1/events/track-listened", h.listened)
			r.Post("/api/v1/events/track-skipped", h.skipped)
		})
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeInvalid, "method not allowed")
	})
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorBody{Message: msg, Code: code})
}

type handlers struct{ d Deps }

func (h *handlers) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Internal-Token")
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(h.d.InternalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "internal token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get("X-User-Id"))
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "user required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func userFrom(ctx context.Context) uuid.UUID { id, _ := ctx.Value(ctxKey{}).(uuid.UUID); return id }

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }

// Unwrap lets http.ResponseController reach Flush on the real writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (h *handlers) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// the path only: signed query strings are never logged
		h.d.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(), "request_id", r.Header.Get("X-Request-Id"))
	})
}

func (h *handlers) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out, ok := map[string]string{}, true
	for name, p := range h.d.Ready {
		if err := p(ctx); err != nil {
			out[name], ok = "down", false
		} else {
			out[name] = "ok"
		}
	}
	for name, p := range h.d.Optional {
		if err := p(ctx); err != nil {
			out[name] = "degraded"
		} else {
			out[name] = "ok"
		}
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, out)
}

// serviceError maps domain errors to HTTP.
func (h *handlers) serviceError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var inv *domain.InvalidError
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, CodeInvalid, inv.Msg)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, domain.ErrStreamingUnavailable):
		w.Header().Set(DegradedHeader, "navidrome")
		w.Header().Set("Retry-After", "15")
		writeJSON(w, http.StatusServiceUnavailable, ErrorBody{Message: "streaming is temporarily unavailable", Code: CodeUnavailable, Service: "streaming"})
	case errors.Is(err, context.Canceled):
		// client went away
	default:
		h.d.Log.ErrorContext(r.Context(), op+" failed", "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
