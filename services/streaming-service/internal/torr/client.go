// Package torr is a small TorrServer REST client (spec §5.5).
// It is used only when a catalog file already has a magnet and CONTENT_SOURCES includes p2p.
package torr

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

	"github.com/sony/gobreaker"
)

// ErrUnavailable means TorrServer is down or its breaker is open.
var ErrUnavailable = errors.New("torrserver unavailable")

// Client talks to TorrServer on the internal network.
type Client struct {
	base    *url.URL
	http    *http.Client
	breaker *gobreaker.CircuitBreaker
}

// New returns nil when rawURL is empty (TorrServer not deployed).
func New(rawURL string) (*Client, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("TORRSERVER_URL is invalid")
	}
	return &Client{
		base: u,
		http: &http.Client{Timeout: 8 * time.Second},
		breaker: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name: "torrserver", Timeout: 15 * time.Second,
			ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 3 },
		}),
	}, nil
}

// Add asks TorrServer to add a magnet. It does not choose the magnet.
func (c *Client) Add(ctx context.Context, magnet string) error {
	if c == nil {
		return ErrUnavailable
	}
	body, _ := json.Marshal(map[string]string{"action": "add", "link": magnet})
	_, err := c.post(ctx, "/torrents", body)
	return err
}

// Stat is a warm-up snapshot.
type Stat struct {
	Hash        string
	PreloadPct  float64
	Peers       int
	DownloadBps float64
}

type torrList struct {
	Hash        string `json:"hash"`
	PreloadSize int64  `json:"preloaded_bytes"`
	TorrentSize int64  `json:"torrent_size"`
	Download    int64  `json:"download_speed"`
	Peers       int    `json:"connected_seeders"`
	Stat        int    `json:"stat"`
}

// StatOf returns progress for a magnet hash. TorrServer's get payload varies by version;
// missing fields stay zero and the caller keeps the catalog up.
func (c *Client) StatOf(ctx context.Context, hash string) (Stat, error) {
	if c == nil {
		return Stat{}, ErrUnavailable
	}
	body, _ := json.Marshal(map[string]string{"action": "list"})
	raw, err := c.post(ctx, "/torrents", body)
	if err != nil {
		return Stat{}, err
	}
	var rows []torrList
	if err := json.Unmarshal(raw, &rows); err != nil {
		var wrap struct {
			Torrents []torrList `json:"torrents"`
		}
		if err2 := json.Unmarshal(raw, &wrap); err2 != nil {
			return Stat{}, fmt.Errorf("torrserver list: %w", err)
		}
		rows = wrap.Torrents
	}
	for _, r := range rows {
		if strings.EqualFold(r.Hash, hash) {
			pct := 0.0
			if r.TorrentSize > 0 {
				pct = float64(r.PreloadSize) / float64(r.TorrentSize) * 100
				if pct > 100 {
					pct = 100
				}
			}
			return Stat{Hash: r.Hash, PreloadPct: pct, Peers: r.Peers, DownloadBps: float64(r.Download)}, nil
		}
	}
	return Stat{}, ErrUnavailable
}

func (c *Client) post(ctx context.Context, p string, body []byte) ([]byte, error) {
	v, err := c.breaker.Execute(func() (any, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath(p).String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 500 || resp.StatusCode == 0 {
			return nil, ErrUnavailable
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("torrserver status %d", resp.StatusCode)
		}
		return b, nil
	})
	if err != nil {
		if errors.Is(err, ErrUnavailable) || errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, ErrUnavailable
		}
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	b, _ := v.([]byte)
	return b, nil
}
