package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Subscriber opens a job's pub/sub channel (mq.Bus).
type Subscriber interface {
	Subscribe(ctx context.Context, id uuid.UUID) *redis.PubSub
}

// events streams job updates as Server-Sent Events (spec §5.3: progress → SSE).
// Every message is a full Job ("event: job"); the stream ends after a terminal state.
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	user := userFrom(r.Context())
	v, err := h.d.API.Get(r.Context(), user, id)
	if err != nil {
		h.serviceError(w, r, "events", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.d.SSEMax)
	defer cancel()
	sub := h.d.Bus.Subscribe(ctx, id)
	defer sub.Close()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(http.StatusOK)
	send := func(dto JobDTO) bool {
		raw, _ := json.Marshal(dto)
		if _, err := fmt.Fprintf(w, "event: job\ndata: %s\n\n", raw); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(toDTO(v)) || v.Job.Status.Terminal() {
		return
	}
	ch := sub.Channel()
	hb := time.NewTicker(h.d.SSEHeartbeat)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case msg, ok := <-ch:
			if !ok {
				return
			}
			v, err := h.d.API.Get(ctx, user, id)
			if err != nil {
				return
			}
			// progress messages carry fresher numbers than the Redis snapshot read in Get
			var p domain.Progress
			if json.Unmarshal([]byte(msg.Payload), &p) == nil && p.Stage != "" && v.Job.Status == domain.StatusRunning {
				v.Progress = &p
			}
			if !send(toDTO(v)) || v.Job.Status.Terminal() {
				return
			}
		}
	}
}
