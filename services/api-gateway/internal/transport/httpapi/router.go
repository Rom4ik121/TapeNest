package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tapenest/tapenest/services/api-gateway/internal/upstream"
)

// Pinger is a readiness dependency (PostgreSQL, Redis).
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingFunc adapts a function to Pinger.
type PingFunc func(ctx context.Context) error

// Ping implements Pinger.
func (f PingFunc) Ping(ctx context.Context) error { return f(ctx) }

// Deps are the router dependencies.
type Deps struct {
	Log           *slog.Logger
	Auth          AuthService
	AuthLimiter   RateLimiter // per IP, auth endpoints
	APILimiter    RateLimiter // per user, authenticated API
	CORSOrigins   []string
	TrustProxy    bool
	InternalToken string
	Ready         map[string]Pinger
	Music         *upstream.Service
	Download      *upstream.Service
	Streaming     *upstream.Service
	Now           func() time.Time
}

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	ip := clientIP(d.TrustProxy)
	onUpstreamErr := upstreamError(d.Log)
	h := &handlers{auth: d.Auth, download: d.Download, log: d.Log, internal: d.InternalToken, now: d.Now}

	r := chi.NewRouter()
	r.Use(requestIDMiddleware, loggingMiddleware(d.Log, ip), recoverMiddleware(d.Log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", readyHandler(d.Ready))

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(corsMiddleware(d.CORSOrigins))
		r.Options("/*", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		r.Route("/auth", func(r chi.Router) {
			r.Use(rateLimit(d.AuthLimiter, ip, d.Log))
			r.Post("/telegram", h.loginTelegram)
			r.Post("/refresh", h.refresh)
			r.Post("/logout", h.logout)
		})
		// Signed media links (music-service verifies the HMAC signature and expiry):
		// <audio src> and <img src> cannot send a bearer token, so no JWT here (ADR-0009).
		// Only GET/HEAD are routed.
		stream := stripIdentity(d.Music.Handler(onUpstreamErr))
		r.Get("/stream/*", stream.ServeHTTP)
		r.Head("/stream/*", stream.ServeHTTP)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth(d.Auth, d.Log), rateLimit(d.APILimiter, ip, d.Log))
			r.Get("/me", h.me)
			// music-service (docs/api/waveplayer.openapi.yaml).
			music := d.Music.Handler(onUpstreamErr)
			// /search, /albums, /artists: unified catalog (library + MusicBrainz, ADR 0011);
			// /admin/*: acquisition status, music-service checks X-User-Role=admin.
			for _, p := range []string{"/tracks", "/tracks/*", "/playlists", "/playlists/*", "/wave/*", "/events/*", "/search", "/albums/*", "/artists/*", "/admin/*"} {
				r.Handle(p, music)
			}
			r.Handle("/downloads", d.Download.Handler(onUpstreamErr))
			r.Handle("/downloads/*", d.Download.Handler(onUpstreamErr))
			r.Handle("/cinema/*", d.Streaming.Handler(onUpstreamErr))
		})
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		})
	})

	// Service-to-service API; never routed from the public edge (nginx/Vite proxy /api only).
	r.Route("/internal/v1", func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Post("/bot/downloads", h.botDownload)
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeInvalid, "method not allowed")
	})
	return r
}

// stripIdentity drops client-supplied identity headers on unauthenticated
// proxied routes, so upstreams never see a spoofed X-User-Id.
func stripIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-User-Id")
		r.Header.Del("X-Telegram-Id")
		r.Header.Del("X-User-Role")
		next.ServeHTTP(w, r)
	})
}

func readyHandler(deps map[string]Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		checks := map[string]string{}
		status := http.StatusOK
		for name, p := range deps {
			if err := p.Ping(ctx); err != nil {
				checks[name] = "down"
				status = http.StatusServiceUnavailable
			} else {
				checks[name] = "ok"
			}
		}
		writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK], "checks": checks})
	}
}
