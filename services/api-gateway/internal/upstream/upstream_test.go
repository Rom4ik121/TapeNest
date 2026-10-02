package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func noopErr(http.ResponseWriter, *http.Request, string, error) {}

func TestNewValidation(t *testing.T) {
	for _, u := range []string{"ftp://x", "://bad", "http://"} {
		if _, err := New("x", u, Options{}, noopErr); err == nil {
			t.Errorf("%q must be rejected", u)
		}
	}
	s, err := New("x", "", Options{}, noopErr)
	if err != nil || s.Configured() {
		t.Fatal("empty url = not configured")
	}
	if _, err := s.Do(context.Background(), "GET", "/", nil, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestDoAndBreaker(t *testing.T) {
	var fail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Header.Get("X-Test")+"|"+r.URL.Path+"|"+string(b)+"|"+r.Header.Get("X-Internal-Token"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	s, err := New("svc", srv.URL, Options{Timeout: time.Second, FailureThreshold: 2, OpenFor: time.Minute, InternalToken: "tok"}, noopErr)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Do(context.Background(), "POST", "/a/b", http.Header{"X-Test": {"1"}}, []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || resp.Header.Get("X-Seen") != "1|/a/b|hi|tok" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("X-Seen"))
	}
	fail = true
	for i := 0; i < 2; i++ {
		resp, err := s.Do(context.Background(), "GET", "/", nil, nil)
		if err != nil || resp.StatusCode != 502 {
			t.Fatalf("5xx must pass through: %v", err)
		}
		_ = resp.Body.Close()
	}
	if _, err := s.Do(context.Background(), "GET", "/", nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("breaker open: %v", err)
	}
}

func TestProxyInjectsInternalToken(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Internal-Token")+"|"+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, err := New("svc", srv.URL, Options{Timeout: time.Second, InternalToken: "secret"}, noopErr)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/downloads", nil)
	req.Header.Set("X-Internal-Token", "forged")
	req.Header.Set("Authorization", "Bearer user-jwt")
	rec := httptest.NewRecorder()
	s.Handler(noopErr).ServeHTTP(rec, req)
	if rec.Code != 200 || len(seen) != 1 || seen[0] != "secret|" {
		t.Fatalf("got %d %v", rec.Code, seen)
	}
}

func TestDegradedDependencyIsNotAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(DegradedHeader, "navidrome")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	s, err := New("svc", srv.URL, Options{Timeout: time.Second, FailureThreshold: 1, OpenFor: time.Minute}, noopErr)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		resp, err := s.Do(context.Background(), "GET", "/", nil, nil)
		if err != nil || resp.StatusCode != 503 {
			t.Fatalf("#%d: %v", i, err)
		}
		_ = resp.Body.Close()
	}
}
