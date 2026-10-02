// Package config loads streaming-service settings from the environment.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"

	"github.com/tapenest/tapenest/services/streaming-service/internal/source"
)

// Config is shared by the API and the worker.
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"dev"`
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"STREAMING_SERVICE_PORT" env-default:"8094"`
	WorkerPort int    `env:"STREAMING_WORKER_PORT" env-default:"8095"`

	DatabaseURL    string `env:"DATABASE_URL"`
	InternalToken  string `env:"INTERNAL_API_TOKEN"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	SigningKey string        `env:"STREAM_SIGNING_KEY"`
	StreamTTL  time.Duration `env:"STREAM_URL_TTL" env-default:"1h"`

	// CONTENT_SOURCES is shared with acquisition-service: p2p and/or licensed.
	// p2p only reaches TorrServer when a catalog row already has a magnet.
	ContentSources string `env:"CONTENT_SOURCES" env-default:"licensed"`
	TorrServerURL  string `env:"TORRSERVER_URL"`

	PreviewDir    string        `env:"CINEMA_PREVIEW_DIR" env-default:"/workspace/data/cinema-preview"`
	CacheDir      string        `env:"CINEMA_CACHE_DIR" env-default:"/workspace/data/cinema-cache"`
	CacheMaxBytes int64         `env:"CINEMA_CACHE_MAX_BYTES" env-default:"2147483648"`
	Warmup        time.Duration `env:"CINEMA_WARMUP" env-default:"2s"`
	SessionTTL    time.Duration `env:"CINEMA_SESSION_TTL" env-default:"6h"`
}

// Load reads and checks the environment.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if len(c.InternalToken) < 24 {
		return nil, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes")
	}
	if len(c.SigningKey) < 32 {
		return nil, errors.New("STREAM_SIGNING_KEY must be at least 32 bytes")
	}
	if c.StreamTTL <= 0 || c.StreamTTL > time.Hour {
		return nil, errors.New("STREAM_URL_TTL must be between 0 and 1h")
	}
	for _, p := range strings.Split(c.ContentSources, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p != "p2p" && p != "licensed" {
			return nil, errors.New("CONTENT_SOURCES must be p2p, licensed, or both")
		}
	}
	if !source.Enabled(c.ContentSources, "p2p") && !source.Enabled(c.ContentSources, "licensed") {
		return nil, errors.New("CONTENT_SOURCES must include p2p or licensed")
	}
	return &c, nil
}

// P2P reports whether magnets may be handed to TorrServer.
func (c *Config) P2P() bool { return source.Enabled(c.ContentSources, "p2p") }
