package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/api-gateway/internal/auth"
	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
	"github.com/tapenest/tapenest/services/api-gateway/internal/upstream"
)

// AuthService is what the handlers need from auth.Service.
type AuthService interface {
	Authenticator
	LoginTelegram(ctx context.Context, rawInitData string) (auth.Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (auth.Tokens, error)
	Logout(ctx context.Context, refreshToken string) error
	EnsureUser(ctx context.Context, p domain.TelegramProfile) (domain.User, error)
	User(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// UserDTO is the contract User (camelCase, UUID id + telegramId).
type UserDTO struct {
	ID           string      `json:"id"`
	TelegramID   int64       `json:"telegramId"`
	FirstName    string      `json:"firstName"`
	LastName     *string     `json:"lastName"`
	Username     *string     `json:"username"`
	PhotoURL     *string     `json:"photoUrl"`
	LanguageCode *string     `json:"languageCode"`
	Role         domain.Role `json:"role"`
}

// AuthTokensDTO is the contract AuthTokens (+ expiresIn, backwards compatible).
type AuthTokensDTO struct {
	AccessToken  string  `json:"accessToken"`
	RefreshToken string  `json:"refreshToken"`
	ExpiresIn    int64   `json:"expiresIn"`
	User         UserDTO `json:"user"`
}

func toUserDTO(u domain.User) UserDTO {
	return UserDTO{
		ID: u.ID.String(), TelegramID: u.TelegramID, FirstName: u.FirstName, LastName: u.LastName,
		Username: u.Username, PhotoURL: u.PhotoURL, LanguageCode: u.LanguageCode, Role: u.Role,
	}
}

func toTokensDTO(t auth.Tokens, now time.Time) AuthTokensDTO {
	return AuthTokensDTO{
		AccessToken: t.AccessToken, RefreshToken: t.RefreshToken,
		ExpiresIn: int64(t.AccessExpiresAt.Sub(now).Seconds()), User: toUserDTO(t.User),
	}
}

const maxBody = 64 << 10

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
		return false
	}
	return true
}

func validationFailed(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"message": "validation failed", "code": CodeInvalid, "fields": err})
}

type handlers struct {
	auth     AuthService
	download *upstream.Service
	log      *slog.Logger
	internal string
	now      func() time.Time
}

type telegramLoginReq struct {
	InitData string `json:"initData"`
}

func (h *handlers) loginTelegram(w http.ResponseWriter, r *http.Request) {
	var req telegramLoginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req,
		validation.Field(&req.InitData, validation.Required, validation.Length(1, 8192)),
	); err != nil {
		validationFailed(w, err)
		return
	}
	t, err := h.auth.LoginTelegram(r.Context(), req.InitData)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toTokensDTO(t, h.now()))
	case isErr(err, auth.ErrInitDataExpired), isErr(err, auth.ErrInitDataFuture):
		writeError(w, http.StatusUnauthorized, CodeInitDataExpired, "initData is expired, reopen the mini app")
	case isErr(err, auth.ErrInitDataSignature), isErr(err, auth.ErrInitDataMalformed):
		writeError(w, http.StatusUnauthorized, CodeInvalidInitData, "invalid initData")
	default:
		h.internalError(w, r, "login", err)
	}
}

type refreshReq struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req,
		validation.Field(&req.RefreshToken, validation.Required, validation.Length(16, 256)),
	); err != nil {
		validationFailed(w, err)
		return
	}
	t, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toTokensDTO(t, h.now()))
	case isErr(err, auth.ErrRefreshReused):
		writeError(w, http.StatusUnauthorized, CodeRefreshReused, "refresh token reuse detected, session revoked")
	case isErr(err, auth.ErrRefreshInvalid):
		writeError(w, http.StatusUnauthorized, CodeRefreshInvalid, "refresh token invalid or expired")
	default:
		h.internalError(w, r, "refresh", err)
	}
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req, validation.Field(&req.RefreshToken, validation.Required)); err != nil {
		validationFailed(w, err)
		return
	}
	if err := h.auth.Logout(r.Context(), req.RefreshToken); err != nil {
		h.internalError(w, r, "logout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	u, err := h.auth.User(r.Context(), p.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "user no longer exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "me", err)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(u))
}

// requireInternal protects service-to-service endpoints (bot-service → gateway).
func (h *handlers) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.internal)) != 1 {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type botDownloadReq struct {
	TelegramID   int64  `json:"telegramId"`
	ChatID       int64  `json:"chatId"`
	URL          string `json:"url"`
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Username     string `json:"username"`
	LanguageCode string `json:"languageCode"`
	// bot's "accepted…" status message (edited with progress) and the user's message
	StatusMessageID  int64 `json:"statusMessageId"`
	ReplyToMessageID int64 `json:"replyToMessageId"`
}

// botDownload accepts a link detected by bot-service: ensures the user exists and
// forwards the job to download-service (progress/result go back to the chat via
// the download:events stream). If download-service is not deployed in this
// environment the answer is an honest 501 NOT_IMPLEMENTED, never a fake success.
func (h *handlers) botDownload(w http.ResponseWriter, r *http.Request) {
	var req botDownloadReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req,
		validation.Field(&req.TelegramID, validation.Required, validation.Min(int64(1))),
		validation.Field(&req.ChatID, validation.Required),
		validation.Field(&req.URL, validation.Required, validation.Length(8, 2048), is.RequestURL,
			validation.By(httpScheme)),
		validation.Field(&req.LanguageCode, validation.Length(0, 16)),
	); err != nil {
		validationFailed(w, err)
		return
	}
	user, err := h.auth.EnsureUser(r.Context(), domain.TelegramProfile{
		ID: req.TelegramID, FirstName: req.FirstName, LastName: req.LastName,
		Username: req.Username, LanguageCode: req.LanguageCode,
	})
	if err != nil {
		h.internalError(w, r, "ensure user", err)
		return
	}
	body, _ := json.Marshal(map[string]any{
		"userId": user.ID.String(), "telegramId": req.TelegramID, "chatId": req.ChatID, "url": req.URL,
		"statusMessageId": req.StatusMessageID, "replyToMessageId": req.ReplyToMessageID, "lang": req.LanguageCode,
	})
	hdr := http.Header{
		"Content-Type": {"application/json"}, "X-Request-Id": {RequestID(r.Context())},
		"X-User-Id": {user.ID.String()},
	}
	resp, err := h.download.Do(r.Context(), http.MethodPost, "/internal/v1/downloads", hdr, body)
	if err != nil {
		upstreamError(h.log)(w, r, "download", err)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

func httpScheme(v any) error {
	s, _ := v.(string)
	if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
		return errors.New("must be an http(s) URL")
	}
	return nil
}

func (h *handlers) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.log.ErrorContext(r.Context(), op+" failed", "err", err, "request_id", RequestID(r.Context()))
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

func isErr(err, target error) bool { return errors.Is(err, target) }
