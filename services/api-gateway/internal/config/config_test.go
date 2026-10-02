package config

import (
	"strings"
	"testing"
	"time"
)

func setBase(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "1:x")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("i", 24))
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
}

func TestLoadDefaults(t *testing.T) {
	setBase(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://a.example/ ,, https://*.ngrok-free.app")
	t.Setenv("ADMIN_TELEGRAM_IDS", "1, 2")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 || c.JWTAccessTTL != 15*time.Minute || c.JWTRefreshTTL != 720*time.Hour || c.InitDataTTL != 24*time.Hour {
		t.Fatalf("defaults: %+v", c)
	}
	if len(c.CORSAllowedOrigins) != 2 || c.CORSAllowedOrigins[0] != "https://a.example" {
		t.Fatalf("origins: %q", c.CORSAllowedOrigins)
	}
	if !c.AdminTelegramIDs[1] || !c.AdminTelegramIDs[2] || len(c.AdminTelegramIDs) != 2 {
		t.Fatalf("admins: %v", c.AdminTelegramIDs)
	}
}

func TestLoadValidation(t *testing.T) {
	for name, env := range map[string][2]string{
		"short jwt secret":  {"JWT_SECRET", "short"},
		"short internal":    {"INTERNAL_API_TOKEN", "short"},
		"refresh <= access": {"JWT_REFRESH_TTL", "1m"},
		"bad admin id":      {"ADMIN_TELEGRAM_IDS", "1,abc"},
		"negative rate":     {"RATE_LIMIT_RPS", "-1"},
		"missing bot token": {"TELEGRAM_BOT_TOKEN", ""},
	} {
		t.Run(name, func(t *testing.T) {
			setBase(t)
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatal("error expected")
			}
		})
	}
}
