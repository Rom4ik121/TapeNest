package arr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// maxTorrentFile bounds a downloaded .torrent (album torrents are a few hundred KB).
const maxTorrentFile = 8 << 20

var fetchClient = &http.Client{
	Timeout: 45 * time.Second,
	// a redirect to magnet: is how some indexers answer; stop and report it
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme == "magnet" {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

func fetchTorrent(ctx context.Context, link string, b base) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", redact.Error(err)
	}
	if strings.HasPrefix(link, b.url+"/") { // the key only ever goes to Prowlarr itself
		req.Header.Set("X-Api-Key", b.key)
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: torrent fetch: %w", ErrUnavailable, redact.Error(err))
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "magnet:") {
		return nil, loc, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: torrent fetch status %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentFile+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: torrent read", ErrUnavailable)
	}
	if len(body) > maxTorrentFile {
		return nil, "", errors.New("arr: torrent file too large")
	}
	if s := strings.TrimSpace(string(body[:min(len(body), 2048)])); strings.HasPrefix(s, "magnet:") {
		return nil, s, nil
	}
	return body, "", nil
}
