package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tapenest/tapenest/services/music-service/internal/config"
)

func setValid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("NAVIDROME_PASSWORD", "pw")
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("t", 24))
	t.Setenv("STREAM_SIGNING_KEY", strings.Repeat("k", 32))
}

func TestLoad(t *testing.T) {
	setValid(t)
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8084 || c.WorkerPort != 8085 || c.StreamURLTTL != time.Hour || c.SyncInterval != 10*time.Minute || !c.MigrateOnStart {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("STREAM_PUBLIC_BASE", "https://m.example.com")
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
}

func TestValidation(t *testing.T) {
	for name, kv := range map[string][2]string{
		"db":       {"DATABASE_URL", " "},
		"token":    {"INTERNAL_API_TOKEN", "short"},
		"key":      {"STREAM_SIGNING_KEY", "short"},
		"ttl":      {"STREAM_URL_TTL", "2h"},
		"base":     {"STREAM_PUBLIC_BASE", "not a url"},
		"interval": {"CATALOG_SYNC_INTERVAL", "1s"},
		"parse":    {"MUSIC_SERVICE_PORT", "abc"},
	} {
		t.Run(name, func(t *testing.T) {
			setValid(t)
			t.Setenv(kv[0], kv[1])
			if _, err := config.Load(); err == nil {
				t.Fatalf("%s=%q accepted", kv[0], kv[1])
			}
		})
	}
}
