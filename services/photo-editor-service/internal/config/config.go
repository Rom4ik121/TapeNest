// Package config loads photo-editor-service settings from the environment.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is the process configuration. Secrets come only from env.
type Config struct {
	LogLevel       string        `env:"LOG_LEVEL" env-default:"info"`
	Port           int           `env:"PHOTO_EDITOR_PORT" env-default:"8098"`
	DatabaseURL    string        `env:"DATABASE_URL" env-required:"true"`
	MigrateOnStart bool          `env:"MIGRATE_ON_START" env-default:"true"`
	InternalToken  string        `env:"INTERNAL_API_TOKEN" env-required:"true"`
	S3Endpoint     string        `env:"S3_ENDPOINT" env-required:"true"`
	S3AccessKey    string        `env:"S3_ACCESS_KEY" env-required:"true"`
	S3SecretKey    string        `env:"S3_SECRET_KEY" env-required:"true"`
	S3UseSSL       bool          `env:"S3_USE_SSL" env-default:"false"`
	S3Region       string        `env:"S3_REGION" env-default:"us-east-1"`
	Bucket         string        `env:"S3_BUCKET_PHOTOS" env-default:"photos"`
	PublicURL      string        `env:"S3_PUBLIC_URL"`
	PresignTTL     time.Duration `env:"PRESIGN_TTL" env-default:"1h"`
	MaxBytes       int64         `env:"PHOTO_MAX_BYTES" env-default:"15728640"`
}

// Load reads and checks the environment.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(c.InternalToken) < 24 {
		return nil, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes")
	}
	if c.PresignTTL <= 0 || c.PresignTTL > time.Hour {
		return nil, errors.New("PRESIGN_TTL must be 1s..1h")
	}
	if c.MaxBytes < 1<<20 {
		return nil, errors.New("PHOTO_MAX_BYTES must be at least 1 MiB")
	}
	return &c, nil
}
