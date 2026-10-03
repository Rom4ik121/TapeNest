// Package domain holds music-service types and pure logic (cursors, validation).
package domain

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Errors mapped to HTTP statuses by the transport.
var (
	ErrNotFound = errors.New("not found")
	// ErrStreamingUnavailable: Navidrome is down; catalog and library keep working (spec §8).
	ErrStreamingUnavailable = errors.New("streaming unavailable")
)

// InvalidError is a 400 with a user-facing message.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Invalid builds an InvalidError.
func Invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

// Track is the catalog entry as the contract exposes it (waveplayer.openapi.yaml).
type Track struct {
	ID          uuid.UUID
	Title       string
	Artist      string
	Album       string // "" → null
	CoverArtID  string // "" → no cover
	DurationSec int
	Liked       bool
	Remote      bool      // YouTube Music row: played from YouTube, not a local file (ADR 0012)
	ArtistID    uuid.UUID // internal (wave session bookkeeping), not in the contract
}

// Album is an album of the unified catalog (library or MusicBrainz placeholder).
type Album struct {
	ID            uuid.UUID
	Title         string
	Artist        string
	ArtistID      *uuid.UUID
	Year          int
	CoverArtID    string
	Remote        bool
	MBID          *uuid.UUID
	YouTubeBrowse string // MPRE… album, not part of the HTTP contract
}

// Artist is an artist of the unified catalog.
type Artist struct {
	ID            uuid.UUID
	Name          string
	Remote        bool
	MBID          *uuid.UUID
	YouTubeBrowse string // UC… channel, not part of the HTTP contract
}

// SearchResult is the unified search (library, then YouTube Music).
type SearchResult struct {
	Tracks  []Track
	Albums  []Album
	Artists []Artist
}

// ErrNoSources means the track has neither a library file nor a YouTube id.
var ErrNoSources = errors.New("no sources found")

// Wave strategies (response field "strategy", header X-Wave-Strategy).
const (
	StrategyReco     = "reco"
	StrategyFallback = "fallback"
)

// WaveModes accepted by POST /wave/sessions (ADR 0010 §6).
var WaveModes = map[string]bool{"default": true, "calm": true, "energetic": true, "discover": true, "favorites": true}

// WaveReason explains why a track is in the wave ("because you liked X").
type WaveReason struct {
	Kind       string
	RefTrackID string
	RefTitle   string
	RefArtist  string
	Artist     string
	Genre      string
	Tag        string
}

// WaveTrack is a wave entry: the track plus an optional reason.
type WaveTrack struct {
	Track
	Reason *WaveReason
}

// WaveBatch is one batch of the wave.
type WaveBatch struct {
	Tracks   []WaveTrack
	Strategy string
	Mode     string
}

// Page is a cursor page of tracks (never offset, spec §7.2).
type Page struct {
	Items []Track
	Next  *string
}

// Playlist is a user folder/playlist.
type Playlist struct {
	ID         uuid.UUID
	Title      string
	TrackCount int
	CreatedAt  time.Time
}

// Position is a saved playback position.
type Position struct {
	TrackID     uuid.UUID
	PositionSec float64
	UpdatedAt   time.Time
}

// Cursor is the opaque keyset position: (score | time, id).
type Cursor struct {
	Score *float64   `json:"s,omitempty"`
	At    *time.Time `json:"t,omitempty"`
	ID    uuid.UUID  `json:"i"`
}

// Encode returns the opaque string.
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c) //nolint:errchkjson // plain struct, cannot fail
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor ("" → nil).
func DecodeCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) > 256 {
		return nil, Invalid("invalid cursor")
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == uuid.Nil || (c.Score == nil && c.At == nil) {
		return nil, Invalid("invalid cursor")
	}
	return &c, nil
}

// Limit clamps a page size (default 20, max 100).
func Limit(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > 100:
		return 100
	}
	return n
}

// PlaylistTitle trims and validates a playlist title (1..100 chars).
func PlaylistTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", Invalid("title is required")
	}
	if utf8.RuneCountInString(s) > 100 {
		return "", Invalid("title must be at most 100 characters")
	}
	return s, nil
}

// MaxPositionSec bounds positions and listen events (a day is far beyond any track).
const MaxPositionSec = 86400

// SearchQuery normalizes a search string; returns the query and an escaped LIKE pattern.
func SearchQuery(q string) (string, string, error) {
	q = strings.ToLower(strings.Join(strings.Fields(q), " "))
	if q == "" {
		return "", "", Invalid("q is required")
	}
	if utf8.RuneCountInString(q) > 100 {
		return "", "", Invalid("q must be at most 100 characters")
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return q, "%" + esc + "%", nil
}

// ParseID parses a UUID path parameter.
func ParseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, Invalid("invalid %s", what)
	}
	return id, nil
}
