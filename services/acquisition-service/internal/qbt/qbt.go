// Package qbt is a qBittorrent WebUI API v2 client (qBittorrent ≥ 5): session
// cookie login (re-login on 403), preferences, categories, add with file
// priorities, sequential / first-last-piece toggles, piece states for streaming.
// Credentials are never logged; errors are scrubbed.
package qbt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// Errors.
var (
	ErrUnavailable = errors.New("qbittorrent unavailable")
	ErrAuth        = errors.New("qbittorrent: login failed")
	ErrNotFound    = errors.New("qbittorrent: torrent not found")
)

// File priorities.
const (
	PrioSkip    = 0
	PrioNormal  = 1
	PrioHigh    = 6
	PrioMaximal = 7
)

// Client is safe for concurrent use.
type Client struct {
	base       string
	user, pass string
	http       *http.Client
	mu         sync.Mutex
	loggedIn   bool
}

// New builds a client.
func New(rawURL, user, pass string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("qbt: bad url")
	}
	jar, _ := cookiejar.New(nil)
	return &Client{base: u.String(), user: user, pass: pass, http: &http.Client{Timeout: 20 * time.Second, Jar: jar}}, nil
}

func (c *Client) login(ctx context.Context) error {
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return redact.Error(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, redact.Error(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent) || strings.Contains(string(b), "Fails") {
		return ErrAuth
	}
	c.loggedIn = true
	return nil
}

// call performs a request; body is form values or a prepared multipart body.
func (c *Client) call(ctx context.Context, method, path string, form url.Values, mp *multipartBody, out any) error {
	c.mu.Lock()
	if !c.loggedIn {
		if err := c.login(ctx); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	c.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		var body io.Reader
		ctype := ""
		u := c.base + path
		switch {
		case mp != nil:
			body, ctype = bytes.NewReader(mp.data), mp.contentType
		case method == http.MethodPost:
			body, ctype = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
		case len(form) > 0:
			u += "?" + form.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return redact.Error(err)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Referer", c.base)
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, redact.Error(err))
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("%w: read body", ErrUnavailable)
		}
		switch {
		case resp.StatusCode == http.StatusForbidden && attempt == 0:
			c.mu.Lock()
			c.loggedIn = false
			err := c.login(ctx)
			c.mu.Unlock()
			if err != nil {
				return err
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode >= 300:
			return fmt.Errorf("%w: %s status %d", ErrUnavailable, path, resp.StatusCode)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("qbt: bad json from %s", path)
			}
		}
		return nil
	}
	return ErrAuth
}

// Version returns the application version (health check).
func (c *Client) Version(ctx context.Context) (string, error) {
	c.mu.Lock()
	if !c.loggedIn {
		if err := c.login(ctx); err != nil {
			c.mu.Unlock()
			return "", err
		}
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v2/app/version", nil)
	if err != nil {
		return "", redact.Error(err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, redact.Error(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode == http.StatusForbidden {
		c.mu.Lock()
		c.loggedIn = false
		c.mu.Unlock()
		return "", ErrAuth
	}
	return strings.TrimSpace(string(b)), nil
}

// SetPreferences applies preferences (JSON object of WebUI preference keys).
func (c *Client) SetPreferences(ctx context.Context, prefs map[string]any) error {
	b, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/api/v2/app/setPreferences", url.Values{"json": {string(b)}}, nil, nil)
}

// Preferences returns the current preferences.
func (c *Client) Preferences(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.call(ctx, http.MethodGet, "/api/v2/app/preferences", nil, nil, &out)
	return out, err
}

// EnsureCategory creates a category (409 = exists → edit save path).
func (c *Client) EnsureCategory(ctx context.Context, name, savePath string) error {
	var cats map[string]struct {
		SavePath string `json:"savePath"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/torrents/categories", nil, nil, &cats); err != nil {
		return err
	}
	f := url.Values{"category": {name}, "savePath": {savePath}}
	if cur, ok := cats[name]; ok {
		if cur.SavePath == savePath {
			return nil
		}
		return c.call(ctx, http.MethodPost, "/api/v2/torrents/editCategory", f, nil, nil)
	}
	return c.call(ctx, http.MethodPost, "/api/v2/torrents/createCategory", f, nil, nil)
}

type multipartBody struct {
	data        []byte
	contentType string
}

// AddOptions for AddTorrent.
type AddOptions struct {
	Category string
	Tags     []string
	Stopped  bool
}

// AddTorrent adds a .torrent file or a magnet link.
func (c *Client) AddTorrent(ctx context.Context, torrentFile []byte, magnet string, o AddOptions) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if len(torrentFile) > 0 {
		fw, err := w.CreateFormFile("torrents", "release.torrent")
		if err != nil {
			return err
		}
		if _, err := fw.Write(torrentFile); err != nil {
			return err
		}
	} else {
		_ = w.WriteField("urls", magnet)
	}
	_ = w.WriteField("category", o.Category)
	_ = w.WriteField("tags", strings.Join(o.Tags, ","))
	_ = w.WriteField("stopped", strconv.FormatBool(o.Stopped))
	_ = w.WriteField("paused", strconv.FormatBool(o.Stopped)) // qBittorrent < 5
	_ = w.WriteField("contentLayout", "Original")
	_ = w.WriteField("autoTMM", "true")
	if err := w.Close(); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/api/v2/torrents/add", nil, &multipartBody{data: buf.Bytes(), contentType: w.FormDataContentType()}, nil)
}

// Torrent is /torrents/info.
type Torrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Size         int64   `json:"size"`
	Downloaded   int64   `json:"downloaded"`
	DLSpeed      int64   `json:"dlspeed"`
	NumSeeds     int     `json:"num_seeds"`
	ETA          int64   `json:"eta"`
	SavePath     string  `json:"save_path"`
	DownloadPath string  `json:"download_path"`
	ContentPath  string  `json:"content_path"`
	Category     string  `json:"category"`
	Tags         string  `json:"tags"`
	Sequential   bool    `json:"seq_dl"`
	FirstLast    bool    `json:"f_l_piece_prio"`
	CompletionOn int64   `json:"completion_on"`
	AddedOn      int64   `json:"added_on"`
	Ratio        float64 `json:"ratio"`
	SeedingTime  int64   `json:"seeding_time"`
}

// Torrents lists torrents filtered by category and/or hashes.
func (c *Client) Torrents(ctx context.Context, category string, hashes ...string) ([]Torrent, error) {
	f := url.Values{}
	if category != "" {
		f.Set("category", category)
	}
	if len(hashes) > 0 {
		f.Set("hashes", strings.Join(hashes, "|"))
	}
	var out []Torrent
	err := c.call(ctx, http.MethodGet, "/api/v2/torrents/info", f, nil, &out)
	return out, err
}

// Torrent returns one torrent.
func (c *Client) Torrent(ctx context.Context, hash string) (Torrent, error) {
	ts, err := c.Torrents(ctx, "", hash)
	if err != nil {
		return Torrent{}, err
	}
	if len(ts) == 0 {
		return Torrent{}, ErrNotFound
	}
	return ts[0], nil
}

// File is /torrents/files (Index = torrent order).
type File struct {
	Index      int     `json:"index"`
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Progress   float64 `json:"progress"`
	Priority   int     `json:"priority"`
	PieceRange []int   `json:"piece_range"`
}

// Files lists a torrent's files.
func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	var out []File
	err := c.call(ctx, http.MethodGet, "/api/v2/torrents/files", url.Values{"hash": {hash}}, nil, &out)
	return out, err
}

// PieceSize returns the torrent piece length.
func (c *Client) PieceSize(ctx context.Context, hash string) (int64, error) {
	var p struct {
		PieceSize int64 `json:"piece_size"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/torrents/properties", url.Values{"hash": {hash}}, nil, &p); err != nil {
		return 0, err
	}
	return p.PieceSize, nil
}

// PieceStates returns per-piece flags: 0 not downloaded, 1 downloading, 2 downloaded.
func (c *Client) PieceStates(ctx context.Context, hash string) ([]int, error) {
	var out []int
	err := c.call(ctx, http.MethodGet, "/api/v2/torrents/pieceStates", url.Values{"hash": {hash}}, nil, &out)
	return out, err
}

// SetFilePriority sets the priority of the given file indexes.
func (c *Client) SetFilePriority(ctx context.Context, hash string, ids []int, prio int) error {
	if len(ids) == 0 {
		return nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return c.call(ctx, http.MethodPost, "/api/v2/torrents/filePrio", url.Values{"hash": {hash}, "id": {strings.Join(parts, "|")}, "priority": {strconv.Itoa(prio)}}, nil, nil)
}

// SetSequential makes sequential download on/off (the API only toggles).
func (c *Client) SetSequential(ctx context.Context, hash string, on bool) error {
	t, err := c.Torrent(ctx, hash)
	if err != nil {
		return err
	}
	if t.Sequential != on {
		if err := c.call(ctx, http.MethodPost, "/api/v2/torrents/toggleSequentialDownload", url.Values{"hashes": {hash}}, nil, nil); err != nil {
			return err
		}
	}
	if t.FirstLast != on {
		return c.call(ctx, http.MethodPost, "/api/v2/torrents/toggleFirstLastPiecePrio", url.Values{"hashes": {hash}}, nil, nil)
	}
	return nil
}

// Start resumes a torrent (qBittorrent 5 "start", falling back to 4.x "resume").
func (c *Client) Start(ctx context.Context, hash string) error {
	err := c.call(ctx, http.MethodPost, "/api/v2/torrents/start", url.Values{"hashes": {hash}}, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return c.call(ctx, http.MethodPost, "/api/v2/torrents/resume", url.Values{"hashes": {hash}}, nil, nil)
	}
	return err
}

// Delete removes a torrent (optionally with its data).
func (c *Client) Delete(ctx context.Context, hash string, withFiles bool) error {
	return c.call(ctx, http.MethodPost, "/api/v2/torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {strconv.FormatBool(withFiles)}}, nil, nil)
}

// Reannounce asks trackers for more peers (after a user starts streaming).
func (c *Client) Reannounce(ctx context.Context, hash string) error {
	return c.call(ctx, http.MethodPost, "/api/v2/torrents/reannounce", url.Values{"hashes": {hash}}, nil, nil)
}
