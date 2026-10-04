package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewAndPresign(t *testing.T) {
	s, err := New(Options{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", Region: "us-east-1", PublicURL: "https://files.example"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Presign(context.Background(), "video-edits", "a/b.mp4", "Клип.mp4", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "files.example") || !strings.Contains(u, "video-edits") {
		t.Fatalf("url %s", u)
	}
	noPub, err := New(Options{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if u, err := noPub.Presign(context.Background(), "b", "k", "", time.Minute); u != "" || err != nil {
		t.Fatalf("%q %v", u, err)
	}
	if _, err := New(Options{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", PublicURL: "://bad"}); err == nil {
		t.Fatal("bad public url")
	}
}

func TestPingDown(t *testing.T) {
	s, err := New(Options{Endpoint: "127.0.0.1:1", AccessKey: "a", SecretKey: "b", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := s.Ping(ctx, "media"); err == nil {
		t.Fatal("expected ping error")
	}
}
