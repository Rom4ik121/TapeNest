package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("i", 24))
	t.Setenv("S3_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("S3_ACCESS_KEY", "a")
	t.Setenv("S3_SECRET_KEY", "b")
	c, err := Load()
	if err != nil || c.Port != 8098 || c.Bucket != "photos" {
		t.Fatal(err, c)
	}
	t.Setenv("INTERNAL_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}
