// Package musicclient notifies music-service about newly imported files.
package musicclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/core"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// Client calls music-service's internal API.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New builds a client.
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Refresh posts imported files so music-service can map them to placeholders
// and trigger a Navidrome scan.
func (c *Client) Refresh(ctx context.Context, files []core.CatalogFile) error {
	body, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/internal/v1/catalog/refresh", bytes.NewReader(body))
	if err != nil {
		return redact.Error(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("music refresh: %w", redact.Error(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("music refresh: status %d", resp.StatusCode)
	}
	return nil
}
