// Package config loads music-service settings from the environment (cleanenv).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is shared by cmd/server (API) and cmd/worker. Secrets only from env (spec §9).
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"dev"`
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"MUSIC_SERVICE_PORT" env-default:"8084"`
	WorkerPort int    `env:"MUSIC_WORKER_PORT" env-default:"8085"`

	DatabaseURL    string `env:"DATABASE_URL"`
	ReplicaURL     string `env:"DB_REPLICA_URL"` // optional read replica (spec §5.4)
	RedisURL       string `env:"REDIS_URL"`
	InternalToken  string `env:"INTERNAL_API_TOKEN"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	NavidromeURL      string        `env:"NAVIDROME_URL" env-default:"http://127.0.0.1:4533"`
	NavidromeUser     string        `env:"NAVIDROME_USER" env-default:"admin"`
	NavidromePassword string        `env:"NAVIDROME_PASSWORD"`
	NavidromePing     time.Duration `env:"NAVIDROME_HEALTH_INTERVAL" env-default:"15s"`

	StreamSigningKey string        `env:"STREAM_SIGNING_KEY"`
	StreamURLTTL     time.Duration `env:"STREAM_URL_TTL" env-default:"1h"`
	StreamPublicBase string        `env:"STREAM_PUBLIC_BASE"`

	// reco-service (ADR 0010); empty URL → My Wave uses the local heuristic only
	RecoURL             string        `env:"RECO_SERVICE_URL"`
	RecoTimeout         time.Duration `env:"RECO_TIMEOUT" env-default:"400ms"`
	RecoBreakerFailures uint32        `env:"RECO_BREAKER_FAILURES" env-default:"3"`
	RecoBreakerOpen     time.Duration `env:"RECO_BREAKER_OPEN" env-default:"30s"`

	CoverArtURL string `env:"COVERART_URL" env-default:"https://coverartarchive.org"`

	// MUSIC_SOURCES: comma list. youtube is the external catalog (ADR 0012).
	// library-only: set MUSIC_SOURCES=library.
	MusicSources string `env:"MUSIC_SOURCES" env-default:"youtube"`
	YTDLPBin     string `env:"YTDLP_BIN" env-default:"yt-dlp"`

	SyncInterval       time.Duration `env:"CATALOG_SYNC_INTERVAL" env-default:"10m"`
	PopularityInterval time.Duration `env:"POPULARITY_REFRESH_INTERVAL" env-default:"5m"`
}

// Load reads env and validates the result.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.finish(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) finish() error {
	var errs []error
	for name, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "NAVIDROME_PASSWORD": c.NavidromePassword} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if len(c.StreamSigningKey) < 32 {
		errs = append(errs, errors.New("STREAM_SIGNING_KEY must be at least 32 bytes"))
	}
	if c.StreamURLTTL <= 0 || c.StreamURLTTL > time.Hour {
		errs = append(errs, errors.New("STREAM_URL_TTL must be in (0, 1h] (spec §9)"))
	}
	if c.StreamPublicBase != "" {
		u, err := url.Parse(c.StreamPublicBase)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("STREAM_PUBLIC_BASE must be an absolute http(s) URL"))
		}
	}
	if c.SyncInterval < time.Minute || c.PopularityInterval < 10*time.Second || c.NavidromePing < time.Second {
		errs = append(errs, errors.New("CATALOG_SYNC_INTERVAL ≥ 1m, POPULARITY_REFRESH_INTERVAL ≥ 10s, NAVIDROME_HEALTH_INTERVAL ≥ 1s"))
	}
	if c.RecoTimeout < 50*time.Millisecond || c.RecoTimeout > 5*time.Second {
		errs = append(errs, errors.New("RECO_TIMEOUT must be in [50ms, 5s]"))
	}
	return errors.Join(errs...)
}

// SourceEnabled reports whether a catalog source (youtube) is on.
func (c *Config) SourceEnabled(name string) bool {
	for _, p := range strings.Split(strings.ToLower(c.MusicSources), ",") {
		if strings.TrimSpace(p) == name {
			return true
		}
	}
	return false
}
