// Command server runs bot-service: Telegram webhook receiver, /start and /help,
// mini app buttons, link forwarding to api-gateway and delivery of download
// results (download:events → progress edits, file upload or link).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/bot-service/internal/bot"
	"github.com/tapenest/tapenest/services/bot-service/internal/config"
	"github.com/tapenest/tapenest/services/bot-service/internal/downloads"
	"github.com/tapenest/tapenest/services/bot-service/internal/gateway"
	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
	"github.com/tapenest/tapenest/services/bot-service/internal/transport"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bot-service stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(cfg.LogLevel))); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "bot-service")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	texts, err := i18n.Load()
	if err != nil {
		return err
	}
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	tg := telegram.NewClient(cfg.TelegramAPI, cfg.BotToken, nil)
	b := bot.New(tg, gateway.New(cfg.GatewayURL, cfg.InternalToken, cfg.GatewayTimeout), texts,
		bot.MiniApps{WavePlayer: cfg.WavePlayerURL, CineNest: cfg.CineNestURL}, log)

	if cfg.SetupOnStart {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := b.Setup(sctx, tg, bot.SetupConfig{WebhookURL: cfg.WebhookURL, WebhookSecret: cfg.WebhookSecret, MenuText: cfg.MenuButton})
		cancel()
		if err != nil {
			return fmt.Errorf("telegram setup: %w", err)
		}
		if me, err := tg.GetMe(ctx); err == nil {
			log.Info("telegram setup done", "bot", "@"+me.Username, "webhook", cfg.WebhookURL, "miniapp", cfg.WavePlayerURL)
		}
	}

	if cfg.DownloadEvents {
		host, _ := os.Hostname()
		n := &downloads.Notifier{TG: tg, Texts: texts, HTTP: &http.Client{Timeout: 10 * time.Minute}, UploadLimit: cfg.UploadLimit, Redis: rdb, Log: log}
		c := &downloads.Consumer{Redis: rdb, Name: fmt.Sprintf("%s-%d", host, os.Getpid()), Handle: n.Handle, Log: log}
		go c.Run(ctx)
		log.Info("download events consumer started", "stream", downloads.Stream, "group", downloads.Group, "upload_limit", cfg.UploadLimit)
	}

	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port),
		Handler: transport.NewRouter(transport.Deps{
			Log: log, Bot: b, Redis: rdb, Secret: cfg.WebhookSecret, Path: cfg.WebhookPath,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "webhook_path", cfg.WebhookPath)
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
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
