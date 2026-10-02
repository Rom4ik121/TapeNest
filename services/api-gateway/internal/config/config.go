// Package config loads api-gateway settings from the environment (cleanenv).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is the full service configuration. Secrets come only from env (spec §9).
type Config struct {
	AppEnv   string `env:"APP_ENV" env-default:"dev"`
	LogLevel string `env:"LOG_LEVEL" env-default:"info"`
	Port     int    `env:"API_GATEWAY_PORT" env-default:"8080"`

	BotToken    string        `env:"TELEGRAM_BOT_TOKEN" env-required:"true"`
	InitDataTTL time.Duration `env:"INITDATA_TTL" env-default:"24h"`

	JWTSecret     string        `env:"JWT_SECRET" env-required:"true"`
	JWTAccessTTL  time.Duration `env:"JWT_ACCESS_TTL" env-default:"15m"`
	JWTRefreshTTL time.Duration `env:"JWT_REFRESH_TTL" env-default:"720h"`

	InternalToken string `env:"INTERNAL_API_TOKEN" env-required:"true"`

	DatabaseURL    string `env:"DATABASE_URL" env-required:"true"`
	RedisURL       string `env:"REDIS_URL" env-required:"true"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" env-separator:","`
	TrustProxyHeaders  bool     `env:"TRUST_PROXY_HEADERS" env-default:"false"`

	RateLimitRPS       float64 `env:"RATE_LIMIT_RPS" env-default:"10"`
	RateLimitBurst     int     `env:"RATE_LIMIT_BURST" env-default:"40"`
	AuthRateLimitRPS   float64 `env:"AUTH_RATE_LIMIT_RPS" env-default:"1"`
	AuthRateLimitBurst int     `env:"AUTH_RATE_LIMIT_BURST" env-default:"20"`

	AdminTelegramIDsRaw string `env:"ADMIN_TELEGRAM_IDS"`
	AdminTelegramIDs    map[int64]bool

	MusicServiceURL     string        `env:"MUSIC_SERVICE_URL"`
	DownloadServiceURL  string        `env:"DOWNLOAD_SERVICE_URL"`
	StreamingServiceURL string        `env:"STREAMING_SERVICE_URL"`
	UpstreamTimeout     time.Duration `env:"UPSTREAM_TIMEOUT" env-default:"10s"`
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
	for name, v := range map[string]string{"TELEGRAM_BOT_TOKEN": c.BotToken, "DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be at least 32 bytes"))
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if c.JWTAccessTTL <= 0 || c.JWTRefreshTTL <= c.JWTAccessTTL {
		errs = append(errs, errors.New("JWT_REFRESH_TTL must be greater than JWT_ACCESS_TTL > 0"))
	}
	if c.RateLimitRPS <= 0 || c.RateLimitBurst < 1 || c.AuthRateLimitRPS <= 0 || c.AuthRateLimitBurst < 1 {
		errs = append(errs, errors.New("rate limits must be positive"))
	}
	ids, err := ParseIDs(c.AdminTelegramIDsRaw)
	if err != nil {
		errs = append(errs, err)
	}
	c.AdminTelegramIDs = ids
	origins := c.CORSAllowedOrigins[:0]
	for _, o := range c.CORSAllowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, strings.TrimRight(o, "/"))
		}
	}
	c.CORSAllowedOrigins = origins
	return errors.Join(errs...)
}

// ParseIDs parses a comma-separated list of Telegram ids.
func ParseIDs(raw string) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ADMIN_TELEGRAM_IDS: bad id %q", p)
		}
		out[id] = true
	}
	return out, nil
}
