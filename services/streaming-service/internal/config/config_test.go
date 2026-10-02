package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("INTERNAL_API_TOKEN", "0123456789abcdef01234567")
	t.Setenv("STREAM_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("CONTENT_SOURCES", "licensed")
	c, err := Load()
	if err != nil || c.Port != 8094 || c.P2P() {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("CONTENT_SOURCES", "nope")
	if _, err := Load(); err == nil {
		t.Fatal("expected reject")
	}
}
