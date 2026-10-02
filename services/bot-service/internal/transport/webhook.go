// Package transport exposes the Telegram webhook and health endpoints (chi).
package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

// Handler processes an update.
type Handler interface {
	Handle(ctx context.Context, u telegram.Update, requestID string) error
}

// Deps of the router.
type Deps struct {
	Log     *slog.Logger
	Bot     Handler
	Redis   redis.UniversalClient
	Secret  string
	Path    string
	Timeout time.Duration
}

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.Timeout == 0 {
		d.Timeout = 20 * time.Second
	}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.Redis.Ping(ctx).Err(); err != nil {
			writeJSON(w, 503, map[string]any{"status": "degraded", "checks": map[string]string{"redis": "down"}})
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok", "checks": map[string]string{"redis": "ok"}})
	})
	r.Post(d.Path, webhook(d))
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// webhook verifies X-Telegram-Bot-Api-Secret-Token, deduplicates update_id
// (Telegram redelivers on timeouts) and handles the update synchronously.
// Handler errors are logged and acknowledged with 200 to avoid redelivery storms.
func webhook(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := uuid.NewString()
		got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(d.Secret)) != 1 {
			d.Log.Warn("webhook: bad secret token", "request_id", reqID, "remote", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var u telegram.Update
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&u); err != nil || u.UpdateID == 0 {
			http.Error(w, "bad update", http.StatusBadRequest)
			return
		}
		log := d.Log.With("request_id", reqID, "update_id", u.UpdateID)
		ctx, cancel := context.WithTimeout(r.Context(), d.Timeout)
		defer cancel()
		fresh, err := d.Redis.SetNX(ctx, "bot:upd:"+strconv.FormatInt(u.UpdateID, 10), 1, 24*time.Hour).Result()
		if err != nil {
			log.Warn("webhook: dedupe unavailable, processing anyway", "err", err)
			fresh = true
		}
		if !fresh {
			log.Info("webhook: duplicate update skipped")
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := d.Bot.Handle(ctx, u, reqID); err != nil {
			log.Error("webhook: handle failed", "err", err)
		}
		log.Info("webhook: update handled", "duration_ms", time.Since(start).Milliseconds())
		w.WriteHeader(http.StatusOK)
	}
}
