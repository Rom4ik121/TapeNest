// Package config loads reco-service settings from the environment (cleanenv).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"

	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
)

// Config is shared by cmd/server (API) and cmd/worker. Secrets only from env.
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"dev"`
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"RECO_SERVICE_PORT" env-default:"8086"`
	WorkerPort int    `env:"RECO_WORKER_PORT" env-default:"8087"`

	DatabaseURL    string `env:"DATABASE_URL"`
	RedisURL       string `env:"REDIS_URL"`
	InternalToken  string `env:"INTERNAL_API_TOKEN"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	// music-service internal API (catalog export, audio, backfill)
	MusicURL string `env:"MUSIC_SERVICE_URL" env-default:"http://127.0.0.1:8084"`

	FFmpegBin         string        `env:"FFMPEG_BIN" env-default:"ffmpeg"`
	AnalyzeWorkers    int           `env:"RECO_ANALYZE_WORKERS" env-default:"2"`
	CatalogInterval   time.Duration `env:"RECO_CATALOG_INTERVAL" env-default:"10m"`
	TrainInterval     time.Duration `env:"RECO_TRAIN_INTERVAL" env-default:"15m"`
	ModelPoll         time.Duration `env:"RECO_MODEL_POLL" env-default:"20s"`
	WeightsJSON       string        `env:"RECO_WEIGHTS"` // partial JSON overrides of rank.Weights
	BackfillDays      int           `env:"RECO_BACKFILL_DAYS" env-default:"90"`
	IngestedRetention time.Duration `env:"RECO_INGESTED_RETENTION" env-default:"720h"`

	Weights rank.Weights `env:"-"`
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
	for name, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if u, err := url.Parse(c.MusicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("MUSIC_SERVICE_URL must be an absolute http(s) URL"))
	}
	if c.AnalyzeWorkers < 1 || c.AnalyzeWorkers > 16 {
		errs = append(errs, errors.New("RECO_ANALYZE_WORKERS must be 1..16"))
	}
	if c.CatalogInterval < time.Minute || c.TrainInterval < time.Minute || c.ModelPoll < time.Second {
		errs = append(errs, errors.New("RECO_CATALOG_INTERVAL ≥ 1m, RECO_TRAIN_INTERVAL ≥ 1m, RECO_MODEL_POLL ≥ 1s"))
	}
	c.Weights = rank.DefaultWeights()
	if c.WeightsJSON != "" {
		if err := json.Unmarshal([]byte(c.WeightsJSON), &c.Weights); err != nil {
			errs = append(errs, errors.New("RECO_WEIGHTS must be a JSON object of rank weights"))
		}
	}
	return errors.Join(errs...)
}
