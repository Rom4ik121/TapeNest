package navidrome_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/music-service/internal/navidrome"
	"github.com/tapenest/tapenest/services/music-service/internal/testutil"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestClient(t *testing.T) {
	if _, err := navidrome.New("ftp://x", "u", "p", quiet); err == nil {
		t.Fatal("bad url accepted")
	}
	songs := []navidrome.Song{{ID: "s1", Title: "One"}, {ID: "s2", Title: "Two"}, {ID: "s3", Title: "Three"}}
	nd := testutil.NewNavidrome(songs)
	defer nd.Close()
	c, err := navidrome.New(nd.URL+"/", "admin", "pw", quiet)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if created, err := c.EnsureAdmin(ctx); !created || err != nil {
		t.Fatalf("first admin: %v %v", created, err)
	}
	if created, err := c.EnsureAdmin(ctx); created || err != nil {
		t.Fatalf("admin exists → no-op: %v %v", created, err)
	}
	if err := c.Ping(ctx); err != nil || !c.Healthy() {
		t.Fatalf("ping: %v", err)
	}
	if err := c.StartScan(ctx); err != nil || nd.Scans.Load() != 1 {
		t.Fatalf("scan: %v", err)
	}
	page, err := c.Songs(ctx, 1, 5)
	if err != nil || len(page) != 2 || page[0].ID != "s2" {
		t.Fatalf("songs: %v %+v", err, page)
	}
	resp, err := c.Stream(ctx, "s1", http.Header{"Range": {"bytes=0-3"}, "X-Other": {"dropped"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 206 || string(b) != "0123" {
		t.Fatalf("range: %d %q", resp.StatusCode, b)
	}
	if _, err := c.Stream(ctx, "missing", nil); !errors.Is(err, navidrome.ErrNotFound) {
		t.Fatalf("missing stream: %v", err)
	}
	resp, err = c.CoverArt(ctx, "al-1", 300)
	if err != nil || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover: %v", err)
	}
	_ = resp.Body.Close()
	if _, err := c.CoverArt(ctx, "missing", 300); !errors.Is(err, navidrome.ErrNotFound) {
		t.Fatalf("missing cover: %v", err)
	}

	// wrong credentials → failed status (not "unavailable")
	bad, _ := navidrome.New(nd.URL, "", "", quiet)
	if err := bad.Ping(ctx); err == nil || errors.Is(err, navidrome.ErrUnavailable) {
		t.Fatalf("auth error: %v", err)
	}

	// down: 5xx → ErrUnavailable, breaker opens after 3, health loop flips
	nd.Down.Store(true)
	if _, err := c.EnsureAdmin(ctx); !errors.Is(err, navidrome.ErrUnavailable) {
		t.Fatalf("admin on 5xx: %v", err)
	}
	hctx, cancel := context.WithCancel(ctx)
	go c.RunHealth(hctx, 10*time.Millisecond)
	for i := 0; i < 3; i++ {
		if err := c.Ping(ctx); !errors.Is(err, navidrome.ErrUnavailable) {
			t.Fatalf("down #%d: %v", i, err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.Healthy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if c.Healthy() {
		t.Fatal("must be unhealthy")
	}
	if _, err := c.Songs(ctx, 0, 1); !errors.Is(err, navidrome.ErrUnavailable) {
		t.Fatalf("breaker open: %v", err)
	}
	// canceled requests are not failures and surface as context.Canceled
	cctx, ccancel := context.WithCancel(ctx)
	ccancel()
	if _, err := c.Stream(cctx, "s1", nil); err == nil {
		t.Fatal("canceled")
	}
	nd.Close()
	c2, _ := navidrome.New(nd.URL, "admin", "pw", quiet)
	if _, err := c2.EnsureAdmin(ctx); !errors.Is(err, navidrome.ErrUnavailable) {
		t.Fatalf("admin on closed server: %v", err)
	}
	err = c2.Ping(ctx)
	if !errors.Is(err, navidrome.ErrUnavailable) {
		t.Fatalf("closed server: %v", err)
	}
	// transport errors must not leak the credential-bearing query string
	if msg := err.Error(); strings.Contains(msg, "t=") || strings.Contains(msg, "u=admin") || !strings.Contains(msg, "/rest/ping") {
		t.Fatalf("unredacted error: %s", msg)
	}
}
