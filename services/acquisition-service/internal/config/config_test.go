package config

import (
	"strings"
	"testing"
	"time"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REDIS_URL", "redis://localhost/0")
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("a", 24))
	t.Setenv("CONTENT_SOURCES", "p2p")
	t.Setenv("LIDARR_API_KEY", "k")
	t.Setenv("PROWLARR_API_KEY", "k")
	t.Setenv("QBITTORRENT_PASSWORD", "k")
	t.Setenv("MUSIC_DIR", "/music")
	t.Setenv("TORRENT_DIR", "/downloads")
}

func TestLoadOK(t *testing.T) {
	setBase(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled() || c.LibraryRoot() != "/music/library" || c.Port != 8088 || c.Tick < 200*time.Millisecond {
		t.Fatalf("%+v", c)
	}
	if c.ProwlarrSelfURL != c.ProwlarrURL || c.LidarrSelfURL != c.LidarrURL {
		t.Fatal("self urls")
	}
}

func TestValidate(t *testing.T) {
	setBase(t)
	t.Setenv("CONTENT_SOURCES", "nope")
	if _, err := Load(); err == nil {
		t.Fatal("sources")
	}
	t.Setenv("CONTENT_SOURCES", "licensed")
	t.Setenv("LIDARR_API_KEY", "")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTENT_SOURCES", "p2p")
	t.Setenv("LIDARR_API_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("key")
	}
	setBase(t)
	t.Setenv("INTERNAL_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("token")
	}
	setBase(t)
	t.Setenv("MUSIC_DIR", "rel")
	if _, err := Load(); err == nil {
		t.Fatal("dir")
	}
	setBase(t)
	t.Setenv("ACQ_LIBRARY_SUBDIR", "../x")
	if _, err := Load(); err == nil {
		t.Fatal("subdir")
	}
	setBase(t)
	t.Setenv("ACQ_MAX_ACTIVE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("active")
	}
	setBase(t)
	t.Setenv("LIDARR_URL", "ftp://x")
	if _, err := Load(); err == nil {
		t.Fatal("url")
	}
	setBase(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("db")
	}
}
