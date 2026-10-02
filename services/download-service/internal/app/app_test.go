package app

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/tapenest/tapenest/services/download-service/internal/config"
)

func TestNewLogger(t *testing.T) {
	if !NewLogger("debug", "t").Enabled(context.Background(), -4) {
		t.Fatal("debug level")
	}
	if NewLogger("nonsense", "t").Enabled(context.Background(), -4) {
		t.Fatal("fallback is info")
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, &config.Config{DatabaseURL: "::bad"}, NewLogger("info", "t"), false); err == nil {
		t.Fatal("bad dsn")
	}
	cfg := &config.Config{DatabaseURL: "postgres://u:p@127.0.0.1:1/db", RedisURL: "nope://"}
	if _, err := Open(ctx, cfg, NewLogger("info", "t"), false); err == nil {
		t.Fatal("bad redis url")
	}
	cfg.RedisURL = "redis://127.0.0.1:1/0"
	cfg.S3PublicURL = "::bad"
	if _, err := Open(ctx, cfg, NewLogger("info", "t"), false); err == nil {
		t.Fatal("bad s3 config")
	}
	if _, err := Open(ctx, &config.Config{DatabaseURL: "postgres://u:p@127.0.0.1:1/db"}, NewLogger("info", "t"), true); err == nil {
		t.Fatal("migrate against unreachable db must fail")
	}
}

func TestOpenOK(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	mr := miniredis.RunT(t)
	infra, err := Open(context.Background(), &config.Config{
		DatabaseURL: dsn, RedisURL: "redis://" + mr.Addr() + "/0",
		S3Endpoint: "127.0.0.1:9000", S3Bucket: "media", S3Region: "us-east-1",
	}, NewLogger("info", "t"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer infra.Close()
	if infra.PingPG(context.Background()) != nil || infra.PingRedis(context.Background()) != nil {
		t.Fatal("pings")
	}
}
