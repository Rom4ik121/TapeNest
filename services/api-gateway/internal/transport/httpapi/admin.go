package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

// requireAllowlist refuses every Telegram account that is not in ADMIN_TELEGRAM_IDS.
// The live list wins over a role baked into an older access token.
func requireAllowlist(ids map[int64]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok || !ids[p.TelegramID] {
				writeError(w, http.StatusForbidden, CodeForbidden, "not on the admin allowlist")
				return
			}
			r.Header.Set("X-User-Role", string(domain.RoleAdmin))
			next.ServeHTTP(w, r)
		})
	}
}

type adminUserDTO struct {
	ID           string     `json:"id"`
	TelegramID   int64      `json:"telegramId"`
	FirstName    string     `json:"firstName"`
	LastName     *string    `json:"lastName"`
	Username     *string    `json:"username"`
	LanguageCode *string    `json:"languageCode"`
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
}

func (h *handlers) adminSession(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"telegramId": p.TelegramID, "role": domain.RoleAdmin})
}

func (h *handlers) adminUsers(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		writeError(w, http.StatusNotImplemented, CodeNotImplemented, "user directory is not configured")
		return
	}
	users, err := h.users.ListUsers(r.Context(), 100)
	if err != nil {
		h.internalError(w, r, "admin users", err)
		return
	}
	items := make([]adminUserDTO, 0, len(users))
	for _, u := range users {
		items = append(items, adminUserDTO{
			ID: u.ID.String(), TelegramID: u.TelegramID, FirstName: u.FirstName, LastName: u.LastName,
			Username: u.Username, LanguageCode: u.LanguageCode, Role: string(u.Role),
			CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type healthDTO struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (h *handlers) adminHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out := make([]healthDTO, 1+len(h.probes))
	out[0] = healthDTO{Name: "gateway", Status: gatewayStatus(ctx, h.ready)}
	var wg sync.WaitGroup
	for i, p := range h.probes {
		wg.Add(1)
		go func(i int, p Probe) {
			defer wg.Done()
			out[i+1] = healthDTO{Name: p.Name, Status: probeStatus(ctx, p.URL)}
		}(i, p)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"services": out})
}

func gatewayStatus(ctx context.Context, ready map[string]Pinger) string {
	if len(ready) == 0 {
		return "ok"
	}
	for _, p := range ready {
		if p.Ping(ctx) != nil {
			return "down"
		}
	}
	return "ok"
}

var probeClient = &http.Client{
	Timeout: 2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func probeStatus(ctx context.Context, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "off"
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "down"
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/healthz"
	base.RawQuery, base.Fragment = "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return "down"
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok"
	}
	return "down"
}
