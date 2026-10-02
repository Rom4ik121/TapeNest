// Package reco is music-service's client of reco-service (ADR 0010): one call per
// wave batch, bounded by a short timeout and a circuit breaker so My Wave falls
// back to the local heuristic (ADR 0009 §6) when reco is slow or down.
package reco

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"
)

// Feedback is one session signal.
type Feedback struct {
	TrackID uuid.UUID `json:"trackId"`
	Action  string    `json:"action"`
}

// NextRequest asks for the next batch.
type NextRequest struct {
	UserID    uuid.UUID   `json:"userId"`
	SessionID uuid.UUID   `json:"sessionId"`
	Mode      string      `json:"mode,omitempty"`
	Limit     int         `json:"limit"`
	Exclude   []uuid.UUID `json:"exclude"`
	Recent    []uuid.UUID `json:"recent"`
	Feedback  []Feedback  `json:"feedback"`
}

// Reason explains a pick.
type Reason struct {
	Kind       string `json:"kind"`
	RefTrackID string `json:"refTrackId,omitempty"`
	RefTitle   string `json:"refTitle,omitempty"`
	RefArtist  string `json:"refArtist,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// Pick is one recommended track.
type Pick struct {
	TrackID uuid.UUID `json:"trackId"`
	Score   float64   `json:"score"`
	Source  string    `json:"source"`
	Reason  *Reason   `json:"reason,omitempty"`
}

type nextResponse struct {
	ModelVersion int64  `json:"modelVersion"`
	Tracks       []Pick `json:"tracks"`
}

// ErrDisabled means RECO_SERVICE_URL is not configured.
var ErrDisabled = errors.New("reco disabled")

// Client calls reco-service.
type Client struct {
	base    string
	token   string
	http    *http.Client
	timeout time.Duration
	cb      *gobreaker.CircuitBreaker
}

// Options configure the client.
type Options struct {
	BaseURL      string
	Token        string
	Timeout      time.Duration // per call (default 400ms)
	FailuresTrip uint32        // consecutive failures that open the breaker (default 3)
	OpenFor      time.Duration // how long the breaker stays open (default 30s)
}

// New creates a client; a nil client (empty BaseURL) is valid and always returns ErrDisabled.
func New(o Options) (*Client, error) {
	if o.BaseURL == "" {
		return nil, nil //nolint:nilnil // disabled by configuration
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("RECO_SERVICE_URL must be an absolute http(s) URL")
	}
	if o.Timeout <= 0 {
		o.Timeout = 400 * time.Millisecond
	}
	if o.FailuresTrip == 0 {
		o.FailuresTrip = 3
	}
	if o.OpenFor <= 0 {
		o.OpenFor = 30 * time.Second
	}
	trip := o.FailuresTrip
	return &Client{
		base: strings.TrimRight(o.BaseURL, "/"), token: o.Token, timeout: o.Timeout,
		http: &http.Client{Timeout: o.Timeout + 100*time.Millisecond},
		cb: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name: "reco", MaxRequests: 1, Timeout: o.OpenFor,
			ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= trip },
		}),
	}, nil
}

// State is the breaker state ("disabled" for a nil client).
func (c *Client) State() string {
	if c == nil {
		return "disabled"
	}
	return c.cb.State().String()
}

// Next returns the next batch of picks.
func (c *Client) Next(ctx context.Context, req NextRequest) ([]Pick, error) {
	if c == nil {
		return nil, ErrDisabled
	}
	res, err := c.cb.Execute(func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		body, _ := json.Marshal(req) //nolint:errchkjson // plain struct
		hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/v1/wave/next", bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("reco: build request")
		}
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("X-Internal-Token", c.token)
		resp, err := c.http.Do(hr)
		if err != nil {
			return nil, scrub(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			return nil, fmt.Errorf("reco: status %d", resp.StatusCode)
		}
		var out nextResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
			return nil, errors.New("reco: invalid response")
		}
		return out.Tracks, nil
	})
	if err != nil {
		return nil, err
	}
	picks, _ := res.([]Pick)
	return picks, nil
}

// scrub drops the URL from transport errors (never log internal URLs/tokens).
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("reco: timeout")
		}
		var op *net.OpError
		if errors.As(ue.Err, &op) {
			return fmt.Errorf("%s: transport: %s failed", "reco", op.Op)
		}
		return errors.New("reco: transport error")
	}
	return errors.New("reco: transport error")
}
