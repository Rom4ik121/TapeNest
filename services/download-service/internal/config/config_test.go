package config

import (
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("INTERNAL_API_TOKEN", strings.Repeat("x", 32))
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
}

func TestLoadDefaults(t *testing.T) {
	setBase(t)
	t.Setenv("PROXY_POOL_DATACENTER", " http://u:p@10.0.0.1:3128 , ,socks5://10.0.0.2:1080")
	t.Setenv("S3_PUBLIC_URL", "https://app.example/")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8082 || c.Concurrency != 2 || c.QuotaActive != 3 || c.QuotaDaily != 30 || c.TelegramLimit != 50_000_000 || c.S3Bucket != "media" {
		t.Fatalf("%+v", c)
	}
	if len(c.ProxyDatacenter) != 2 || c.S3PublicURL != "https://app.example" {
		t.Fatalf("%v %s", c.ProxyDatacenter, c.S3PublicURL)
	}
	if l := c.DomainLimits(); l["youtube"] != 50 || l["vk"] != 100 || l["rutube"] != 200 {
		t.Fatal(l)
	}
	if lim := c.Limits(); lim.MaxHeight != 720 || lim.MinTGHeight != 360 {
		t.Fatal(lim)
	}
}

func TestLoadValidation(t *testing.T) {
	for name, env := range map[string][2]string{
		"short token":  {"INTERNAL_API_TOKEN", "short"},
		"presign ttl":  {"PRESIGN_TTL", "2h"},
		"public url":   {"S3_PUBLIC_URL", "not a url"},
		"quota":        {"DOWNLOAD_QUOTA_DAILY", "0"},
		"domain limit": {"DOWNLOAD_LIMIT_VK", "0"},
		"proxy":        {"PROXY_POOL_MOBILE", "::bad"},
		"missing s3":   {"S3_SECRET_KEY", " "},
	} {
		t.Run(name, func(t *testing.T) {
			setBase(t)
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
