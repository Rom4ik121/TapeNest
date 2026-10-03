package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status of a job.
type Status string

// Job lifecycle: queued → running → done | failed (retries go back to queued).
const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Terminal reports whether the job will not change anymore.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusFailed }

// Stage of a running job.
type Stage string

// Running stages.
const (
	StageProbing     Stage = "probing"
	StageDownloading Stage = "downloading"
	StageUploading   Stage = "uploading"
)

// Priority selects the queue stream (spec §5.3: high/normal/low).
type Priority string

// Priorities. Retries go to low so fresh requests are not starved.
const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Priorities in the order workers poll them.
var Priorities = []Priority{PriorityHigh, PriorityNormal, PriorityLow}

// Chat is set for jobs started from the bot: where to report progress/result.
type Chat struct {
	ChatID           int64
	StatusMessageID  int64 // bot's "accepted…" message, edited with progress
	ReplyToMessageID int64 // the user's message with the link
	Lang             string
}

// Job is one user request to download a URL.
type Job struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	URL          string // as sent by the user
	Normalized   string
	URLHash      string
	Source       Source
	Status       Status
	Stage        Stage
	Priority     Priority
	Attempts     int
	ErrorKind    ErrorKind
	ErrorMessage string
	MediaID      *uuid.UUID
	Title        string
	DisplayTitle string // owner rename; empty means Title
	DeletedAt    *time.Time
	Chat         *Chat
	CreatedAt    time.Time
	UpdatedAt    time.Time
	FinishedAt   *time.Time
}

// VisibleTitle is the name shown to the owner (rename, else the source title).
func (j Job) VisibleTitle() string {
	if t := strings.TrimSpace(j.DisplayTitle); t != "" {
		return t
	}
	return strings.TrimSpace(j.Title)
}

// Media is a stored file, shared by every job with the same canonical URL (dedup).
type Media struct {
	ID          uuid.UUID
	URLHash     string
	URL         string
	Source      Source
	ExternalID  string
	Title       string
	DurationSec int
	Width       int
	Height      int
	FormatID    string
	ObjectKey   string
	SizeBytes   int64
	MimeType    string
	Thumbnail   string
	PosterKey   string // jpeg frame in the media bucket; empty if not extracted yet
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// OwnedEdit reports a file produced by trim or a timeline export (not a shared source download).
func (m Media) OwnedEdit() bool {
	return strings.HasPrefix(m.URL, "edit:")
}

// Progress of a running job (kept in Redis, streamed via SSE and to the bot).
type Progress struct {
	Stage           Stage   `json:"stage"`
	Pct             float64 `json:"pct"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	SpeedBps        float64 `json:"speedBps"`
	EtaSec          int     `json:"etaSec"`
}
