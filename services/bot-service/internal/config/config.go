// Package config loads bot-service settings from the environment (cleanenv).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config of bot-service. Secrets come only from env (spec §9).
type Config struct {
	AppEnv   string `env:"APP_ENV" env-default:"dev"`
	LogLevel string `env:"LOG_LEVEL" env-default:"info"`
	Port     int    `env:"BOT_SERVICE_PORT" env-default:"8081"`

	BotToken      string `env:"TELEGRAM_BOT_TOKEN" env-required:"true"`
	TelegramAPI   string `env:"TELEGRAM_API_BASE" env-default:"https://api.telegram.org"`
	WebhookURL    string `env:"TELEGRAM_WEBHOOK_URL"`
	WebhookSecret string `env:"TELEGRAM_WEBHOOK_SECRET" env-required:"true"`
	WebhookPath   string `env:"TELEGRAM_WEBHOOK_PATH" env-default:"/tg/webhook"`
	SetupOnStart  bool   `env:"BOT_SETUP_ON_START" env-default:"true"`

	WavePlayerURL string `env:"MINIAPP_WAVEPLAYER_URL" env-required:"true"`
	// VideosURL opens the download library. Empty derives WavePlayerURL + #/videos.
	VideosURL  string `env:"MINIAPP_VIDEOS_URL"`
	MenuButton string `env:"BOT_MENU_BUTTON_TEXT" env-default:"WavePlayer"`

	GatewayURL     string        `env:"GATEWAY_URL" env-required:"true"`
	InternalToken  string        `env:"INTERNAL_API_TOKEN" env-required:"true"`
	GatewayTimeout time.Duration `env:"GATEWAY_TIMEOUT" env-default:"5s"`

	RedisURL string `env:"REDIS_URL" env-required:"true"`

	// download results (stage 2): consume download:events and deliver files
	DownloadEvents bool  `env:"DOWNLOAD_EVENTS_ENABLED" env-default:"true"`
	UploadLimit    int64 `env:"TELEGRAM_UPLOAD_LIMIT" env-default:"50000000"` // Bot API: 50 MB for bot uploads
}

// Telegram allows 1-256 chars of A-Z a-z 0-9 _ - in secret_token.
var secretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

// Load reads and validates the configuration.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	var errs []error
	if strings.TrimSpace(c.BotToken) == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN is required"))
	}
	if !secretPattern.MatchString(c.WebhookSecret) {
		errs = append(errs, errors.New("TELEGRAM_WEBHOOK_SECRET must be 16-256 chars of [A-Za-z0-9_-]"))
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if !strings.HasPrefix(c.WebhookPath, "/") {
		errs = append(errs, errors.New("TELEGRAM_WEBHOOK_PATH must start with /"))
	}
	mustHTTPS := map[string]string{"MINIAPP_WAVEPLAYER_URL": c.WavePlayerURL, "MINIAPP_VIDEOS_URL": c.VideosURL}
	if c.SetupOnStart {
		mustHTTPS["TELEGRAM_WEBHOOK_URL"] = c.WebhookURL
		if c.WebhookURL == "" {
			errs = append(errs, errors.New("TELEGRAM_WEBHOOK_URL is required when BOT_SETUP_ON_START=true"))
		}
	}
	for name, v := range mustHTTPS {
		if v == "" {
			continue
		}
		if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an https URL (Telegram requirement)", name))
		}
	}
	if c.UploadLimit <= 0 || c.UploadLimit > 2_000_000_000 {
		errs = append(errs, errors.New("TELEGRAM_UPLOAD_LIMIT must be 1..2000000000 bytes"))
	}
	if u, err := url.Parse(c.GatewayURL); err != nil || u.Host == "" {
		errs = append(errs, errors.New("GATEWAY_URL must be a URL"))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if c.VideosURL == "" {
		if u, err := url.Parse(c.WavePlayerURL); err == nil && u.Scheme == "https" && u.Host != "" {
			u.Fragment = "/videos"
			c.VideosURL = u.String()
		}
	}
	return nil
}
