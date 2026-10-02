package app_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/tapenest/tapenest/services/music-service/internal/app"
	"github.com/tapenest/tapenest/services/music-service/internal/config"
)

func TestNewLogger(t *testing.T) {
	if app.NewLogger("debug", "api") == nil || app.NewLogger("nonsense", "worker") == nil {
		t.Fatal("logger")
	}
}

func TestOpen(t *testing.T) {
	dsn, rurl := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dsn == "" || rurl == "" {
		t.Skip("TEST_DATABASE_URL / TEST_REDIS_URL not set")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	cfg := &config.Config{DatabaseURL: dsn, ReplicaURL: dsn, RedisURL: rurl, NavidromeURL: "http://127.0.0.1:1", NavidromeUser: "u", NavidromePassword: "p"}
	inf, err := app.Open(ctx, cfg, log, true)
	if err != nil {
		t.Fatal(err)
	}
	defer inf.Close()
	if inf.PingPG(ctx) != nil || inf.PingRedis(ctx) != nil || inf.Store == nil || inf.Replica == nil {
		t.Fatal("open")
	}
	bad := *cfg
	bad.RedisURL = "::"
	if _, err := app.Open(ctx, &bad, log, false); err == nil {
		t.Fatal("bad redis url")
	}
	bad = *cfg
	bad.NavidromeURL = "nope"
	if _, err := app.Open(ctx, &bad, log, false); err == nil {
		t.Fatal("bad navidrome url")
	}
	bad = *cfg
	bad.DatabaseURL = "::bad"
	if _, err := app.Open(ctx, &bad, log, false); err == nil {
		t.Fatal("bad db url")
	}
}
