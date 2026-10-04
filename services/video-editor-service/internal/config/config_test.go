package config

import (
	"strings"
	"testing"
	"time"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("i", 24))
	t.Setenv("DOWNLOAD_SERVICE_URL", "http://127.0.0.1:8082")
	t.Setenv("S3_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("S3_ACCESS_KEY", "a")
	t.Setenv("S3_SECRET_KEY", "b")
}

func TestLoadDefaults(t *testing.T) {
	setBase(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8096 || c.WorkerPort != 8097 || c.EditBucket != "video-edits" || c.PresignTTL != time.Hour {
		t.Fatalf("%+v", c)
	}
}

func TestLoadRejectsShortToken(t *testing.T) {
	setBase(t)
	t.Setenv("INTERNAL_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}
