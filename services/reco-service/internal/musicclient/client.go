// Package musicclient calls music-service's internal API (/internal/v1/*):
// catalog export, one-off interactions backfill and raw audio for analysis.
// reco-service never talks to Navidrome and never holds its credentials.
package musicclient

import (
	"bufio"
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
)

// Client is the music-service internal client.
type Client struct {
	base  string
	token string
	api   *http.Client
	media *http.Client
}

// New creates a client.
func New(base, token string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"), token: token,
		api:   &http.Client{Timeout: 30 * time.Second},
		media: &http.Client{Timeout: 3 * time.Minute},
	}
}

// Track is one catalog export item.
type Track struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	ArtistID    uuid.UUID  `json:"artistId"`
	Artist      string     `json:"artist"`
	AlbumID     *uuid.UUID `json:"albumId"`
	Album       string     `json:"album"`
	Genre       string     `json:"genre"`
	Year        *int32     `json:"year"`
	DurationSec int        `json:"durationSec"`
	Popularity  float64    `json:"popularity"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Interaction is one backfill line.
type Interaction struct {
	Kind        string    `json:"kind"`
	UserID      uuid.UUID `json:"userId"`
	TrackID     uuid.UUID `json:"trackId"`
	At          time.Time `json:"at"`
	PositionSec float64   `json:"positionSec"`
	Completed   bool      `json:"completed"`
	EventID     string    `json:"eventId"`
}

func (c *Client) get(ctx context.Context, hc *http.Client, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, errors.New("music: build request")
	}
	req.Header.Set("X-Internal-Token", c.token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, scrub(err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("music: status %d", resp.StatusCode)
	}
	return resp, nil
}

// CatalogPage returns one page and the next cursor (nil at the end).
func (c *Client) CatalogPage(ctx context.Context, after *uuid.UUID, limit int) ([]Track, *uuid.UUID, error) {
	q := url.Values{"limit": {fmt.Sprint(limit)}}
	if after != nil {
		q.Set("after", after.String())
	}
	resp, err := c.get(ctx, c.api, "/internal/v1/catalog?"+q.Encode())
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Items []Track `json:"items"`
		Next  *string `json:"next"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return nil, nil, errors.New("music: invalid catalog response")
	}
	var next *uuid.UUID
	if out.Next != nil {
		id, err := uuid.Parse(*out.Next)
		if err != nil {
			return nil, nil, errors.New("music: invalid catalog cursor")
		}
		next = &id
	}
	return out.Items, next, nil
}

// Interactions streams the backfill export.
func (c *Client) Interactions(ctx context.Context, days int, fn func(Interaction) error) error {
	resp, err := c.get(ctx, c.media, fmt.Sprintf("/internal/v1/interactions?days=%d", days))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var it Interaction
		if err := json.Unmarshal(sc.Bytes(), &it); err != nil {
			continue
		}
		if err := fn(it); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return errors.New("music: interactions stream interrupted")
	}
	return nil
}

// Audio opens the raw audio of a track (caller closes).
func (c *Client) Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, error) {
	resp, err := c.get(ctx, c.media, "/internal/v1/tracks/"+id.String()+"/audio")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// scrub drops URLs from transport errors (never log internal URLs / tokens).
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("music: timeout")
		}
		var op *net.OpError
		if errors.As(ue.Err, &op) {
			return fmt.Errorf("%s: transport: %s failed", "music", op.Op)
		}
		return errors.New("music: transport error")
	}
	return errors.New("music: transport error")
}
