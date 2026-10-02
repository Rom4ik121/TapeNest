package arr

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Prowlarr client.
type Prowlarr struct{ b base }

// NewProwlarr builds a client (search can be slow: many indexers).
func NewProwlarr(rawURL, key string) (*Prowlarr, error) {
	b, err := newBase(rawURL, key, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return &Prowlarr{b: b}, nil
}

// Ping checks the API (and the key).
func (p *Prowlarr) Ping(ctx context.Context) error {
	return p.b.do(ctx, http.MethodGet, "/api/v1/system/status", nil, nil, nil)
}

// Indexer is a configured indexer.
type Indexer struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Enable   bool   `json:"enable"`
	Protocol string `json:"protocol"`
}

// Indexers lists configured indexers.
func (p *Prowlarr) Indexers(ctx context.Context) ([]Indexer, error) {
	var out []Indexer
	err := p.b.do(ctx, http.MethodGet, "/api/v1/indexer", nil, nil, &out)
	return out, err
}

// Release is one search result. DownloadURL is a Prowlarr proxy link that embeds
// the API key: treat it as a secret (never log it, never return it to clients).
type Release struct {
	GUID        string `json:"guid"`
	Title       string `json:"title"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	Protocol    string `json:"protocol"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
	Indexer     string `json:"indexer"`
	IndexerID   int    `json:"indexerId"`
	Categories  []struct {
		ID int `json:"id"`
	} `json:"categories"`
}

// Search queries all enabled indexers in the audio categories (Newznab 3000 = Audio).
func (p *Prowlarr) Search(ctx context.Context, query string, limit int) ([]Release, error) {
	q := url.Values{"query": {query}, "type": {"search"}, "categories": {"3000"}, "limit": {strconv.Itoa(limit)}}
	var out []Release
	if err := p.b.do(ctx, http.MethodGet, "/api/v1/search", q, nil, &out); err != nil {
		return nil, err
	}
	torrents := out[:0]
	for _, r := range out {
		if strings.EqualFold(r.Protocol, "torrent") {
			torrents = append(torrents, r)
		}
	}
	return torrents, nil
}

// Download fetches a release's .torrent through Prowlarr (or reports a magnet).
func (p *Prowlarr) Download(ctx context.Context, r Release) (torrentFile []byte, magnet string, err error) {
	if r.DownloadURL == "" {
		if strings.HasPrefix(r.MagnetURL, "magnet:") {
			return nil, r.MagnetURL, nil
		}
		return nil, "", ErrNotFound
	}
	return fetchTorrent(ctx, r.DownloadURL, p.b)
}

// EnsureLidarrApp registers Lidarr as a Prowlarr application (full indexer sync),
// so Lidarr's own monitoring can use the operator's indexers. Idempotent.
func (p *Prowlarr) EnsureLidarrApp(ctx context.Context, prowlarrURL, lidarrURL, lidarrKey string) (bool, error) {
	var apps []Provider
	if err := p.b.do(ctx, http.MethodGet, "/api/v1/applications", nil, nil, &apps); err != nil {
		return false, err
	}
	for _, a := range apps {
		if strings.EqualFold(a.Implementation, "Lidarr") {
			return false, nil
		}
	}
	app, err := schemaFor(ctx, p.b, "/api/v1/applications/schema", "Lidarr")
	if err != nil {
		return false, err
	}
	app.Name = "Lidarr"
	app.SetField("prowlarrUrl", prowlarrURL)
	app.SetField("baseUrl", lidarrURL)
	app.SetField("apiKey", lidarrKey)
	app.Extra = map[string]any{"syncLevel": "fullSync"}
	return true, p.b.do(ctx, http.MethodPost, "/api/v1/applications", nil, app.body(), nil)
}

// AddTorznab registers a Generic Torznab indexer (dev tooling for the legal test
// indexer only — TapeNest never configures indexers on its own).
func (p *Prowlarr) AddTorznab(ctx context.Context, name, baseURL string, categories []int) (bool, error) {
	var all []Provider
	if err := p.b.do(ctx, http.MethodGet, "/api/v1/indexer", nil, nil, &all); err != nil {
		return false, err
	}
	for _, a := range all {
		if a.Name == name {
			return false, nil
		}
	}
	var schemas []map[string]any
	if err := p.b.do(ctx, http.MethodGet, "/api/v1/indexer/schema", nil, nil, &schemas); err != nil {
		return false, err
	}
	for _, s := range schemas {
		if s["implementation"] != "Torznab" {
			continue
		}
		if dn, _ := s["definitionName"].(string); dn != "" && !strings.EqualFold(dn, "torznab") {
			continue
		}
		s["name"] = name
		s["enable"] = true
		s["appProfileId"] = 1
		s["priority"] = 25
		fields, _ := s["fields"].([]any)
		for _, f := range fields {
			fm, _ := f.(map[string]any)
			switch fm["name"] {
			case "baseUrl":
				fm["value"] = baseURL
			case "apiPath":
				fm["value"] = "/api"
			case "apiKey":
				fm["value"] = "legal-test"
			case "categories", "capabilities.categories":
				fm["value"] = categories
			}
		}
		delete(s, "id")
		return true, p.b.do(ctx, http.MethodPost, "/api/v1/indexer", url.Values{"forceSave": {"true"}}, s, nil)
	}
	return false, ErrNotFound
}
