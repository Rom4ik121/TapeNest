package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOutcomes(t *testing.T) {
	status := 0
	var got DownloadRequest
	var auth, reqID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/bot/downloads" {
			w.WriteHeader(404)
			return
		}
		auth, reqID = r.Header.Get("Authorization"), r.Header.Get("X-Request-Id")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "internal-token", time.Second)
	req := DownloadRequest{TelegramID: 1, ChatID: 1, URL: "https://youtu.be/x", LanguageCode: "ru"}
	for st, want := range map[int]Outcome{
		202: Accepted, 200: Accepted, 501: NotImplemented, 400: Invalid, 429: RateLimited,
		503: Unavailable, 502: Unavailable, 401: Failed, 500: Failed,
	} {
		status = st
		out, err := c.RequestDownload(context.Background(), req, "rid-1")
		if out != want {
			t.Errorf("status %d: got %v want %v (err %v)", st, out, want, err)
		}
		if (want == Unavailable || want == Failed) != (err != nil) {
			t.Errorf("status %d: unexpected err %v", st, err)
		}
	}
	if auth != "Bearer internal-token" || reqID != "rid-1" || got.URL != req.URL || got.LanguageCode != "ru" {
		t.Fatalf("request: %q %q %+v", auth, reqID, got)
	}
}

func TestBreakerAndNetworkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	c := New(srv.URL, "t", 200*time.Millisecond)
	for i := 0; i < 7; i++ {
		out, err := c.RequestDownload(context.Background(), DownloadRequest{}, "")
		if out != Unavailable || err == nil {
			t.Fatalf("attempt %d: %v %v", i, out, err)
		}
	}
	if c.breaker.State().String() != "open" {
		t.Fatalf("breaker must open, state=%s", c.breaker.State())
	}
}

func TestErrorCodes(t *testing.T) {
	var status int
	var body string
	var got DownloadRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New(srv.URL, "t", time.Second)
	for _, tc := range []struct {
		status int
		code   string
		want   Outcome
	}{
		{400, "UNSUPPORTED_SOURCE", Unsupported},
		{400, "FORBIDDEN_HOST", Unsupported},
		{400, "PLAYLIST_NOT_SUPPORTED", Playlist},
		{400, "INVALID_URL", Invalid},
		{429, "QUOTA_ACTIVE", QuotaActive},
		{429, "QUOTA_DAILY", QuotaDaily},
		{429, "RATE_LIMITED", RateLimited},
	} {
		status, body = tc.status, `{"message":"x","code":"`+tc.code+`"}`
		if out, _ := c.RequestDownload(context.Background(), DownloadRequest{StatusMessageID: 5, ReplyToMessageID: 4}, ""); out != tc.want {
			t.Errorf("%d %s: got %v", tc.status, tc.code, out)
		}
	}
	if got.StatusMessageID != 5 || got.ReplyToMessageID != 4 {
		t.Fatalf("message ids not forwarded: %+v", got)
	}
}
