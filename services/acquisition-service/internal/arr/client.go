// Package arr holds minimal Lidarr and Prowlarr v1 API clients. The API key goes
// in the X-Api-Key header (never in URLs); errors are scrubbed (package redact).
package arr

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

	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// Errors.
var (
	ErrUnavailable = errors.New("arr unavailable")
	ErrNotFound    = errors.New("arr: not found")
)

// StatusError is a non-2xx answer (body trimmed and scrubbed).
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("arr: status %d: %s", e.Status, e.Body) }

type base struct {
	url  string
	key  string
	http *http.Client
}

func newBase(rawURL, key string, timeout time.Duration) (base, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return base{}, errors.New("arr: bad url")
	}
	return base{url: u.String(), key: key, http: &http.Client{Timeout: timeout}}, nil
}

func (b base) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := b.url + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return redact.Error(err)
	}
	req.Header.Set("X-Api-Key", b.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, redact.Error(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%w: read body", ErrUnavailable)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	case resp.StatusCode >= 300:
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &StatusError{Status: resp.StatusCode, Body: redact.String(msg)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("arr: bad json: %w", err)
		}
	}
	return nil
}

// Field is a provider settings field ({name, value}).
type Field struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// Provider is a download client / application / indexer resource (schema-driven).
type Provider struct {
	ID             int            `json:"id,omitempty"`
	Name           string         `json:"name"`
	Implementation string         `json:"implementation"`
	ConfigContract string         `json:"configContract"`
	Fields         []Field        `json:"fields"`
	Tags           []int          `json:"tags"`
	Extra          map[string]any `json:"-"`
}

// SetField sets a field value (adds it when the schema lacks it).
func (p *Provider) SetField(name string, v any) {
	for i := range p.Fields {
		if strings.EqualFold(p.Fields[i].Name, name) {
			p.Fields[i].Value = v
			return
		}
	}
	p.Fields = append(p.Fields, Field{Name: name, Value: v})
}

// FieldValue returns a field value.
func (p *Provider) FieldValue(name string) any {
	for _, f := range p.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value
		}
	}
	return nil
}

// withExtra merges Extra (top-level knobs like enable/protocol/priority) into JSON.
func (p Provider) body() map[string]any {
	m := map[string]any{
		"name": p.Name, "implementation": p.Implementation, "configContract": p.ConfigContract,
		"fields": p.Fields, "tags": p.Tags,
	}
	if p.ID != 0 {
		m["id"] = p.ID
	}
	for k, v := range p.Extra {
		m[k] = v
	}
	if p.Tags == nil {
		m["tags"] = []int{}
	}
	return m
}

func schemaFor(ctx context.Context, b base, path, impl string) (Provider, error) {
	var all []Provider
	if err := b.do(ctx, http.MethodGet, path, nil, nil, &all); err != nil {
		return Provider{}, err
	}
	for _, p := range all {
		if strings.EqualFold(p.Implementation, impl) {
			return p, nil
		}
	}
	return Provider{}, ErrNotFound
}
