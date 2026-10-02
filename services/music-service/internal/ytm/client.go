// Package ytm is a small YouTube Music search client. It talks to YouTube's
// public innertube JSON endpoint (WEB_REMIX). The API key and client version
// are read from the music.youtube.com HTML at runtime. This is not derived
// from Metrolist or InnerTune (those are GPL-3.0 and are not vendored).
package ytm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultBase = "https://music.youtube.com"
	// Search filters are YouTube Music's own protobuf params for the songs,
	// albums and artists shelves. They are public request fields, not client source.
	paramSongs   = "EgWKAQIIAWoKEAkQBRAKEAMQBA=="
	paramAlbums  = "EgWKAQIYAWoKEAkQBRAKEAMQBA=="
	paramArtists = "EgWKAQIgAWoKEAkQBRAKEAMQBA=="
	userAgent    = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
)

var (
	reKey = regexp.MustCompile(`"INNERTUBE_API_KEY":"([^"]+)"`)
	reVer = regexp.MustCompile(`"INNERTUBE_CLIENT_VERSION":"([^"]+)"`)
)

// Client searches YouTube Music.
type Client struct {
	Base string
	HTTP *http.Client

	mu     sync.Mutex
	key    string
	ver    string
	bootAt time.Time
}

// New returns a client for music.youtube.com.
func New() *Client {
	return &Client{Base: defaultBase, HTTP: &http.Client{Timeout: 12 * time.Second}}
}

type innertube struct {
	Context struct {
		Client struct {
			ClientName    string `json:"clientName"`
			ClientVersion string `json:"clientVersion"`
			HL            string `json:"hl"`
			GL            string `json:"gl"`
		} `json:"client"`
	} `json:"context"`
	Query    string `json:"query,omitempty"`
	Params   string `json:"params,omitempty"`
	BrowseID string `json:"browseId,omitempty"`
}

// Search returns songs, albums and artists. A partial result is returned when
// at least one shelf succeeded.
func (c *Client) Search(ctx context.Context, q string) (Catalog, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return Catalog{}, errors.New("empty query")
	}
	type job struct {
		param string
		kind  string
	}
	jobs := []job{{paramSongs, "song"}, {paramAlbums, "album"}, {paramArtists, "artist"}}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		out  Catalog
		errs []error
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			items, err := c.searchFilter(ctx, q, j.param)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			switch j.kind {
			case "song":
				for _, it := range items {
					if !ValidVideoID(it.VideoID) || it.Title == "" {
						continue
					}
					out.Tracks = append(out.Tracks, Track{VideoID: it.VideoID, Title: it.Title, Artist: it.Artist, Album: it.Album, DurationSec: it.DurationSec, Thumb: it.Thumb})
					if len(out.Tracks) >= 10 {
						break
					}
				}
			case "album":
				for _, it := range items {
					if !strings.HasPrefix(it.BrowseID, "MPRE") || it.Title == "" {
						continue
					}
					out.Albums = append(out.Albums, Album{BrowseID: it.BrowseID, Title: it.Title, Artist: it.Artist, Year: it.Year, Thumb: it.Thumb})
					if len(out.Albums) >= 8 {
						break
					}
				}
			case "artist":
				for _, it := range items {
					if !strings.HasPrefix(it.BrowseID, "UC") || it.Title == "" {
						continue
					}
					out.Artists = append(out.Artists, Artist{BrowseID: it.BrowseID, Name: it.Title, Thumb: it.Thumb})
					if len(out.Artists) >= 8 {
						break
					}
				}
			}
		}(j)
	}
	wg.Wait()
	if len(out.Tracks)+len(out.Albums)+len(out.Artists) == 0 && len(errs) > 0 {
		return Catalog{}, errors.Join(errs...)
	}
	return out, nil
}

// Album lists the tracks of an album browse id (MPRE…).
func (c *Client) Album(ctx context.Context, browseID string) ([]Track, error) {
	if !strings.HasPrefix(browseID, "MPRE") || !ValidBrowseID(browseID) {
		return nil, errors.New("invalid album id")
	}
	body, err := c.post(ctx, "browse", innertubeBody(c, "", "", browseID))
	if err != nil {
		return nil, err
	}
	items, err := ParseItems(body)
	if err != nil {
		return nil, err
	}
	var out []Track
	seen := map[string]bool{}
	for _, it := range items {
		if !ValidVideoID(it.VideoID) || it.Title == "" || seen[it.VideoID] {
			continue
		}
		seen[it.VideoID] = true
		out = append(out, Track{VideoID: it.VideoID, Title: it.Title, Artist: it.Artist, Album: it.Album, DurationSec: it.DurationSec, Thumb: it.Thumb})
		if len(out) >= 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) searchFilter(ctx context.Context, q, param string) ([]Item, error) {
	body, err := c.post(ctx, "search", innertubeBody(c, q, param, ""))
	if err != nil {
		return nil, err
	}
	return ParseItems(body)
}

func innertubeBody(c *Client, q, param, browse string) innertube {
	c.mu.Lock()
	ver := c.ver
	c.mu.Unlock()
	var b innertube
	b.Context.Client.ClientName = "WEB_REMIX"
	b.Context.Client.ClientVersion = ver
	b.Context.Client.HL = "en"
	b.Context.Client.GL = "US"
	b.Query = q
	b.Params = param
	b.BrowseID = browse
	return b
}

func (c *Client) post(ctx context.Context, method string, body innertube) ([]byte, error) {
	key, ver, err := c.bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	if body.Context.Client.ClientVersion == "" {
		body.Context.Client.ClientVersion = ver
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimRight(c.base(), "/") + "/youtubei/v1/" + method)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("key", key)
	q.Set("prettyPrint", "false")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", c.base())
	req.Header.Set("Referer", c.base()+"/")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube music %s: status %d", method, resp.StatusCode)
	}
	return b, nil
}

func (c *Client) bootstrap(ctx context.Context) (key, ver string, err error) {
	c.mu.Lock()
	if c.key != "" && time.Since(c.bootAt) < 6*time.Hour {
		defer c.mu.Unlock()
		return c.key, c.ver, nil
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	html, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("youtube music home: status %d", resp.StatusCode)
	}
	km, vm := reKey.FindSubmatch(html), reVer.FindSubmatch(html)
	if km == nil || vm == nil {
		return "", "", errors.New("youtube music: innertube config not found")
	}
	c.mu.Lock()
	c.key, c.ver, c.bootAt = string(km[1]), string(vm[1]), time.Now()
	c.mu.Unlock()
	return string(km[1]), string(vm[1]), nil
}

func (c *Client) base() string {
	if c.Base == "" {
		return defaultBase
	}
	return strings.TrimRight(c.Base, "/")
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
