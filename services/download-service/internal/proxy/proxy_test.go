package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

func TestPickRotationFallbackAvoid(t *testing.T) {
	empty := New(nil, nil, nil)
	if u, tier := empty.Pick(domain.TierDatacenter, ""); u != "" || tier != domain.TierDirect {
		t.Fatalf("empty = %q %s", u, tier)
	}
	p := New([]string{"http://dc1", "http://dc2"}, []string{"http://res1"}, nil)
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		u, tier := p.Pick(domain.TierDatacenter, "")
		if tier != domain.TierDatacenter {
			t.Fatal(tier)
		}
		seen[u] = true
	}
	if len(seen) != 2 {
		t.Fatalf("round robin: %v", seen)
	}
	for i := 0; i < 4; i++ {
		if u, _ := p.Pick(domain.TierDatacenter, "http://dc1"); u != "http://dc2" {
			t.Fatalf("avoid ignored: %s", u)
		}
	}
	if u, tier := p.Pick(domain.TierMobile, ""); u != "http://res1" || tier != domain.TierResidential {
		t.Fatalf("mobile fallback = %s %s", u, tier)
	}
	if u, _ := p.Pick(domain.TierResidential, "http://res1"); u != "http://res1" {
		t.Fatal("single proxy must be reused when it is the only one")
	}
	if u, tier := p.Pick(domain.TierDirect, ""); u != "" || tier != domain.TierDirect {
		t.Fatal("direct")
	}
	for _, e := range p.tiers[domain.TierDatacenter] {
		e.healthy.Store(false)
	}
	if u, tier := p.Pick(domain.TierDatacenter, ""); u != "http://res1" || tier != domain.TierResidential {
		t.Fatalf("unhealthy fallback = %s %s", u, tier)
	}
	h := p.Healthy()
	if h[domain.TierDatacenter] != [2]int{0, 2} || h[domain.TierResidential] != [2]int{1, 1} {
		t.Fatalf("healthy = %v", h)
	}
}

func TestCheckAndRun(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer bad.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	p := New([]string{good.URL, bad.URL}, []string{deadURL, "::bad url"}, []string{"http://user:secret@127.0.0.1:1"})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p.Check(context.Background(), "http://health.example/generate_204", 2*time.Second, log)
	h := p.Healthy()
	if h[domain.TierDatacenter] != [2]int{1, 2} || h[domain.TierResidential] != [2]int{0, 2} || h[domain.TierMobile] != [2]int{0, 1} {
		t.Fatalf("healthy = %v", h)
	}
	p.Check(context.Background(), "::bad target", time.Second, log)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx, "http://health.example/", time.Hour, log); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	New(nil, nil, nil).Run(context.Background(), "x", time.Second, log) // returns immediately
}

func TestRedact(t *testing.T) {
	if got := redact("http://user:pass@host:8080"); got != "http://host:8080" {
		t.Fatal(got)
	}
	if got := redact("::"); got != "invalid" {
		t.Fatal(got)
	}
}
