// Package downloads delivers download-service results to chats (spec §5.1:
// "отправка уведомлений — прямой вызов Bot API"). It consumes the
// download:events Redis stream (group bot-service) and edits the status message
// the bot sent when the link arrived; finished files ≤ the Bot API upload limit
// (50 MB) are uploaded to the chat, bigger ones are offered as a presigned link.
package downloads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

// Event types published by download-service.
const (
	TypeProgress   = "download.progress"
	TypeDownloaded = "video.downloaded"
	TypeFailed     = "download.failed"
)

// Progress of a running job.
type Progress struct {
	Stage string  `json:"stage"`
	Pct   float64 `json:"pct"`
}

// FileInfo describes the stored file (presigned URLs, TTL ≤ 1 h).
type FileInfo struct {
	SizeBytes   int64  `json:"sizeBytes"`
	MimeType    string `json:"mimeType"`
	FileName    string `json:"fileName"`
	DurationSec int    `json:"durationSec"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	InternalURL string `json:"internalUrl"`
	PublicURL   string `json:"publicUrl"`
}

// Event is one download:events entry (subset of download-service mq.Event).
type Event struct {
	Type             string    `json:"type"`
	JobID            string    `json:"jobId"`
	Status           string    `json:"status"`
	ChatID           int64     `json:"chatId"`
	StatusMessageID  int64     `json:"statusMessageId"`
	ReplyToMessageID int64     `json:"replyToMessageId"`
	Lang             string    `json:"lang"`
	Source           string    `json:"source"`
	Title            string    `json:"title"`
	Progress         *Progress `json:"progress"`
	File             *FileInfo `json:"file"`
	ErrorKind        string    `json:"errorKind"`
	Cached           bool      `json:"cached"`
	Attempt          int       `json:"attempt"`
}

// Telegram is the Bot API subset used here.
type Telegram interface {
	SendMessage(ctx context.Context, m telegram.SendMessage) (telegram.Message, error)
	EditMessageText(ctx context.Context, m telegram.EditMessageText) error
	SendFile(ctx context.Context, u telegram.Upload) error
}

// Notifier renders events into chat messages.
type Notifier struct {
	TG          Telegram
	Texts       *i18n.Bundle
	HTTP        *http.Client // fetches the file from MinIO (internal presigned URL)
	UploadLimit int64        // Bot API limit for bot uploads (50 MB)
	Redis       redis.UniversalClient
	Log         *slog.Logger
}

var sourceNames = map[string]string{
	"youtube": "YouTube", "vk": "VK", "rutube": "RuTube", "tiktok": "TikTok", "vimeo": "Vimeo",
	"dailymotion": "Dailymotion", "instagram": "Instagram", "twitter": "X", "twitch": "Twitch",
	"facebook": "Facebook", "ok": "OK", "coub": "Coub", "reddit": "Reddit", "streamable": "Streamable",
	"rumble": "Rumble", "kick": "Kick", "bilibili": "Bilibili", "mailru": "Mail.ru", "niconico": "Niconico",
	"telegram": "Telegram",
}

func (n *Notifier) t(e Event, key string, args ...string) string {
	return n.Texts.T(i18n.Detect(e.Lang), key, args...)
}

func title(e Event) string {
	if t := strings.TrimSpace(e.Title); t != "" {
		if r := []rune(t); len(r) > 200 {
			return string(r[:200]) + "…"
		}
		return t
	}
	if s, ok := sourceNames[e.Source]; ok {
		return s
	}
	return "video"
}

// bar renders a 10-cell progress bar.
func bar(pct float64) string {
	n := int(math.Round(math.Max(0, math.Min(100, pct)) / 10))
	return strings.Repeat("▓", n) + strings.Repeat("░", 10-n)
}

// HumanSize formats bytes as "12.3 MB" (decimal, like Telegram's limit).
func HumanSize(b int64) string {
	switch {
	case b >= 1e9:
		return strconv.FormatFloat(float64(b)/1e9, 'f', 1, 64) + " GB"
	case b >= 1e6:
		return strconv.FormatFloat(float64(b)/1e6, 'f', 1, 64) + " MB"
	default:
		return strconv.FormatFloat(float64(b)/1e3, 'f', 0, 64) + " KB"
	}
}

// Handle processes one event. Errors are transient delivery failures; a chat that
// can no longer be reached (bot blocked, chat not found) drops the event.
func (n *Notifier) Handle(ctx context.Context, e Event) error {
	if e.ChatID == 0 {
		return nil
	}
	err := n.handle(ctx, e)
	if telegram.IsUnreachable(err) {
		n.Log.InfoContext(ctx, "chat unreachable, dropping download event", "job", e.JobID, "type", e.Type, "chat_id", e.ChatID, "err", err)
		return nil
	}
	return err
}

func (n *Notifier) handle(ctx context.Context, e Event) error {
	switch e.Type {
	case TypeProgress:
		return n.progress(ctx, e)
	case TypeDownloaded:
		return n.downloaded(ctx, e)
	case TypeFailed:
		key := "dl.error." + e.ErrorKind
		text := n.t(e, key, "title", title(e))
		if text == key {
			text = n.t(e, "dl.error.internal", "title", title(e))
		}
		return n.status(ctx, e, text, nil)
	}
	return nil
}

func (n *Notifier) progress(ctx context.Context, e Event) error {
	var text string
	switch {
	case e.ErrorKind != "" && e.Status == "queued":
		text = n.t(e, "dl.retry", "title", title(e), "attempt", strconv.Itoa(e.Attempt+1))
	case e.Progress == nil || e.Progress.Stage == "probing":
		text = n.t(e, "dl.probing", "source", title(e))
	case e.Progress.Stage == "uploading":
		text = n.t(e, "dl.storing", "title", title(e))
	default:
		text = n.t(e, "dl.progress", "title", title(e), "bar", bar(e.Progress.Pct), "pct", strconv.Itoa(int(e.Progress.Pct)))
	}
	return n.status(ctx, e, text, nil)
}

func (n *Notifier) downloaded(ctx context.Context, e Event) error {
	f := e.File
	if f == nil {
		return n.status(ctx, e, n.t(e, "dl.error.internal", "title", title(e)), nil)
	}
	// at-least-once stream delivery: never upload the same job twice
	if n.Redis != nil {
		fresh, err := n.Redis.SetNX(ctx, "bot:dl:done:"+e.JobID, 1, 24*time.Hour).Result()
		if err == nil && !fresh {
			return nil
		}
	}
	if f.SizeBytes > 0 && f.SizeBytes <= n.UploadLimit && f.InternalURL != "" {
		_ = n.status(ctx, e, n.t(e, "dl.sending", "title", title(e)), nil)
		err := n.upload(ctx, e)
		if err == nil {
			return n.status(ctx, e, n.t(e, "dl.done", "title", title(e), "size", HumanSize(f.SizeBytes)), nil)
		}
		if telegram.IsUnreachable(err) {
			return err
		}
		n.Log.WarnContext(ctx, "upload to telegram failed, offering a link", "job", e.JobID, "too_large", telegram.IsTooLarge(err), "err", err)
	}
	return n.linkMessage(ctx, e)
}

func (n *Notifier) upload(ctx context.Context, e Event) error {
	f := e.File
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.InternalURL, nil)
	if err != nil {
		return fmt.Errorf("file request: %w", err)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("fetch file: %w", redactURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch file: status %d", resp.StatusCode)
	}
	size := f.SizeBytes
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}
	if size > n.UploadLimit {
		return errors.New("file exceeds the upload limit")
	}
	caption := "🎬 " + title(e)
	return n.TG.SendFile(ctx, telegram.Upload{
		ChatID: e.ChatID, ReplyToMessageID: e.ReplyToMessageID, Caption: caption, FileName: f.FileName,
		MimeType: f.MimeType, Size: size, Body: resp.Body, DurationSec: f.DurationSec, Width: f.Width, Height: f.Height,
		AsDocument: f.MimeType != "video/mp4",
	})
}

// linkMessage offers the presigned public link for files over the upload limit.
func (n *Notifier) linkMessage(ctx context.Context, e Event) error {
	f := e.File
	args := []string{"title", title(e), "size", HumanSize(f.SizeBytes), "limit", HumanSize(n.UploadLimit)}
	if f.PublicURL == "" {
		return n.status(ctx, e, n.t(e, "dl.too_big_no_link", args...), nil)
	}
	kb := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		{Text: n.t(e, "dl.button.download"), URL: f.PublicURL},
	}}}
	return n.status(ctx, e, n.t(e, "dl.too_big", args...), kb)
}

// status edits the bot's status message (or sends a new one when there is none).
func (n *Notifier) status(ctx context.Context, e Event, text string, kb *telegram.InlineKeyboardMarkup) error {
	lp := &telegram.LinkPreviewOptions{IsDisabled: true}
	if e.StatusMessageID != 0 {
		err := n.TG.EditMessageText(ctx, telegram.EditMessageText{ChatID: e.ChatID, MessageID: e.StatusMessageID, Text: text, ReplyMarkup: kb, LinkPreviewOptions: lp})
		if err == nil {
			return nil
		}
		var apiErr *telegram.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest || telegram.IsUnreachable(err) {
			return err
		}
		// the status message was deleted by the user: fall back to a new message
	}
	m := telegram.SendMessage{ChatID: e.ChatID, Text: text, ReplyMarkup: kb, LinkPreviewOptions: lp}
	if e.ReplyToMessageID != 0 {
		m.ReplyParameters = &telegram.ReplyParameters{MessageID: e.ReplyToMessageID, AllowSendingWithoutReply: true}
	}
	_, err := n.TG.SendMessage(ctx, m)
	return err
}

// redactURL drops presigned query strings from transport errors.
func redactURL(err error) error {
	s := err.Error()
	if i := strings.Index(s, "?"); i >= 0 {
		end := strings.IndexAny(s[i:], "\" ")
		if end < 0 {
			s = s[:i]
		} else {
			s = s[:i] + s[i+end:]
		}
	}
	return errors.New(s)
}
