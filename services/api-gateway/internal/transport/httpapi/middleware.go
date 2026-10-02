package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/api-gateway/internal/auth"
	"github.com/tapenest/tapenest/services/api-gateway/internal/ratelimit"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
)

var reqIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID returns the request id stored in ctx.
func RequestID(ctx context.Context) string {
	s, _ := ctx.Value(ctxRequestID).(string)
	return s
}

// PrincipalFrom returns the authenticated caller, if any.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(auth.Principal)
	return p, ok
}

// requestIDMiddleware accepts a sane inbound X-Request-Id or generates one; it is
// echoed in the response and forwarded to upstreams (end-to-end trace id, spec §11).
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !reqIDPattern.MatchString(id) {
			id = uuid.NewString()
		}
		r.Header.Set("X-Request-Id", id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// loggingMiddleware writes one structured line per request. Never logs headers,
// bodies or query strings (tokens, initData).
func loggingMiddleware(log *slog.Logger, ip func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			defer func() {
				route := r.URL.Path
				if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
					route = rc.RoutePattern()
				}
				status := rec.status
				if status == 0 {
					status = http.StatusOK
				}
				attrs := []any{
					"request_id", RequestID(r.Context()), "method", r.Method, "route", route,
					"status", status, "bytes", rec.bytes, "duration_ms", time.Since(start).Milliseconds(),
					"ip", ip(r),
				}
				if p, ok := PrincipalFrom(r.Context()); ok {
					attrs = append(attrs, "user_id", p.UserID.String())
				}
				level := slog.LevelInfo
				if status >= 500 && status != http.StatusNotImplemented {
					level = slog.LevelError
				} else if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
					level = slog.LevelDebug
				}
				log.Log(r.Context(), level, "http request", attrs...)
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

func recoverMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity per net/http docs
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic", "request_id", RequestID(r.Context()), "panic", v, "stack", string(debug.Stack()))
					writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the caller IP; proxy headers are honoured only when trusted
// (dev: ngrok → Vite proxy → gateway).
func clientIP(trustProxy bool) func(*http.Request) string {
	return func(r *http.Request) string {
		if trustProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				first := strings.TrimSpace(strings.Split(xff, ",")[0])
				if net.ParseIP(first) != nil {
					return first
				}
			}
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}

// corsMiddleware allows only mini app origins (spec §9). "https://*.example.com"
// matches any single-level-or-deeper subdomain. No cookies are used (Bearer only).
func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	match := func(origin string) bool {
		for _, a := range allowed {
			if a == "*" || a == origin {
				return true
			}
			if scheme, rest, ok := strings.Cut(a, "://*."); ok {
				if strings.HasPrefix(origin, scheme+"://") && strings.HasSuffix(origin, "."+rest) {
					return true
				}
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			if !match(origin) {
				if r.Method == http.MethodOptions {
					writeError(w, http.StatusForbidden, CodeForbidden, "origin not allowed")
					return
				}
				next.ServeHTTP(w, r) // same-origin/no-CORS clients still work; browsers block the read
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", "X-Request-Id, Retry-After, X-RateLimit-Remaining")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Authenticator validates access tokens.
type Authenticator interface {
	Authenticate(ctx context.Context, accessToken string) (auth.Principal, error)
}

func requireAuth(a Authenticator, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, "missing bearer token")
				return
			}
			p, err := a.Authenticate(r.Context(), token)
			switch {
			case err == nil:
			case isErr(err, auth.ErrTokenExpired):
				writeError(w, http.StatusUnauthorized, CodeTokenExpired, "access token expired")
				return
			case isErr(err, auth.ErrTokenInvalid):
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid access token")
				return
			default:
				log.ErrorContext(r.Context(), "authenticate", "err", err, "request_id", RequestID(r.Context()))
				writeError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "auth temporarily unavailable")
				return
			}
			r.Header.Set("X-User-Id", p.UserID.String())
			r.Header.Set("X-Telegram-Id", strconv.FormatInt(p.TelegramID, 10))
			r.Header.Set("X-User-Role", string(p.Role))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
		})
	}
}

// RateLimiter abstracts ratelimit.Limiter for tests.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (ratelimit.Result, error)
}

// rateLimit keys by user id when authenticated, else by client IP. Redis errors
// fail open (availability over strictness; logged).
func rateLimit(l RateLimiter, ip func(*http.Request) string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + ip(r)
			if p, ok := PrincipalFrom(r.Context()); ok {
				key = "u:" + p.UserID.String()
			}
			res, err := l.Allow(r.Context(), key)
			if err != nil {
				log.WarnContext(r.Context(), "rate limiter unavailable, failing open", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			if !res.Allowed {
				secs := int(res.RetryAfter.Seconds() + 0.999)
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
