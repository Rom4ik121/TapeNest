package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
)

// mediaIndex implements dedup layers 1–2 (Redis 1 h → PostgreSQL by SHA-256),
// shared by the API (instant answers) and the worker (re-check under the lock).
type mediaIndex struct {
	store Store
	bus   Bus
	files Files
	log   *slog.Logger
	now   func() time.Time
}

func (x *mediaIndex) lookup(ctx context.Context, hash string) (domain.Media, bool, error) {
	if id, err := x.bus.CachedMedia(ctx, hash); err != nil {
		x.log.WarnContext(ctx, "dedup cache unavailable", "err", err)
	} else if id != uuid.Nil {
		m, err := x.store.GetMedia(ctx, id)
		if err == nil && m.ExpiresAt.After(x.now()) {
			return m, true, nil
		}
		_ = x.bus.ForgetMedia(ctx, hash)
	}
	m, err := x.store.GetMediaByHash(ctx, hash)
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Media{}, false, nil
	}
	if err != nil {
		return domain.Media{}, false, err
	}
	// the object may have been removed by the lifecycle rule / an operator
	if ok, err := x.files.Exists(ctx, m.ObjectKey); err != nil {
		return domain.Media{}, false, fmt.Errorf("check object: %w", err)
	} else if !ok {
		return domain.Media{}, false, nil
	}
	if err := x.bus.CacheMedia(ctx, hash, m.ID); err != nil {
		x.log.WarnContext(ctx, "dedup cache write failed", "err", err)
	}
	return m, true, nil
}

// fileInfo builds the file part of events/DTOs with presigned URLs (TTL ≤ 1 h).
func (x *mediaIndex) fileInfo(ctx context.Context, m domain.Media, ttl time.Duration) *mq.FileInfo {
	name := FileName(m.Title, m.ExternalID, m.MimeType)
	fi := &mq.FileInfo{
		SizeBytes: m.SizeBytes, MimeType: m.MimeType, FileName: name, DurationSec: m.DurationSec,
		Width: m.Width, Height: m.Height, ExpiresAt: x.now().Add(ttl).UTC().Format(time.RFC3339),
	}
	var err error
	if fi.InternalURL, err = x.files.PresignInternal(ctx, m.ObjectKey, name, ttl); err != nil {
		x.log.WarnContext(ctx, "presign internal failed", "err", err)
	}
	if fi.PublicURL, err = x.files.PresignPublic(ctx, m.ObjectKey, name, ttl); err != nil {
		x.log.WarnContext(ctx, "presign public failed", "err", err)
	}
	return fi
}

// FileName makes a safe download name from the title ("Title.mp4").
func FileName(title, fallback, mime string) string {
	ext := "mp4"
	if strings.Contains(mime, "webm") {
		ext = "webm"
	}
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
		if b.Len() >= 120 {
			break
		}
	}
	name := strings.Trim(b.String(), " .")
	if name == "" {
		name = fallback
	}
	if name == "" {
		name = "video"
	}
	return name + "." + ext
}

func eventFor(typ string, j domain.Job) mq.Event {
	e := mq.Event{Type: typ, JobID: j.ID, UserID: j.UserID, Status: j.Status, Source: j.Source, URL: j.Normalized, Title: j.Title, Attempt: j.Attempts}
	if j.Chat != nil {
		e.ChatID, e.StatusMessageID, e.ReplyToMessageID, e.Lang = j.Chat.ChatID, j.Chat.StatusMessageID, j.Chat.ReplyToMessageID, j.Chat.Lang
	}
	return e
}
