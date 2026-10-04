// Package config loads video-editor-service settings from the environment.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is the process configuration. Secrets come only from env.
type Config struct {
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"VIDEO_EDITOR_PORT" env-default:"8096"`
	WorkerPort int    `env:"VIDEO_EDITOR_WORKER_PORT" env-default:"8097"`

	DatabaseURL    string `env:"DATABASE_URL" env-required:"true"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`
	InternalToken  string `env:"INTERNAL_API_TOKEN" env-required:"true"`
	DownloadURL    string `env:"DOWNLOAD_SERVICE_URL" env-required:"true"`

	S3Endpoint  string        `env:"S3_ENDPOINT" env-required:"true"`
	S3AccessKey string        `env:"S3_ACCESS_KEY" env-required:"true"`
	S3SecretKey string        `env:"S3_SECRET_KEY" env-required:"true"`
	S3UseSSL    bool          `env:"S3_USE_SSL" env-default:"false"`
	S3Region    string        `env:"S3_REGION" env-default:"us-east-1"`
	MediaBucket string        `env:"S3_BUCKET_MEDIA" env-default:"media"`
	EditBucket  string        `env:"S3_BUCKET_EDITS" env-default:"video-edits"`
	PublicURL   string        `env:"S3_PUBLIC_URL"`
	PresignTTL  time.Duration `env:"PRESIGN_TTL" env-default:"1h"`

	FFmpegBin  string `env:"FFMPEG_BIN" env-default:"ffmpeg"`
	FFprobeBin string `env:"FFPROBE_BIN" env-default:"ffprobe"`
	FontFile   string `env:"VIDEO_FONT_FILE" env-default:"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"`
	WorkDir    string `env:"VIDEO_EDITOR_TMP" env-default:"/var/tmp/tapenest-edit"`
	MaxBytes   int64  `env:"VIDEO_EDITOR_MAX_BYTES" env-default:"2147483648"`
}

// Load reads and checks the environment.
func Load() (*Config, error) {
	var c Config
	if err := cleanenv.ReadEnv(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	var errs []error
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if c.PresignTTL <= 0 || c.PresignTTL > time.Hour {
		errs = append(errs, errors.New("PRESIGN_TTL must be 1s..1h"))
	}
	if c.MaxBytes < 1<<20 {
		errs = append(errs, errors.New("VIDEO_EDITOR_MAX_BYTES must be at least 1 MiB"))
	}
	for _, p := range []string{c.MediaBucket, c.EditBucket} {
		if strings.TrimSpace(p) == "" {
			errs = append(errs, errors.New("S3 bucket names are required"))
		}
	}
	return errors.Join(errs...)
}
