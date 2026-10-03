// Package download asks download-service where a user's finished file lives.
// The editor never downloads from the internet itself.
package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

// Client calls download-service's internal source route.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New builds a client. base is DOWNLOAD_SERVICE_URL.
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Resolve returns the stored object for a job the user owns.
func (c *Client) Resolve(ctx context.Context, user, job uuid.UUID) (domain.SourceFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/internal/v1/downloads/"+job.String()+"/source", nil)
	if err != nil {
		return domain.SourceFile{}, err
	}
	req.Header.Set("X-Internal-Token", c.Token)
	req.Header.Set("X-User-Id", user.String())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return domain.SourceFile{}, fmt.Errorf("download-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return domain.SourceFile{}, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return domain.SourceFile{}, domain.ErrNotFound
	case http.StatusConflict:
		return domain.SourceFile{}, domain.ErrNotReady
	default:
		return domain.SourceFile{}, fmt.Errorf("download-service status %d", resp.StatusCode)
	}
	var dto struct {
		ObjectKey   string `json:"objectKey"`
		Title       string `json:"title"`
		DurationSec int    `json:"durationSec"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		MimeType    string `json:"mimeType"`
		SizeBytes   int64  `json:"sizeBytes"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		return domain.SourceFile{}, fmt.Errorf("download-service json: %w", err)
	}
	if dto.ObjectKey == "" {
		return domain.SourceFile{}, errors.New("download-service: empty object key")
	}
	return domain.SourceFile{
		ObjectKey: dto.ObjectKey, Title: dto.Title, DurationSec: float64(dto.DurationSec),
		Width: dto.Width, Height: dto.Height, MimeType: dto.MimeType, SizeBytes: dto.SizeBytes,
	}, nil
}
