// Command server runs api-gateway: Telegram auth (initData → JWT), rate limiting
// and routing to internal services. `server -migrate` applies migrations and exits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/api-gateway/internal/auth"
	"github.com/tapenest/tapenest/services/api-gateway/internal/config"
	"github.com/tapenest/tapenest/services/api-gateway/internal/ratelimit"
	"github.com/tapenest/tapenest/services/api-gateway/internal/repo"
	"github.com/tapenest/tapenest/services/api-gateway/internal/transport/httpapi"
	"github.com/tapenest/tapenest/services/api-gateway/internal/upstream"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()
	if err := run(*migrateOnly); err != nil {
		slog.Error("api-gateway stopped", "err", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", "api-gateway")
}

func run(migrateOnly bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if migrateOnly || cfg.MigrateOnStart {
		mctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := repo.Migrate(mctx, pool)
		cancel()
		if err != nil {
			return err
		}
		log.Info("migrations applied", "schema", repo.Schema)
		if migrateOnly {
			return nil
		}
	}

	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	validator := auth.NewInitDataValidator(cfg.BotToken, cfg.InitDataTTL, nil)
	jwtIssuer := auth.NewJWTIssuer(cfg.JWTSecret, cfg.JWTAccessTTL, nil)
	refresh := auth.NewRefreshStore(rdb, cfg.JWTRefreshTTL, cfg.JWTAccessTTL)
	authSvc := auth.NewService(validator, jwtIssuer, refresh, repo.NewUsers(pool), cfg.AdminTelegramIDs)

	uopt := upstream.Options{Timeout: cfg.UpstreamTimeout, InternalToken: cfg.InternalToken}
	var services [5]*upstream.Service
	for i, s := range []struct{ name, url string }{
		{"music", cfg.MusicServiceURL}, {"download", cfg.DownloadServiceURL}, {"streaming", cfg.StreamingServiceURL},
		{"video", cfg.VideoEditorURL}, {"photo", cfg.PhotoEditorURL},
	} {
		svc, err := upstream.New(s.name, s.url, uopt, httpapi.UpstreamErrorWriter(log))
		if err != nil {
			return err
		}
		services[i] = svc
		log.Info("upstream", "service", s.name, "configured", svc.Configured())
	}

	handler := httpapi.NewRouter(httpapi.Deps{
		Log:           log,
		Auth:          authSvc,
		AuthLimiter:   ratelimit.New(rdb, "gw:rl:auth", cfg.AuthRateLimitRPS, cfg.AuthRateLimitBurst, nil),
		APILimiter:    ratelimit.New(rdb, "gw:rl:api", cfg.RateLimitRPS, cfg.RateLimitBurst, nil),
		CORSOrigins:   cfg.CORSAllowedOrigins,
		TrustProxy:    cfg.TrustProxyHeaders,
		InternalToken: cfg.InternalToken,
		Ready: map[string]httpapi.Pinger{
			"postgres": pool,
			"redis":    httpapi.PingFunc(func(ctx context.Context) error { return rdb.Ping(ctx).Err() }),
		},
		Music: services[0], Download: services[1], Streaming: services[2],
		Video: services[3], Photo: services[4],
	})

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
