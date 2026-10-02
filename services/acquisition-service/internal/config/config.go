// Package config loads acquisition-service settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is shared by cmd/server (API) and cmd/worker. Secrets only from env,
// never logged (see LogValue-free usage: callers log Summary()).
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"dev"`
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"ACQUISITION_SERVICE_PORT" env-default:"8088"`
	WorkerPort int    `env:"ACQUISITION_WORKER_PORT" env-default:"8089"`

	DatabaseURL    string `env:"DATABASE_URL"`
	RedisURL       string `env:"REDIS_URL"`
	InternalToken  string `env:"INTERNAL_API_TOKEN"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	// p2p = acquisition on; licensed = off (spec §14: switch without rewriting)
	ContentSources string `env:"CONTENT_SOURCES" env-default:"p2p"`

	MusicURL       string `env:"MUSIC_SERVICE_URL" env-default:"http://127.0.0.1:8084"`
	LidarrURL      string `env:"LIDARR_URL" env-default:"http://127.0.0.1:8686"`
	LidarrAPIKey   string `env:"LIDARR_API_KEY"`
	ProwlarrURL    string `env:"PROWLARR_URL" env-default:"http://127.0.0.1:9696"`
	ProwlarrAPIKey string `env:"PROWLARR_API_KEY"`
	QbtURL         string `env:"QBITTORRENT_URL" env-default:"http://127.0.0.1:8092"`
	QbtUser        string `env:"QBITTORRENT_USER" env-default:"tapenest"`
	QbtPassword    string `env:"QBITTORRENT_PASSWORD"`
	QbtCategory    string `env:"ACQ_QBT_CATEGORY" env-default:"tapenest"`
	// how Lidarr (and Prowlarr) reach the others; differs from ours inside Docker
	LidarrQbtHost   string `env:"ACQ_LIDARR_QBT_HOST" env-default:"127.0.0.1"`
	ProwlarrSelfURL string `env:"ACQ_PROWLARR_SELF_URL"`
	LidarrSelfURL   string `env:"ACQ_LIDARR_SELF_URL"`

	MusicDir      string `env:"MUSIC_DIR" env-default:"/music"`
	LibrarySubdir string `env:"ACQ_LIBRARY_SUBDIR" env-default:"library"`
	TorrentDir    string `env:"TORRENT_DIR" env-default:"/downloads"`

	MBURL       string `env:"ACQ_MUSICBRAINZ_URL" env-default:"https://musicbrainz.org"`
	MBUserAgent string `env:"ACQ_MUSICBRAINZ_UA" env-default:"TapeNest/1.0 ( https://t.me/tapenest_bot )"`
	CoverURL    string `env:"ACQ_COVERART_URL" env-default:"https://coverartarchive.org"`

	UserDaily        int           `env:"ACQ_USER_DAILY" env-default:"30"`
	UserActive       int           `env:"ACQ_USER_ACTIVE" env-default:"5"`
	MaxActive        int           `env:"ACQ_MAX_ACTIVE" env-default:"3"`
	MaxReleaseGB     float64       `env:"ACQ_MAX_RELEASE_GB" env-default:"2"`
	RetryAfter       time.Duration `env:"ACQ_RETRY_AFTER" env-default:"6h"`
	StallTimeout     time.Duration `env:"ACQ_STALL_TIMEOUT" env-default:"15m"`
	ImportTimeout    time.Duration `env:"ACQ_IMPORT_TIMEOUT" env-default:"3m"`
	SeedRatio        float64       `env:"ACQ_SEED_RATIO" env-default:"1.0"`
	SeedMinutes      int           `env:"ACQ_SEED_MINUTES" env-default:"1440"`
	TorrentMaxGB     float64       `env:"ACQ_TORRENT_MAX_GB" env-default:"50"`
	LibraryMaxGB     float64       `env:"ACQ_LIBRARY_MAX_GB" env-default:"500"`
	StreamStartBytes int64         `env:"ACQ_STREAM_START_BYTES" env-default:"393216"`
	Tick             time.Duration `env:"ACQ_TICK" env-default:"2s"`
	Bootstrap        bool          `env:"ACQ_BOOTSTRAP" env-default:"true"`
}

// Load reads env and validates.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Enabled reports whether p2p acquisition is allowed (CONTENT_SOURCES=p2p).
func (c *Config) Enabled() bool { return c.ContentSources == "p2p" }

// LibraryRoot is the Lidarr root folder inside Navidrome's music folder.
func (c *Config) LibraryRoot() string { return filepath.Join(c.MusicDir, c.LibrarySubdir) }

// Validate checks required settings (names only in messages, never values).
func (c *Config) Validate() error {
	var errs []error
	for name, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if c.ContentSources != "p2p" && c.ContentSources != "licensed" {
		errs = append(errs, errors.New("CONTENT_SOURCES must be p2p or licensed"))
	}
	for name, v := range map[string]string{"MUSIC_SERVICE_URL": c.MusicURL, "LIDARR_URL": c.LidarrURL, "PROWLARR_URL": c.ProwlarrURL, "QBITTORRENT_URL": c.QbtURL, "ACQ_MUSICBRAINZ_URL": c.MBURL, "ACQ_COVERART_URL": c.CoverURL} {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an absolute http(s) URL", name))
		}
	}
	if c.Enabled() {
		for name, v := range map[string]string{"LIDARR_API_KEY": c.LidarrAPIKey, "PROWLARR_API_KEY": c.ProwlarrAPIKey, "QBITTORRENT_PASSWORD": c.QbtPassword} {
			if strings.TrimSpace(v) == "" {
				errs = append(errs, fmt.Errorf("%s is required when CONTENT_SOURCES=p2p", name))
			}
		}
	}
	if !filepath.IsAbs(c.MusicDir) || !filepath.IsAbs(c.TorrentDir) {
		errs = append(errs, errors.New("MUSIC_DIR and TORRENT_DIR must be absolute paths"))
	}
	if strings.Contains(c.LibrarySubdir, "..") || filepath.IsAbs(c.LibrarySubdir) {
		errs = append(errs, errors.New("ACQ_LIBRARY_SUBDIR must be a relative directory"))
	}
	if c.MaxActive < 1 || c.MaxActive > 20 || c.UserDaily < 1 || c.UserActive < 1 {
		errs = append(errs, errors.New("ACQ_MAX_ACTIVE must be 1..20, ACQ_USER_DAILY/ACQ_USER_ACTIVE ≥ 1"))
	}
	if c.SeedRatio < 0 || c.SeedMinutes < 0 || c.TorrentMaxGB <= 0 || c.LibraryMaxGB <= 0 || c.MaxReleaseGB <= 0 {
		errs = append(errs, errors.New("seeding and disk limits must be positive"))
	}
	if c.Tick < 200*time.Millisecond || c.StreamStartBytes < 16<<10 {
		errs = append(errs, errors.New("ACQ_TICK ≥ 200ms, ACQ_STREAM_START_BYTES ≥ 16384"))
	}
	if c.ProwlarrSelfURL == "" {
		c.ProwlarrSelfURL = c.ProwlarrURL
	}
	if c.LidarrSelfURL == "" {
		c.LidarrSelfURL = c.LidarrURL
	}
	return errors.Join(errs...)
}
