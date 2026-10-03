package config

import "testing"

func base(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "1:x")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "abcdefghijklmnop_-12")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://x.example/tg/webhook")
	t.Setenv("MINIAPP_WAVEPLAYER_URL", "https://x.example/")
	t.Setenv("GATEWAY_URL", "http://127.0.0.1:8080")
	t.Setenv("INTERNAL_API_TOKEN", "iiiiiiiiiiiiiiiiiiiiiiiiiiii")
	t.Setenv("REDIS_URL", "redis://127.0.0.1:6379/0")
}

func TestLoad(t *testing.T) {
	base(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8081 || c.WebhookPath != "/tg/webhook" || !c.SetupOnStart || c.MenuButton != "WavePlayer" {
		t.Fatalf("%+v", c)
	}
}

func TestValidation(t *testing.T) {
	for name, kv := range map[string][2]string{
		"short secret":     {"TELEGRAM_WEBHOOK_SECRET", "short"},
		"bad secret chars": {"TELEGRAM_WEBHOOK_SECRET", "abcdefghijklmnop!!!!"},
		"http miniapp":     {"MINIAPP_WAVEPLAYER_URL", "http://x.example/"},
		"no webhook url":   {"TELEGRAM_WEBHOOK_URL", ""},
		"bad path":         {"TELEGRAM_WEBHOOK_PATH", "tg"},
		"short internal":   {"INTERNAL_API_TOKEN", "x"},
		"empty token":      {"TELEGRAM_BOT_TOKEN", " "},
		"bad gateway":      {"GATEWAY_URL", "::"},
	} {
		t.Run(name, func(t *testing.T) {
			base(t)
			t.Setenv(kv[0], kv[1])
			if _, err := Load(); err == nil {
				t.Fatal("error expected")
			}
		})
	}
	base(t)
	t.Setenv("BOT_SETUP_ON_START", "false")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "")
	if _, err := Load(); err != nil {
		t.Fatalf("webhook url optional without setup: %v", err)
	}
}
