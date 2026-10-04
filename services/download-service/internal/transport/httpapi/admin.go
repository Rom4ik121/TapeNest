package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/service"
)

// adminJobDTO is a job plus its owner. File metadata has no object key and no presigned URL.
type adminJobDTO struct {
	JobDTO
	UserID string `json:"userId"`
}

func toAdminDTO(v service.View) adminJobDTO {
	d := toDTO(v)
	d.URL = v.Job.URL
	return adminJobDTO{JobDTO: d, UserID: v.Job.UserID.String()}
}

func adminStatus(raw string) (string, bool) {
	switch raw {
	case "", string(domain.StatusQueued), string(domain.StatusRunning), string(domain.StatusDone), string(domain.StatusFailed):
		return raw, true
	default:
		return "", false
	}
}

func (h *handlers) adminList(w http.ResponseWriter, r *http.Request) {
	status, ok := adminStatus(r.URL.Query().Get("status"))
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalid, "status must be queued, running, done or failed")
		return
	}
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
	views, err := h.d.API.AdminList(r.Context(), status, after, limit+1)
	if err != nil {
		h.serviceError(w, r, "admin list", err)
		return
	}
	page := struct {
		Items      []adminJobDTO `json:"items"`
		NextCursor *string       `json:"nextCursor"`
	}{Items: make([]adminJobDTO, 0, len(views))}
	if len(views) > limit {
		views = views[:limit]
		last := views[len(views)-1].Job
		c := encodeCursor(repo.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &c
	}
	for _, v := range views {
		page.Items = append(page.Items, toAdminDTO(v))
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handlers) adminRetry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.d.API.Retry(r.Context(), id)
	if err != nil {
		h.adminError(w, r, "admin retry", err)
		return
	}
	writeJSON(w, http.StatusOK, toAdminDTO(service.View{Job: j}))
}

func (h *handlers) adminRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.d.API.RemoveFromLibrary(r.Context(), id); err != nil {
		h.adminError(w, r, "admin remove", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) adminError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, service.ErrNotFailed):
		writeError(w, http.StatusConflict, CodeConflict, "only a failed download can be retried")
	case errors.Is(err, service.ErrNotDone):
		writeError(w, http.StatusConflict, CodeConflict, "only a finished download can be removed from the library")
	default:
		h.serviceError(w, r, op, err)
	}
}
