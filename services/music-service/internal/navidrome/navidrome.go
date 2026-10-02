// Package navidrome is a minimal Subsonic API client for Navidrome (spec §5.4):
// catalog listing for the sync worker, raw streaming with HTTP Range and cover
// art. Calls go through a circuit breaker; a background ping keeps Healthy()
// current so the API can answer "streaming unavailable" without waiting on
// timeouts while the catalog keeps working (spec §8).
package navidrome

import (
	"context"
	"crypto/md5" //nolint:gosec // Subsonic token auth is md5(password+salt) by protocol
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker"
)

// Errors.
var (
	ErrUnavailable = errors.New("navidrome unavailable")
	ErrNotFound    = errors.New("navidrome: not found")
)

const apiVersion = "1.16.1"

// Song is a Subsonic child entry (only the fields the catalog needs).
type Song struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Album       string `json:"album"`
	AlbumID     string `json:"albumId"`
	Artist      string `json:"artist"`
	ArtistID    string `json:"artistId"`
	Track       int    `json:"track"`
	Year        int    `json:"year"`
	Genre       string `json:"genre"`
	CoverArt    string `json:"coverArt"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	Duration    int    `json:"duration"`
	BitRate     int    `json:"bitRate"`
	MBID        string `json:"musicBrainzId"`
}

// Client talks to one Navidrome instance.
type Client struct {
	base    *url.URL
	user    string
	pass    string
	api     *http.Client // JSON calls (short timeout)
	media   *http.Client // streams / covers (header timeout only; bodies stream)
	cb      *gobreaker.CircuitBreaker
	healthy atomic.Bool
	log     *slog.Logger
}

// New builds a client. rawURL e.g. http://navidrome:4533.
func New(rawURL, user, pass string, log *slog.Logger) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("navidrome: bad url")
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
	}
	c := &Client{
		base: u, user: user, pass: pass, log: log,
		api:   &http.Client{Transport: tr, Timeout: 30 * time.Second},
		media: &http.Client{Transport: tr},
	}
	c.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "navidrome",
		Timeout:     15 * time.Second,
		ReadyToTrip: func(cnt gobreaker.Counts) bool { return cnt.ConsecutiveFailures >= 3 },
		// players abort range requests all the time (seek, track switch): not a failure
		IsSuccessful: func(err error) bool { return err == nil || errors.Is(err, context.Canceled) },
		OnStateChange: func(_ string, from, to gobreaker.State) {
			log.Warn("navidrome breaker", "from", from.String(), "to", to.String())
		},
	})
	c.healthy.Store(true) // optimistic until the first ping says otherwise
	return c, nil
}

// Healthy reports the last known state (ping loop + breaker).
func (c *Client) Healthy() bool { return c.healthy.Load() && c.cb.State() != gobreaker.StateOpen }

func (c *Client) endpoint(name string, q url.Values) string {
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)
	s := hex.EncodeToString(salt)
	sum := md5.Sum([]byte(c.pass + s)) //nolint:gosec // protocol-defined
	if q == nil {
		q = url.Values{}
	}
	q.Set("u", c.user)
	q.Set("t", hex.EncodeToString(sum[:]))
	q.Set("s", s)
	q.Set("v", apiVersion)
	q.Set("c", "tapenest-music")
	return c.base.JoinPath("rest", name).String() + "?" + q.Encode()
}

// do runs a request through the breaker; 5xx and network errors count as failures.
func (c *Client) do(hc *http.Client, req *http.Request) (*http.Response, error) {
	v, err := c.cb.Execute(func() (any, error) {
		resp, err := hc.Do(req) //nolint:bodyclose // returned to the caller
		if err != nil {
			return nil, redact(err)
		}
		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("navidrome: status %d", resp.StatusCode)
		}
		return resp, nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return v.(*http.Response), nil
}

// redact drops the request URL from transport errors: its query carries the
// Subsonic credentials (u, t = md5(password+salt), s), which must never reach logs.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		path := "?"
		if u, perr := url.Parse(ue.URL); perr == nil {
			path = u.Path
		}
		return fmt.Errorf("%s %s: %w", ue.Op, path, ue.Err)
	}
	return err
}

type envelope struct {
	Resp struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		SearchResult3 *struct {
			Song []Song `json:"song"`
		} `json:"searchResult3"`
		ScanStatus *struct {
			Scanning bool `json:"scanning"`
		} `json:"scanStatus"`
	} `json:"subsonic-response"`
}

func (c *Client) call(ctx context.Context, name string, q url.Values) (*envelope, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("f", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(name, q), nil)
	if err != nil {
		return nil, fmt.Errorf("navidrome request: %w", err)
	}
	resp, err := c.do(c.api, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&env); err != nil {
		return nil, fmt.Errorf("navidrome %s: decode: %w", name, err)
	}
	if env.Resp.Status != "ok" {
		if env.Resp.Error != nil && env.Resp.Error.Code == 70 {
			return nil, ErrNotFound
		}
		msg := "unknown error"
		if env.Resp.Error != nil {
			msg = env.Resp.Error.Message
		}
		return nil, fmt.Errorf("navidrome %s: %s", name, msg)
	}
	return &env, nil
}

// Ping checks connectivity and credentials.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	return err
}

// StartScan asks Navidrome to rescan the music folder (admin user).
func (c *Client) StartScan(ctx context.Context) error {
	_, err := c.call(ctx, "startScan", nil)
	return err
}

// Scanning reports whether a library scan is running.
func (c *Client) Scanning(ctx context.Context) (bool, error) {
	env, err := c.call(ctx, "getScanStatus", nil)
	if err != nil {
		return false, err
	}
	return env.Resp.ScanStatus != nil && env.Resp.ScanStatus.Scanning, nil
}

// Songs returns one page of all songs (search3 with an empty query lists everything).
func (c *Client) Songs(ctx context.Context, offset, count int) ([]Song, error) {
	env, err := c.call(ctx, "search3", url.Values{
		"query": {""}, "artistCount": {"0"}, "albumCount": {"0"},
		"songCount": {strconv.Itoa(count)}, "songOffset": {strconv.Itoa(offset)},
	})
	if err != nil {
		return nil, err
	}
	if env.Resp.SearchResult3 == nil {
		return nil, nil
	}
	return env.Resp.SearchResult3.Song, nil
}

// passRequestHeaders are forwarded to Navidrome for conditional/range requests.
var passRequestHeaders = []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"}

// Stream opens the original file (format=raw, no transcoding) honouring Range.
// The caller must close the body. JSON bodies are Subsonic errors (e.g. deleted file).
func (c *Client) Stream(ctx context.Context, id string, h http.Header) (*http.Response, error) {
	return c.media1(ctx, "stream", url.Values{"id": {id}, "format": {"raw"}}, h)
}

// CoverArt opens cover art resized to size px.
func (c *Client) CoverArt(ctx context.Context, id string, size int) (*http.Response, error) {
	return c.media1(ctx, "getCoverArt", url.Values{"id": {id}, "size": {strconv.Itoa(size)}}, nil)
}

func (c *Client) media1(ctx context.Context, name string, q url.Values, h http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(name, q), nil)
	if err != nil {
		return nil, fmt.Errorf("navidrome request: %w", err)
	}
	for _, k := range passRequestHeaders {
		if v := h.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.do(c.media, req)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode == http.StatusNotFound || strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/xml") {
		_ = resp.Body.Close()
		return nil, ErrNotFound
	}
	return resp, nil
}

// EnsureAdmin creates the configured user as Navidrome's first admin on a fresh
// instance (POST /auth/createAdmin is only accepted while no user exists, so the
// call is idempotent). This replaces ND_DEVAUTOCREATEADMINPASSWORD, which logs
// the password in clear text. Returns true when the admin was created now.
func (c *Client) EnsureAdmin(ctx context.Context) (bool, error) {
	body, err := json.Marshal(map[string]string{"username": c.user, "password": c.pass})
	if err != nil {
		return false, fmt.Errorf("navidrome admin: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath("auth", "createAdmin").String(), strings.NewReader(string(body)))
	if err != nil {
		return false, fmt.Errorf("navidrome admin: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.api.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnavailable, redact(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode >= 500:
		return false, fmt.Errorf("%w: createAdmin status %d", ErrUnavailable, resp.StatusCode)
	default: // 403: users already exist
		return false, nil
	}
}

// RunHealth pings every interval and updates Healthy() until ctx is done.
func (c *Client) RunHealth(ctx context.Context, every time.Duration) {
	check := func() {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.Ping(pctx)
		cancel()
		was := c.healthy.Swap(err == nil)
		if was != (err == nil) {
			c.log.Info("navidrome health changed", "healthy", err == nil, "err", err)
		}
	}
	check()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}
