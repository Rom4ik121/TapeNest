package config

import (
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("INTERNAL_API_TOKEN", "0123456789abcdef0123456789")
	t.Setenv("RECO_WEIGHTS", `{"cf":0.5,"epsilon":0.2}`)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8086 || c.Weights.CF != 0.5 || c.Weights.Epsilon != 0.2 || c.Weights.Profile != 1 {
		t.Fatalf("config %+v", c.Weights)
	}
	t.Setenv("RECO_WEIGHTS", "{bad")
	t.Setenv("MUSIC_SERVICE_URL", "ftp://x")
	t.Setenv("RECO_ANALYZE_WORKERS", "0")
	t.Setenv("INTERNAL_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("invalid config accepted")
	}
}
