package reco_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/reco"
)

func TestClient(t *testing.T) {
	var mode atomic.Value
	mode.Store("ok")
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" || r.URL.Path != "/internal/v1/wave/next" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req reco.NextRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch mode.Load() {
		case "slow":
			time.Sleep(150 * time.Millisecond)
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "junk":
			_, _ = w.Write([]byte("{"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"modelVersion": 3, "tracks": []map[string]any{
			{"trackId": id, "score": 1.5, "source": "cf", "reason": map[string]any{"kind": "because_you_liked", "refTitle": "X"}},
		}})
	}))
	defer srv.Close()
	c, err := reco.New(reco.Options{BaseURL: srv.URL + "/", Token: "tok", Timeout: 60 * time.Millisecond, FailuresTrip: 2, OpenFor: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	picks, err := c.Next(ctx, reco.NextRequest{UserID: uuid.New(), Limit: 10})
	if err != nil || len(picks) != 1 || picks[0].TrackID != id || picks[0].Reason.RefTitle != "X" || c.State() != "closed" {
		t.Fatalf("ok: %+v %v", picks, err)
	}
	mode.Store("slow")
	if _, err := c.Next(ctx, reco.NextRequest{}); err == nil || err.Error() != "reco: timeout" {
		t.Fatalf("timeout: %v", err)
	}
	mode.Store("500")
	if _, err := c.Next(ctx, reco.NextRequest{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("500: %v", err)
	}
	if c.State() != "open" {
		t.Fatalf("breaker should be open: %s", c.State())
	}
	mode.Store("ok")
	if _, err := c.Next(ctx, reco.NextRequest{}); err == nil { // short-circuited
		t.Fatal("open breaker must fail fast")
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := c.Next(ctx, reco.NextRequest{}); err != nil || c.State() != "closed" {
		t.Fatalf("half-open probe should close: %v %s", err, c.State())
	}
	mode.Store("junk")
	if _, err := c.Next(ctx, reco.NextRequest{}); err == nil || err.Error() != "reco: invalid response" {
		t.Fatalf("junk: %v", err)
	}
	srv.Close()
	_, err = c.Next(ctx, reco.NextRequest{})
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "tok") {
		t.Fatalf("transport error must be scrubbed: %v", err)
	}
}

func TestDisabledAndInvalid(t *testing.T) {
	c, err := reco.New(reco.Options{})
	if c != nil || err != nil {
		t.Fatal("empty URL → nil client")
	}
	if _, err := c.Next(context.Background(), reco.NextRequest{}); !errors.Is(err, reco.ErrDisabled) || c.State() != "disabled" {
		t.Fatal("nil client is disabled")
	}
	for _, u := range []string{"ftp://x", "not a url", "http://"} {
		if _, err := reco.New(reco.Options{BaseURL: u}); err == nil {
			t.Fatalf("%q accepted", u)
		}
	}
	if c, _ := reco.New(reco.Options{BaseURL: "http://127.0.0.1:1"}); c.State() != "closed" {
		t.Fatal("defaults")
	}
}
