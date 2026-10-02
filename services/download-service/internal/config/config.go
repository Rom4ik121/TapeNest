// Package config loads download-service settings from the environment (cleanenv).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

// Config is shared by cmd/server (API) and cmd/worker. Secrets only from env (spec §9).
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"dev"`
	LogLevel   string `env:"LOG_LEVEL" env-default:"info"`
	Port       int    `env:"DOWNLOAD_SERVICE_PORT" env-default:"8082"`
	WorkerPort int    `env:"DOWNLOAD_WORKER_PORT" env-default:"8083"` // worker /healthz, /readyz

	InternalToken string `env:"INTERNAL_API_TOKEN" env-required:"true"`

	DatabaseURL    string `env:"DATABASE_URL" env-required:"true"`
	RedisURL       string `env:"REDIS_URL" env-required:"true"`
	MigrateOnStart bool   `env:"MIGRATE_ON_START" env-default:"true"`

	S3Endpoint  string        `env:"S3_ENDPOINT" env-default:"127.0.0.1:9000"`
	S3AccessKey string        `env:"S3_ACCESS_KEY" env-required:"true"`
	S3SecretKey string        `env:"S3_SECRET_KEY" env-required:"true"`
	S3UseSSL    bool          `env:"S3_USE_SSL" env-default:"false"`
	S3Region    string        `env:"S3_REGION" env-default:"us-east-1"`
	S3Bucket    string        `env:"S3_BUCKET_MEDIA" env-default:"media"`
	S3PublicURL string        `env:"S3_PUBLIC_URL"` // e.g. https://app.example — /<bucket>/… is routed to MinIO
	PresignTTL  time.Duration `env:"PRESIGN_TTL" env-default:"1h"`
	Retention   time.Duration `env:"MEDIA_RETENTION" env-default:"168h"`
	// objects ≥ threshold are uploaded with multipart (spec: > 100 MB)
	MultipartThreshold int64 `env:"S3_MULTIPART_THRESHOLD" env-default:"104857600"`

	YtDlpPath       string        `env:"YTDLP_PATH" env-default:"yt-dlp"`
	YtDlpJSRuntimes string        `env:"YTDLP_JS_RUNTIMES" env-default:"deno"`
	FFmpegLocation  string        `env:"FFMPEG_LOCATION"`
	TmpDir          string        `env:"DOWNLOAD_TMP_DIR"`
	Concurrency     int           `env:"DOWNLOAD_WORKER_CONCURRENCY" env-default:"2"`
	JobTimeout      time.Duration `env:"DOWNLOAD_JOB_TIMEOUT" env-default:"30m"`
	DrainTimeout    time.Duration `env:"DOWNLOAD_DRAIN_TIMEOUT" env-default:"60s"`
	MaxFilesize     int64         `env:"DOWNLOAD_MAX_FILESIZE" env-default:"2147483648"`
	MaxDuration     time.Duration `env:"DOWNLOAD_MAX_DURATION" env-default:"3h"`
	MaxHeight       int           `env:"DOWNLOAD_MAX_HEIGHT" env-default:"720"`
	MinTGHeight     int           `env:"DOWNLOAD_MIN_TELEGRAM_HEIGHT" env-default:"360"`
	TelegramLimit   int64         `env:"TELEGRAM_UPLOAD_LIMIT" env-default:"50000000"`

	QuotaActive int `env:"DOWNLOAD_QUOTA_ACTIVE" env-default:"3"`
	QuotaDaily  int `env:"DOWNLOAD_QUOTA_DAILY" env-default:"30"`

	LimitYouTube int `env:"DOWNLOAD_LIMIT_YOUTUBE" env-default:"50"`
	LimitVK      int `env:"DOWNLOAD_LIMIT_VK" env-default:"100"`
	LimitRuTube  int `env:"DOWNLOAD_LIMIT_RUTUBE" env-default:"200"`

	ProxyDatacenter  []string      `env:"PROXY_POOL_DATACENTER" env-separator:","`
	ProxyResidential []string      `env:"PROXY_POOL_RESIDENTIAL" env-separator:","`
	ProxyMobile      []string      `env:"PROXY_POOL_MOBILE" env-separator:","`
	ProxyHealthEvery time.Duration `env:"PROXY_HEALTH_INTERVAL" env-default:"5m"`
	ProxyHealthURL   string        `env:"PROXY_HEALTH_URL" env-default:"https://www.gstatic.com/generate_204"`
	CookiesMaxAge    time.Duration `env:"COOKIES_MAX_AGE" env-default:"12h"`
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
	for name, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "S3_ACCESS_KEY": c.S3AccessKey, "S3_SECRET_KEY": c.S3SecretKey} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	if len(c.InternalToken) < 24 {
		errs = append(errs, errors.New("INTERNAL_API_TOKEN must be at least 24 bytes"))
	}
	if c.PresignTTL <= 0 || c.PresignTTL > time.Hour {
		errs = append(errs, errors.New("PRESIGN_TTL must be in (0, 1h] (spec §9)"))
	}
	if c.S3PublicURL != "" {
		u, err := url.Parse(c.S3PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("S3_PUBLIC_URL must be an absolute http(s) URL"))
		}
		c.S3PublicURL = strings.TrimRight(c.S3PublicURL, "/")
	}
	if c.Concurrency < 1 || c.QuotaActive < 1 || c.QuotaDaily < 1 || c.MaxHeight < 144 || c.TelegramLimit < 1 || c.MaxFilesize < 1 {
		errs = append(errs, errors.New("concurrency, quotas and size limits must be positive"))
	}
	if c.LimitYouTube < 1 || c.LimitVK < 1 || c.LimitRuTube < 1 {
		errs = append(errs, errors.New("per-domain limits must be positive"))
	}
	c.ProxyDatacenter, c.ProxyResidential, c.ProxyMobile = clean(c.ProxyDatacenter), clean(c.ProxyResidential), clean(c.ProxyMobile)
	for _, p := range append(append(append([]string{}, c.ProxyDatacenter...), c.ProxyResidential...), c.ProxyMobile...) {
		if u, err := url.Parse(p); err != nil || u.Host == "" {
			errs = append(errs, errors.New("proxy pools must contain proxy URLs (scheme://[user:pass@]host:port)"))
			break
		}
	}
	return errors.Join(errs...)
}

func clean(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// DomainLimits returns max parallel jobs per source (spec §5.3).
func (c *Config) DomainLimits() map[domain.Source]int {
	return map[domain.Source]int{domain.SourceYouTube: c.LimitYouTube, domain.SourceVK: c.LimitVK, domain.SourceRuTube: c.LimitRuTube}
}

// Limits returns format-selection limits.
func (c *Config) Limits() domain.Limits {
	return domain.Limits{MaxHeight: c.MaxHeight, MinTGHeight: c.MinTGHeight, TGLimit: c.TelegramLimit}
}
