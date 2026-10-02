package bot

import (
	"context"
	"fmt"

	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

// SetupAPI is the Telegram surface used on startup.
type SetupAPI interface {
	SetWebhook(ctx context.Context, url, secret string, allowed []string) error
	SetChatMenuButton(ctx context.Context, b telegram.MenuButton) error
	SetMyCommands(ctx context.Context, cmds []telegram.BotCommand, lang string) error
}

// SetupConfig describes what to register.
type SetupConfig struct {
	WebhookURL    string
	WebhookSecret string
	MenuText      string
}

// Setup registers the webhook (secret_token), the web_app menu button and
// localized commands. Idempotent — safe on every start.
func (b *Bot) Setup(ctx context.Context, api SetupAPI, c SetupConfig) error {
	if err := api.SetWebhook(ctx, c.WebhookURL, c.WebhookSecret, []string{"message"}); err != nil {
		return fmt.Errorf("setWebhook: %w", err)
	}
	if err := api.SetChatMenuButton(ctx, telegram.MenuButton{
		Type: "web_app", Text: c.MenuText, WebApp: &telegram.WebAppInfo{URL: b.apps.WavePlayer},
	}); err != nil {
		return fmt.Errorf("setChatMenuButton: %w", err)
	}
	if err := api.SetMyCommands(ctx, b.Commands(i18n.RU), ""); err != nil {
		return fmt.Errorf("setMyCommands: %w", err)
	}
	for _, l := range i18n.Langs {
		if err := api.SetMyCommands(ctx, b.Commands(l), string(l)); err != nil {
			return fmt.Errorf("setMyCommands %s: %w", l, err)
		}
	}
	return nil
}
