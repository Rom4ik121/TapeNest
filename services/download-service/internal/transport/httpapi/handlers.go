package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/service"
)

// JobDTO is the public job shape (download.openapi.yaml#/components/schemas/Job).
type JobDTO struct {
	ID           string           `json:"id"`
	URL          string           `json:"url"`
	Source       string           `json:"source"`
	Status       string           `json:"status"`
	Stage        string           `json:"stage,omitempty"`
	Progress     *domain.Progress `json:"progress,omitempty"`
	Title        string           `json:"title,omitempty"`
	Attempts     int              `json:"attempts"`
	ErrorKind    string           `json:"errorKind,omitempty"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	PosterURL    string           `json:"posterUrl,omitempty"`
	File         *FileDTO         `json:"file,omitempty"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	FinishedAt   *time.Time       `json:"finishedAt,omitempty"`
}

// FileDTO describes the stored file (the link itself comes from /file).
type FileDTO struct {
	FileName    string    `json:"fileName"`
	SizeBytes   int64     `json:"sizeBytes"`
	MimeType    string    `json:"mimeType"`
	DurationSec int       `json:"durationSec"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func toDTO(v service.View) JobDTO {
	j := v.Job
	title := j.VisibleTitle()
	d := JobDTO{
		ID: j.ID.String(), URL: j.Normalized, Source: string(j.Source), Status: string(j.Status), Stage: string(j.Stage),
		Progress: v.Progress, Title: title, Attempts: j.Attempts, ErrorKind: string(j.ErrorKind),
		PosterURL: v.PosterURL, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, FinishedAt: j.FinishedAt,
	}
	if j.Status == domain.StatusFailed {
		d.ErrorMessage = j.ErrorMessage
	}
	if f := v.File; f != nil {
		if d.Title == "" {
			d.Title = f.Title
		}
		d.File = &FileDTO{
			FileName: service.FileName(f.Title, f.ExternalID, f.MimeType), SizeBytes: f.SizeBytes, MimeType: f.MimeType,
			DurationSec: f.DurationSec, Width: f.Width, Height: f.Height, ExpiresAt: f.ExpiresAt,
		}
	}
	return d
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
		return false
	}
	return true
}

func (h *handlers) serviceError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, CodeInvalidURL, "not a video URL")
	case errors.Is(err, domain.ErrUnsupported):
		writeError(w, http.StatusBadRequest, CodeUnsupported, "supported sources: "+domain.HostSummary())
	case errors.Is(err, domain.ErrPlaylist):
		writeError(w, http.StatusBadRequest, CodePlaylist, "playlists are not supported, send a single video")
	case errors.Is(err, service.ErrForbiddenHost):
		writeError(w, http.StatusBadRequest, CodeForbiddenHost, "host is not allowed")
	case errors.Is(err, service.ErrQuotaActive):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, CodeQuotaActive, "too many active downloads")
	case errors.Is(err, service.ErrQuotaDaily):
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, CodeQuotaDaily, "daily download limit reached")
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "download not found")
	case errors.Is(err, service.ErrNotReady):
		writeError(w, http.StatusConflict, CodeNotReady, "download is not finished")
	case errors.Is(err, service.ErrNoPublicURL):
		writeError(w, http.StatusServiceUnavailable, CodeNoPublicURL, "public file links are not configured")
	case errors.Is(err, service.ErrBadTitle):
		writeError(w, http.StatusBadRequest, CodeInvalid, "title must be 1–120 characters")
	case errors.Is(err, service.ErrBadRange):
		writeError(w, http.StatusBadRequest, CodeInvalid, "trim needs a start and an end at least 1 second apart, inside the video")
	case errors.Is(err, service.ErrBadProject):
		writeError(w, http.StatusBadRequest, CodeTimeline, "the timeline is not valid")
	case errors.Is(err, service.ErrEditFailed):
		writeError(w, http.StatusUnprocessableEntity, CodeEditFailed, "could not trim this video")
	case errors.Is(err, service.ErrEditUnavailable):
		writeError(w, http.StatusServiceUnavailable, CodeEditUnavailable, "trimming is not available on this server")
	default:
		h.d.Log.ErrorContext(r.Context(), op+" failed", "err", err, "request_id", w.Header().Get("X-Request-Id"))
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

type createReq struct {
	URL string `json:"url"`
}

type internalCreateReq struct {
	UserID           string `json:"userId"`
	TelegramID       int64  `json:"telegramId"`
	ChatID           int64  `json:"chatId"`
	URL              string `json:"url"`
	StatusMessageID  int64  `json:"statusMessageId"`
	ReplyToMessageID int64  `json:"replyToMessageId"`
	Lang             string `json:"lang"`
}

func (h *handlers) respondCreated(w http.ResponseWriter, r *http.Request, j domain.Job, created bool) {
	status := http.StatusAccepted
	if !created || j.Status.Terminal() {
		status = http.StatusOK
	}
	v := service.View{Job: j}
	if j.Status != domain.StatusQueued { // cached or existing job: include progress/file
		if full, err := h.d.API.View(r.Context(), j); err == nil {
			v = full
		}
	}
	writeJSON(w, status, toDTO(v))
}

// create: POST /api/v1/downloads (mini app).
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if !decode(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req, validation.Field(&req.URL, validation.Required, validation.Length(4, 2048))); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	j, created, err := h.d.API.Create(r.Context(), service.CreateInput{UserID: userFrom(r.Context()), URL: req.URL})
	if err != nil {
		h.serviceError(w, r, "create", err)
		return
	}
	h.respondCreated(w, r, j, created)
}

// createInternal: POST /internal/v1/downloads (bot-service via api-gateway).
func (h *handlers) createInternal(w http.ResponseWriter, r *http.Request) {
	var req internalCreateReq
	if !decode(w, r, &req) {
		return
	}
	if err := validation.ValidateStruct(&req,
		validation.Field(&req.UserID, validation.Required, validation.By(isUUID)),
		validation.Field(&req.ChatID, validation.Required),
		validation.Field(&req.URL, validation.Required, validation.Length(4, 2048)),
		validation.Field(&req.Lang, validation.Length(0, 16)),
	); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, err.Error())
		return
	}
	uid, _ := uuid.Parse(req.UserID)
	j, created, err := h.d.API.Create(r.Context(), service.CreateInput{
		UserID: uid, URL: req.URL,
		Chat: &domain.Chat{ChatID: req.ChatID, StatusMessageID: req.StatusMessageID, ReplyToMessageID: req.ReplyToMessageID, Lang: req.Lang},
	})
	if err != nil {
		h.serviceError(w, r, "create internal", err)
		return
	}
	h.respondCreated(w, r, j, created)
}

func isUUID(v any) error {
	s, _ := v.(string)
	if _, err := uuid.Parse(s); err != nil {
		return errors.New("must be a UUID")
	}
	return nil
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "download not found")
		return uuid.Nil, false
	}
	return id, true
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, err := h.d.API.Get(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "get", err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(v))
}

// PageDTO is a cursor page.
type PageDTO struct {
	Items      []JobDTO `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

func encodeCursor(c repo.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + ":" + c.ID.String()))
}

func decodeCursor(s string) (*repo.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	ts, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, errors.New("bad cursor")
	}
	us, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &repo.Cursor{CreatedAt: time.UnixMicro(us), ID: u}, nil
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 50 {
			writeError(w, http.StatusBadRequest, CodeInvalid, "limit must be 1..50")
			return
		}
		limit = n
	}
	var after *repo.Cursor
	if s := r.URL.Query().Get("cursor"); s != "" {
		c, err := decodeCursor(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalid, "invalid cursor")
			return
		}
		after = c
	}
	views, err := h.d.API.List(r.Context(), userFrom(r.Context()), after, limit+1)
	if err != nil {
		h.serviceError(w, r, "list", err)
		return
	}
	page := PageDTO{Items: make([]JobDTO, 0, len(views))}
	if len(views) > limit {
		views = views[:limit]
		last := views[len(views)-1].Job
		c := encodeCursor(repo.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &c
	}
	for _, v := range views {
		page.Items = append(page.Items, toDTO(v))
	}
	writeJSON(w, http.StatusOK, page)
}

// file: 302 to a presigned link (TTL ≤ 1 h); ?redirect=false returns JSON.
func (h *handlers) file(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.d.API.FileURL(r.Context(), userFrom(r.Context()), id)
	if err != nil {
		h.serviceError(w, r, "file", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("redirect") == "false" {
		writeJSON(w, http.StatusOK, map[string]string{"url": u})
		return
	}
	http.Redirect(w, r, u, http.StatusFound) //nolint:gosec // u is our own presigned MinIO URL, not user input
}

type titleReq struct {
	Title string `json:"title"`
}

func (h *handlers) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req titleReq
	if !decode(w, r, &req) {
		return
	}
	v, err := h.d.API.Rename(r.Context(), userFrom(r.Context()), id, req.Title)
	if err != nil {
		h.serviceError(w, r, "rename", err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(v))
}

func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.d.API.Delete(r.Context(), userFrom(r.Context()), id); err != nil {
		h.serviceError(w, r, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type trimReq struct {
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}

func (h *handlers) trim(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req trimReq
	if !decode(w, r, &req) {
		return
	}
	v, err := h.d.API.Trim(r.Context(), userFrom(r.Context()), id, req.StartSec, req.EndSec)
	if err != nil {
		h.serviceError(w, r, "trim", err)
		return
	}
	writeJSON(w, http.StatusCreated, toDTO(v))
}

func (h *handlers) compose(w http.ResponseWriter, r *http.Request) {
	var req service.Project
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid JSON body")
		return
	}
	v, err := h.d.API.Compose(r.Context(), userFrom(r.Context()), req)
	if err != nil {
		h.serviceError(w, r, "compose", err)
		return
	}
	writeJSON(w, http.StatusCreated, toDTO(v))
}
