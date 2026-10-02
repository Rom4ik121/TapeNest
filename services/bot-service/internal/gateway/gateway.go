// Package gateway is bot-service's REST client for api-gateway internal endpoints.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sony/gobreaker"
)

// Outcome of a download request, mapped from the gateway response.
type Outcome int

// Outcomes.
const (
	Accepted       Outcome = iota // 2xx: job queued
	NotImplemented                // 501: download-service not deployed in this environment
	Invalid                       // 400: gateway rejected the link
	RateLimited                   // 429
	Unavailable                   // 503, network error or open breaker (spec §8)
	Failed                        // anything else
	Unsupported                   // 400 UNSUPPORTED_SOURCE / FORBIDDEN_HOST
	Playlist                      // 400 PLAYLIST_NOT_SUPPORTED
	QuotaActive                   // 429 QUOTA_ACTIVE: too many downloads in progress
	QuotaDaily                    // 429 QUOTA_DAILY: daily limit reached
)

// DownloadRequest is sent to POST /internal/v1/bot/downloads.
type DownloadRequest struct {
	TelegramID   int64  `json:"telegramId"`
	ChatID       int64  `json:"chatId"`
	URL          string `json:"url"`
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"languageCode,omitempty"`
	// bot's status message (edited with progress) and the user's message with the link
	StatusMessageID  int64 `json:"statusMessageId,omitempty"`
	ReplyToMessageID int64 `json:"replyToMessageId,omitempty"`
}

// Client calls the gateway with a timeout and a circuit breaker.
type Client struct {
	base    string
	token   string
	http    *http.Client
	breaker *gobreaker.CircuitBreaker
}

// New creates a client.
func New(base, internalToken string, timeout time.Duration) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: internalToken,
		http:  &http.Client{Timeout: timeout},
		breaker: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name:        "api-gateway",
			Timeout:     15 * time.Second,
			ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 5 },
		}),
	}
}

var errServer = errors.New("gateway 5xx")

// RequestDownload forwards a link; the error is non-nil only for Unavailable/Failed.
func (c *Client) RequestDownload(ctx context.Context, req DownloadRequest, requestID string) (Outcome, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Failed, fmt.Errorf("marshal: %w", err)
	}
	var status int
	var code string
	_, err = c.breaker.Execute(func() (any, error) {
		hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/v1/bot/downloads", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Authorization", "Bearer "+c.token)
		if requestID != "" {
			hr.Header.Set("X-Request-Id", requestID)
		}
		resp, err := c.http.Do(hr)
		if err != nil {
			return nil, err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		status = resp.StatusCode
		var eb struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &eb) == nil {
			code = eb.Code
		}
		if status >= 500 && status != http.StatusNotImplemented {
			return nil, errServer
		}
		return nil, nil
	})
	switch {
	case err != nil && status == 0:
		return Unavailable, fmt.Errorf("gateway: %w", err)
	case status >= 200 && status < 300:
		return Accepted, nil
	case status == http.StatusNotImplemented:
		return NotImplemented, nil
	case status == http.StatusBadRequest:
		switch code {
		case "UNSUPPORTED_SOURCE", "FORBIDDEN_HOST":
			return Unsupported, nil
		case "PLAYLIST_NOT_SUPPORTED":
			return Playlist, nil
		}
		return Invalid, nil
	case status == http.StatusTooManyRequests:
		switch code {
		case "QUOTA_ACTIVE":
			return QuotaActive, nil
		case "QUOTA_DAILY":
			return QuotaDaily, nil
		}
		return RateLimited, nil
	case status == http.StatusServiceUnavailable || status == http.StatusBadGateway || status == http.StatusGatewayTimeout:
		return Unavailable, fmt.Errorf("gateway: status %d", status)
	default:
		return Failed, fmt.Errorf("gateway: status %d", status)
	}
}
