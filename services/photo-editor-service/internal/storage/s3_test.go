package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPresign(t *testing.T) {
	s, err := New(Options{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", Region: "us-east-1", PublicURL: "https://files.example"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Presign(context.Background(), "photos", "a.jpg", time.Minute)
	if err != nil || !strings.Contains(u, "files.example") {
		t.Fatal(err, u)
	}
	plain, err := New(Options{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if u, err := plain.Presign(context.Background(), "photos", "a.jpg", time.Minute); u != "" || err != nil {
		t.Fatal(u, err)
	}
}
