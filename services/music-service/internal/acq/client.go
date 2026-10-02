// Package acq is music-service's client for acquisition-service (ADR 0011):
// MusicBrainz search/lookups, acquire-on-play/like and partial-file streaming.
package acq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Errors.
var (
	ErrNotFound    = errors.New("acq: not found")
	ErrQuota       = errors.New("acq: quota exceeded")
	ErrDisabled    = errors.New("acq: disabled")
	ErrStorageFull = errors.New("acq: storage full")
	ErrUnavailable = errors.New("acq: unavailable")
)

// Artist is a MusicBrainz artist (types below mirror acquisition-service's JSON).
type Artist struct {
	MBID           uuid.UUID `json:"mbid"`
	Name           string    `json:"name"`
	Disambiguation string    `json:"disambiguation"`
	Score          int       `json:"score"`
}

// ReleaseGroup is an album/EP/single.
type ReleaseGroup struct {
	MBID       uuid.UUID `json:"mbid"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Year       int       `json:"year"`
	ArtistMBID uuid.UUID `json:"artistMbid"`
	Artist     string    `json:"artist"`
	Score      int       `json:"score"`
}

// Recording is a track resolved to its best release group.
type Recording struct {
	MBID             uuid.UUID `json:"mbid"`
	Title            string    `json:"title"`
	LengthMS         int       `json:"lengthMs"`
	ArtistMBID       uuid.UUID `json:"artistMbid"`
	Artist           string    `json:"artist"`
	ReleaseGroupMBID uuid.UUID `json:"releaseGroupMbid"`
	Album            string    `json:"album"`
	Year             int       `json:"year"`
	Score            int       `json:"score"`
}

// Track is a tracklist entry.
type Track struct {
	RecordingMBID uuid.UUID `json:"recordingMbid"`
	Title         string    `json:"title"`
	Disc          int       `json:"disc"`
	Position      int       `json:"position"`
	LengthMS      int       `json:"lengthMs"`
	Artist        string    `json:"artist"`
}

// Album is a release group with its tracklist.
type Album struct {
	ReleaseGroup
	ReleaseMBID uuid.UUID `json:"releaseMbid"`
	Tracks      []Track   `json:"tracks"`
}

// SearchResult is the external half of the unified search.
type SearchResult struct {
	Artists    []Artist       `json:"artists"`
	Albums     []ReleaseGroup `json:"albums"`
	Recordings []Recording    `json:"recordings"`
}

// ArtistView is an artist with its discography.
type ArtistView struct {
	Artist Artist         `json:"artist"`
	Albums []ReleaseGroup `json:"albums"`
}

// Status is the acquisition state of a recording.
type Status struct {
	RequestID uuid.UUID `json:"requestId"`
	State     string    `json:"state"`
	Progress  float64   `json:"progress"`
	ErrorCode string    `json:"errorCode"`
	Stream    *struct {
		Ready    bool `json:"ready"`
		Imported bool `json:"imported"`
	} `json:"stream"`
}

// Ready reports whether playback can start.
func (s Status) Ready() bool { return s.Stream != nil && s.Stream.Ready }

// AcquireRequest asks for a release group (optionally a recording first).
type AcquireRequest struct {
	UserID           uuid.UUID `json:"userId"`
	ReleaseGroupMBID uuid.UUID `json:"releaseGroupMbid"`
	RecordingMBID    uuid.UUID `json:"recordingMbid,omitempty"`
	Title            string    `json:"title,omitempty"`
	Reason           string    `json:"reason"`
}

// Client talks to acquisition-service.
type Client struct {
	base   string
	token  string
	api    *http.Client
	stream *http.Client
}

// New builds a client ("" base → nil: acquisition not configured).
func New(base, token string) *Client {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	return &Client{
		base: strings.TrimRight(base, "/"), token: token,
		api:    &http.Client{Timeout: 10 * time.Second},
		stream: &http.Client{}, // long-lived partial-file streams; bounded by the request context
	}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Token", c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.api.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // never echo URLs
		}
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		if out == nil {
			return nil
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrQuota
	case http.StatusInsufficientStorage:
		return ErrStorageFull
	case http.StatusServiceUnavailable:
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Code == "DISABLED" {
			return ErrDisabled
		}
		return fmt.Errorf("%w: status 503", ErrUnavailable)
	default:
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
}

// Search queries MusicBrainz through acquisition-service.
func (c *Client) Search(ctx context.Context, q string, limit int) (SearchResult, error) {
	var out SearchResult
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/internal/v1/search?q=%s&limit=%d", url.QueryEscape(q), limit), nil, &out)
	return out, err
}

// Album returns a release group's tracklist.
func (c *Client) Album(ctx context.Context, rg uuid.UUID) (Album, error) {
	var out Album
	err := c.do(ctx, http.MethodGet, "/internal/v1/albums/"+rg.String(), nil, &out)
	return out, err
}

// Artist returns an artist's discography.
func (c *Client) Artist(ctx context.Context, id uuid.UUID) (ArtistView, error) {
	var out ArtistView
	err := c.do(ctx, http.MethodGet, "/internal/v1/artists/"+id.String(), nil, &out)
	return out, err
}

// Acquire starts (or joins) an acquisition and reports readiness.
func (c *Client) Acquire(ctx context.Context, r AcquireRequest) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodPost, "/internal/v1/acquisitions", r, &out)
	return out, err
}

// Admin proxies a read-only admin path (JSON passthrough).
func (c *Client) Admin(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	var out json.RawMessage
	p := "/internal/v1/admin/" + strings.TrimPrefix(path, "/")
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	return out, err
}

// Stream opens a recording (Range headers forwarded). The caller closes the body.
func (c *Client) Stream(ctx context.Context, rec uuid.UUID, h http.Header, method string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/internal/v1/recordings/"+rec.String()+"/stream", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", c.token)
	for _, k := range []string{"Range", "If-Range"} {
		if v := h.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: stream", ErrUnavailable)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 500 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: stream status %d", ErrUnavailable, resp.StatusCode)
	}
	return resp, nil
}
