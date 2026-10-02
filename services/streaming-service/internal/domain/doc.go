// Package domain holds the cinema catalog types (docs/api/cinema.openapi.yaml).
package domain

import (
	"time"

	"github.com/google/uuid"
)

// TitleKind is movie or series.
type TitleKind string

// KindMovie and KindSeries are the catalog kinds.
const (
	KindMovie  TitleKind = "movie"
	KindSeries TitleKind = "series"
)

// Summary is a catalog card.
type Summary struct {
	ID            uuid.UUID
	Kind          TitleKind
	Title         string
	OriginalTitle *string
	Year          *int
	Rating        *float64
	Genres        []string
	InWatchlist   bool
	Sort          int
	WatchedAt     time.Time
}

// File is one playable version inside a title (quality or episode).
type File struct {
	ID          uuid.UUID
	Name        string
	Season      *int
	Episode     *int
	Quality     string
	SizeBytes   int64
	DurationSec *float64
	Magnet      string
	Sort        int
}

// Title is a card plus files.
type Title struct {
	Summary
	Description string
	RuntimeMin  *int
	Files       []File
}

// Position is a saved watch position.
type Position struct {
	TitleID     uuid.UUID
	FileID      uuid.UUID
	PositionSec float64
	DurationSec float64
	UpdatedAt   time.Time
}

// ContinueItem is one unfinished watch.
type ContinueItem struct {
	Title    Summary
	File     File
	Position Position
}

// Session is a playback session (licensed preview or a failed/p2p attempt).
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TitleID   uuid.UUID
	FileID    uuid.UUID
	Mode      string // preview | failed
	Err       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Audit is one admin action.
type Audit struct {
	ID        int64
	Actor     uuid.UUID
	Action    string
	Detail    string
	CreatedAt time.Time
}
