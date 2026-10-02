// Package httpapi is reco-service's internal HTTP API (docs/api/reco.openapi.yaml).
// Only music-service calls it (X-Internal-Token); api-gateway never routes here.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
)

// UserStore loads profiles.
type UserStore interface {
	LoadUser(ctx context.Context, user uuid.UUID, m *model.Model, now time.Time) (*rank.User, error)
	ProfileCounts(ctx context.Context, user uuid.UUID) (tracks, plays, likes, earlySkips int64, err error)
	Ping(ctx context.Context) error
}

// Deps wires the router.
type Deps struct {
	Models        *Models
	Users         UserStore
	Weights       rank.Weights
	InternalToken string
	Log           *slog.Logger
	Now           func() time.Time
}

// ErrorBody is the JSON error shape ({message, code}).
type ErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type handlers struct{ d Deps }

// NewRouter builds the handler.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	h := &handlers{d: d}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", h.readyz)
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternal)
		r.Post("/internal/v1/wave/next", h.next)
		r.Get("/internal/v1/users/{id}/profile", h.profile)
		r.Get("/internal/v1/tracks/{id}/similar", h.similar)
		r.Get("/internal/v1/model", h.modelInfo)
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
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

func (h *handlers) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]any{"postgres": "ok", "model": "ok"}
	status := http.StatusOK
	if h.d.Users.Ping(ctx) != nil {
		out["postgres"], status = "down", http.StatusServiceUnavailable
	}
	if m := h.d.Models.Current(); m == nil || len(m.Tracks) == 0 {
		out["model"], status = "empty", http.StatusServiceUnavailable
	} else {
		out["modelVersion"], out["tracks"] = m.Version, len(m.Tracks)
	}
	writeJSON(w, status, out)
}

// NextRequest is the body of POST /internal/v1/wave/next.
type NextRequest struct {
	UserID    uuid.UUID   `json:"userId"`
	SessionID uuid.UUID   `json:"sessionId"`
	Mode      string      `json:"mode"`
	Limit     int         `json:"limit"`
	Exclude   []uuid.UUID `json:"exclude"`
	Recent    []uuid.UUID `json:"recent"`
	Feedback  []struct {
		TrackID uuid.UUID `json:"trackId"`
		Action  string    `json:"action"`
	} `json:"feedback"`
}

// ReasonDTO explains a pick.
type ReasonDTO struct {
	Kind       string `json:"kind"`
	RefTrackID string `json:"refTrackId,omitempty"`
	RefTitle   string `json:"refTitle,omitempty"`
	RefArtist  string `json:"refArtist,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// PickDTO is one recommended track.
type PickDTO struct {
	TrackID string     `json:"trackId"`
	Score   float64    `json:"score"`
	Source  string     `json:"source"`
	Reason  *ReasonDTO `json:"reason,omitempty"`
}

func (h *handlers) next(w http.ResponseWriter, r *http.Request) {
	var req NextRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid JSON body")
		return
	}
	if req.UserID == uuid.Nil || !rank.ValidMode(req.Mode) || len(req.Exclude) > 5000 || len(req.Feedback) > 500 {
		writeError(w, http.StatusBadRequest, "INVALID", "userId required; mode must be valid; exclude ≤ 5000; feedback ≤ 500")
		return
	}
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}
	m := h.d.Models.Current()
	if m == nil || len(m.Tracks) == 0 {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "model not loaded")
		return
	}
	now := h.d.Now()
	u, err := h.d.Users.LoadUser(r.Context(), req.UserID, m, now)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			h.d.Log.Error("load user failed", "err", err)
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
		return
	}
	s := &rank.Session{Mode: req.Mode, Exclude: map[int]bool{}}
	for _, id := range req.Exclude {
		if i, ok := m.Index[id]; ok {
			s.Exclude[i] = true
		}
	}
	for _, id := range req.Recent {
		if i, ok := m.Index[id]; ok {
			s.Recent = append(s.Recent, i)
		}
	}
	for _, f := range req.Feedback {
		if i, ok := m.Index[f.TrackID]; ok && (f.Action == "like" || f.Action == "skip") {
			s.Feedback = append(s.Feedback, rank.Feedback{Track: i, Action: f.Action})
		}
	}
	hs := fnv.New64a()
	_, _ = hs.Write(req.SessionID[:])
	rnd := rand.New(rand.NewPCG(uint64(now.UnixNano()), hs.Sum64())) //nolint:gosec // exploration, not security
	picks := rank.Rank(m, u, s, h.d.Weights, req.Limit, rnd, now)
	out := make([]PickDTO, 0, len(picks))
	for _, p := range picks {
		out = append(out, PickDTO{TrackID: m.Tracks[p.Track].ID.String(), Score: round(p.Score), Source: p.Source, Reason: reasonDTO(m, p.Reason)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"modelVersion": m.Version, "tracks": out})
}

func round(v float64) float64 { return float64(int64(v*1000)) / 1000 }

func reasonDTO(m *model.Model, r rank.Reason) *ReasonDTO {
	if r.Kind == "" {
		return nil
	}
	d := &ReasonDTO{Kind: r.Kind, Artist: r.Artist, Genre: r.Genre, Tag: r.Tag}
	if r.Ref >= 0 && r.Ref < len(m.Tracks) {
		t := m.Tracks[r.Ref]
		d.RefTrackID, d.RefTitle, d.RefArtist = t.ID.String(), t.Title, t.Artist
	}
	return d
}

func (h *handlers) profile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid user id")
		return
	}
	m := h.d.Models.Current()
	if m == nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "model not loaded")
		return
	}
	u, err := h.d.Users.LoadUser(r.Context(), id, m, h.d.Now())
	if err != nil {
		h.d.Log.Error("load user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
		return
	}
	tracks, plays, likes, early, err := h.d.Users.ProfileCounts(r.Context(), id)
	if err != nil {
		h.d.Log.Error("profile counts failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
		return
	}
	artistName := map[string]string{}
	for _, t := range m.Tracks {
		artistName[t.ArtistID.String()] = t.Artist
	}
	type weight struct {
		Key    string  `json:"key"`
		Label  string  `json:"label"`
		Weight float64 `json:"weight"`
	}
	top := map[string][]weight{}
	for k, v := range u.Taste {
		kind, key, _ := strings.Cut(k, ":")
		label := key
		if kind == "artist" {
			label = artistName[key]
		}
		if kind == "album" {
			continue
		}
		top[kind] = append(top[kind], weight{Key: key, Label: label, Weight: round(v)})
	}
	for k := range top {
		sort.Slice(top[k], func(a, b int) bool { return top[k][a].Weight > top[k][b].Weight })
		if len(top[k]) > 10 {
			top[k] = top[k][:10]
		}
	}
	src := map[string]rank.Beta{}
	for k, v := range u.Sources {
		src[k] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userId": id.String(), "tracks": tracks, "plays": plays, "likes": likes, "earlySkips": early,
		"hasFactors": u.Factors != nil, "top": top, "sources": src, "coldStart": len(u.Taste) == 0,
	})
}

func (h *handlers) similar(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", "invalid track id")
		return
	}
	m := h.d.Models.Current()
	if m == nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "model not loaded")
		return
	}
	i, ok := m.Index[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "track not found")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	type sim struct {
		TrackID string  `json:"trackId"`
		Title   string  `json:"title"`
		Artist  string  `json:"artist"`
		Sim     float64 `json:"sim"`
	}
	conv := func(ns []model.Neighbor) []sim {
		out := []sim{}
		for k, n := range ns {
			if k >= limit {
				break
			}
			t := m.Tracks[n.Idx]
			out = append(out, sim{TrackID: t.ID.String(), Title: t.Title, Artist: t.Artist, Sim: round(float64(n.Sim))})
		}
		return out
	}
	t := m.Tracks[i]
	writeJSON(w, http.StatusOK, map[string]any{
		"trackId": id.String(), "tags": t.Tags, "tempo": round(t.Tempo),
		"energyPct": round(t.EnergyPct), "genre": t.Genre, "content": conv(m.Content[i]), "cf": conv(m.CF[i]),
	})
}

func (h *handlers) modelInfo(w http.ResponseWriter, _ *http.Request) {
	m := h.d.Models.Current()
	if m == nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "model not loaded")
		return
	}
	withEmb, withCF, withFactors := 0, 0, 0
	genres := map[string]int{}
	for i, t := range m.Tracks {
		if t.Emb != nil {
			withEmb++
		}
		if len(m.CF[i]) > 0 {
			withCF++
		}
		if m.ItemFactors[i] != nil {
			withFactors++
		}
		genres[t.Genre]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": m.Version, "tracks": len(m.Tracks), "withAudioFeatures": withEmb,
		"withCF": withCF, "withFactors": withFactors, "genres": genres, "builtAt": m.BuiltAt.UTC(), "weights": h.d.Weights,
	})
}
