package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status of an export.
type Status string

// Export lifecycle.
const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Terminal reports a finished export.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusFailed }

// Busy reports an export the worker still owns.
func (s Status) Busy() bool { return s == StatusQueued || s == StatusRunning }

// SourceFile is a download the user already owns.
type SourceFile struct {
	ObjectKey   string
	Title       string
	DurationSec float64
	Width       int
	Height      int
	MimeType    string
	SizeBytes   int64
}

// Project is one edit of one download.
type Project struct {
	UserID      uuid.UUID
	ID          uuid.UUID
	SourceJobID uuid.UUID
	ObjectKey   string
	Title       string
	Duration    float64
	Width       int
	Height      int
	Recipe      Recipe
	MusicKey    string
	Latest      *Export
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Export is one render of a project.
type Export struct {
	UserID    uuid.UUID
	ID        uuid.UUID
	ProjectID uuid.UUID
	Status    Status
	Recipe    Recipe
	OutputKey string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Sentinel errors mapped by HTTP.
var (
	ErrNotFound = errors.New("not found")
	ErrNotReady = errors.New("download is not finished")
	ErrInvalid  = errors.New("invalid")
	ErrBusy     = errors.New("export in progress")
	ErrTooLarge = errors.New("file is too large")
)
