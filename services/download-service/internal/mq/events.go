package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Keys of the event/progress side.
const (
	// EventsStream is consumed by bot-service (group "bot-service").
	EventsStream = "download:events"
	dedupPrefix  = "download:dedup:"
	progressTTL  = time.Hour
)

// Event types on EventsStream. "video.downloaded" is the spec's completion event.
const (
	EventProgress   = "download.progress"
	EventDownloaded = "video.downloaded"
	EventFailed     = "download.failed"
)

// Event is the payload of EventsStream entries (bot jobs only) and of the per-job
// pub/sub channel used by SSE (all jobs).
type Event struct {
	Type             string           `json:"type"`
	JobID            uuid.UUID        `json:"jobId"`
	UserID           uuid.UUID        `json:"userId"`
	Status           domain.Status    `json:"status"`
	ChatID           int64            `json:"chatId,omitempty"`
	StatusMessageID  int64            `json:"statusMessageId,omitempty"`
	ReplyToMessageID int64            `json:"replyToMessageId,omitempty"`
	Lang             string           `json:"lang,omitempty"`
	Source           domain.Source    `json:"source"`
	URL              string           `json:"url"`
	Title            string           `json:"title,omitempty"`
	Progress         *domain.Progress `json:"progress,omitempty"`
	File             *FileInfo        `json:"file,omitempty"`
	ErrorKind        domain.ErrorKind `json:"errorKind,omitempty"`
	Cached           bool             `json:"cached,omitempty"`
	Attempt          int              `json:"attempt,omitempty"`
	At               time.Time        `json:"at"`
}

// FileInfo describes the stored file. InternalURL (MinIO endpoint, for
// bot-service upload to Telegram) and PublicURL (through the public origin,
// for users) are presigned with TTL ≤ 1 h.
type FileInfo struct {
	SizeBytes   int64  `json:"sizeBytes"`
	MimeType    string `json:"mimeType"`
	FileName    string `json:"fileName"`
	DurationSec int    `json:"durationSec"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	InternalURL string `json:"internalUrl,omitempty"`
	PublicURL   string `json:"publicUrl,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
}

// Bus publishes progress/events and keeps the dedup cache.
type Bus struct {
	rdb redis.UniversalClient
}

// NewBus creates a Bus.
func NewBus(rdb redis.UniversalClient) *Bus { return &Bus{rdb: rdb} }

// JobChannel is the pub/sub channel of one job (SSE).
func JobChannel(id uuid.UUID) string { return "download:job:" + id.String() }

func progressKey(id uuid.UUID) string { return "download:progress:" + id.String() }

// SetProgress stores the latest progress (TTL 1 h) and notifies SSE subscribers.
func (b *Bus) SetProgress(ctx context.Context, id uuid.UUID, p domain.Progress) error {
	raw, _ := json.Marshal(p)
	pipe := b.rdb.TxPipeline()
	pipe.Set(ctx, progressKey(id), raw, progressTTL)
	pipe.Publish(ctx, JobChannel(id), raw)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("set progress: %w", err)
	}
	return nil
}

// Progress returns the latest progress of a job (nil when unknown).
func (b *Bus) Progress(ctx context.Context, id uuid.UUID) (*domain.Progress, error) {
	raw, err := b.rdb.Get(ctx, progressKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get progress: %w", err)
	}
	var p domain.Progress
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil //nolint:nilerr // stale/corrupt entry = unknown
	}
	return &p, nil
}

// Publish sends an event: always to the job channel (SSE), and to EventsStream
// when the job came from the bot (ChatID set).
func (b *Bus) Publish(ctx context.Context, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	raw, _ := json.Marshal(e)
	pipe := b.rdb.TxPipeline()
	pipe.Publish(ctx, JobChannel(e.JobID), raw)
	if e.ChatID != 0 {
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: EventsStream, MaxLen: 10000, Approx: true, Values: map[string]any{"type": e.Type, "data": raw}})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// Subscribe listens to a job channel (SSE).
func (b *Bus) Subscribe(ctx context.Context, id uuid.UUID) *redis.PubSub {
	return b.rdb.Subscribe(ctx, JobChannel(id))
}

// CacheMedia is dedup layer 1: canonical URL hash → media id, TTL 1 h.
func (b *Bus) CacheMedia(ctx context.Context, hash string, mediaID uuid.UUID) error {
	if err := b.rdb.Set(ctx, dedupPrefix+hash, mediaID.String(), time.Hour).Err(); err != nil {
		return fmt.Errorf("cache media: %w", err)
	}
	return nil
}

// CachedMedia returns the cached media id for a URL hash (uuid.Nil when absent).
func (b *Bus) CachedMedia(ctx context.Context, hash string) (uuid.UUID, error) {
	v, err := b.rdb.Get(ctx, dedupPrefix+hash).Result()
	if errors.Is(err, redis.Nil) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cached media: %w", err)
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, nil //nolint:nilerr // corrupt entry = miss
	}
	return id, nil
}

// ForgetMedia drops a stale dedup entry (e.g. the object expired).
func (b *Bus) ForgetMedia(ctx context.Context, hash string) error {
	return b.rdb.Del(ctx, dedupPrefix+hash).Err()
}
