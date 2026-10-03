// Package bot turns Telegram updates into replies. No business logic lives here
// (spec §5.1): commands are answered from dictionaries, links go to api-gateway.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tapenest/tapenest/services/bot-service/internal/gateway"
	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/links"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

// Sender is the subset of the Telegram client the bot needs.
type Sender interface {
	SendMessage(ctx context.Context, m telegram.SendMessage) (telegram.Message, error)
	EditMessageText(ctx context.Context, m telegram.EditMessageText) error
}

// Downloader forwards links to api-gateway.
type Downloader interface {
	RequestDownload(ctx context.Context, req gateway.DownloadRequest, requestID string) (gateway.Outcome, error)
}

// MiniApps are the mini app URLs shown as web_app buttons.
type MiniApps struct {
	WavePlayer string
	Videos     string // library of this user's downloads; optional
}

// Bot handles updates.
type Bot struct {
	tg    Sender
	gw    Downloader
	texts *i18n.Bundle
	apps  MiniApps
	log   *slog.Logger
}

// New creates a Bot.
func New(tg Sender, gw Downloader, texts *i18n.Bundle, apps MiniApps, log *slog.Logger) *Bot {
	return &Bot{tg: tg, gw: gw, texts: texts, apps: apps, log: log}
}

// Commands returns the localized command list for setMyCommands.
func (b *Bot) Commands(l i18n.Lang) []telegram.BotCommand {
	cmds := []telegram.BotCommand{
		{Command: "start", Description: b.texts.T(l, "commands.start")},
		{Command: "help", Description: b.texts.T(l, "commands.help")},
	}
	if b.apps.Videos != "" {
		cmds = append(cmds, telegram.BotCommand{Command: "videos", Description: b.texts.T(l, "commands.videos")})
	}
	return cmds
}

func (b *Bot) keyboard(l i18n.Lang) *telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{{
		{Text: b.texts.T(l, "button.waveplayer"), WebApp: &telegram.WebAppInfo{URL: b.apps.WavePlayer}},
	}}
	if b.apps.Videos != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{
			{Text: b.texts.T(l, "button.videos"), WebApp: &telegram.WebAppInfo{URL: b.apps.Videos}},
		})
	}
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// parseCommand returns "start" for "/start payload" or "/start@MyBot".
func parseCommand(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	cmd := strings.Fields(text)[0][1:]
	cmd, _, _ = strings.Cut(cmd, "@")
	return strings.ToLower(cmd), true
}

// Handle processes one update. Only private chats are served (group support is Post-MVP).
func (b *Bot) Handle(ctx context.Context, u telegram.Update, requestID string) error {
	m := u.Message
	if m == nil || m.Chat.Type != "private" || m.From == nil || m.From.IsBot {
		return nil
	}
	lang := i18n.Detect(m.From.LanguageCode)
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	if cmd, ok := parseCommand(text); ok {
		return b.command(ctx, m, lang, cmd)
	}
	if found := links.Find(text, entities); len(found) > 0 {
		return b.link(ctx, m, lang, found, requestID)
	}
	return b.reply(ctx, m, b.texts.T(lang, "text.hint"), true)
}

func (b *Bot) command(ctx context.Context, m *telegram.Message, lang i18n.Lang, cmd string) error {
	switch cmd {
	case "start":
		text := b.texts.T(lang, "start.greeting_anon")
		if name := strings.TrimSpace(m.From.FirstName); name != "" {
			text = b.texts.T(lang, "start.greeting", "name", name)
		}
		return b.reply(ctx, m, text, true)
	case "help":
		return b.reply(ctx, m, b.texts.T(lang, "help.text", "sources", links.SourceNames()), true)
	case "videos", "video":
		return b.reply(ctx, m, b.texts.T(lang, "videos.open"), true)
	case "cinema", "cinenest", "film", "films", "movie", "movies":
		// Cinema is gone. Point at a pasted link and the download library.
		return b.reply(ctx, m, b.texts.T(lang, "videos.noCinema"), true)
	default:
		return b.reply(ctx, m, b.texts.T(lang, "command.unknown"), false)
	}
}

func (b *Bot) link(ctx context.Context, m *telegram.Message, lang i18n.Lang, found []links.Link, requestID string) error {
	var link *links.Link
	for i := range found {
		if found[i].Source != "" {
			link = &found[i]
			break
		}
	}
	if link == nil {
		return b.reply(ctx, m, b.texts.T(lang, "link.unsupported", "sources", links.SourceNames()), false)
	}
	// 1) acknowledge at once; this message is then edited with progress/result
	// (download events, internal/downloads) — one message per link, no chat spam.
	ack, err := b.sendMsg(ctx, m, b.texts.T(lang, "link.accepted", "source", string(link.Source)), nil)
	if err != nil {
		return err
	}
	outcome, gerr := b.gw.RequestDownload(ctx, gateway.DownloadRequest{
		TelegramID: m.From.ID, ChatID: m.Chat.ID, URL: link.URL, FirstName: m.From.FirstName,
		LastName: m.From.LastName, Username: m.From.Username, LanguageCode: m.From.LanguageCode,
		StatusMessageID: ack.MessageID, ReplyToMessageID: m.MessageID,
	}, requestID)
	if gerr != nil {
		b.log.WarnContext(ctx, "download request failed", "outcome", outcome, "err", gerr, "request_id", requestID)
	}
	var key string
	var kb *telegram.InlineKeyboardMarkup
	switch outcome {
	case gateway.Accepted:
		return nil // progress and the file arrive via download events
	case gateway.NotImplemented:
		key, kb = "link.not_implemented", b.keyboard(lang)
	case gateway.Invalid:
		key = "link.invalid"
	case gateway.Unsupported:
		key = "link.unsupported"
	case gateway.Playlist:
		key = "link.playlist"
	case gateway.QuotaActive:
		key = "link.quota_active"
	case gateway.QuotaDaily:
		key = "link.quota_daily"
	case gateway.RateLimited:
		key = "link.rate_limited"
	case gateway.Unavailable:
		key = "link.unavailable"
	default:
		key = "link.error"
	}
	text := b.texts.T(lang, key, "source", string(link.Source), "sources", links.SourceNames())
	if err := b.tg.EditMessageText(ctx, telegram.EditMessageText{
		ChatID: m.Chat.ID, MessageID: ack.MessageID, Text: text, ReplyMarkup: kb,
		LinkPreviewOptions: &telegram.LinkPreviewOptions{IsDisabled: true},
	}); err != nil {
		return fmt.Errorf("edit status: %w", err)
	}
	return nil
}

func (b *Bot) reply(ctx context.Context, m *telegram.Message, text string, withApps bool) error {
	var kb *telegram.InlineKeyboardMarkup
	if withApps {
		kb = b.keyboard(i18n.Detect(m.From.LanguageCode))
	}
	return b.send(ctx, m, text, kb)
}

func (b *Bot) send(ctx context.Context, m *telegram.Message, text string, kb *telegram.InlineKeyboardMarkup) error {
	_, err := b.sendMsg(ctx, m, text, kb)
	return err
}

func (b *Bot) sendMsg(ctx context.Context, m *telegram.Message, text string, kb *telegram.InlineKeyboardMarkup) (telegram.Message, error) {
	msg := telegram.SendMessage{
		ChatID:             m.Chat.ID,
		Text:               text,
		ReplyParameters:    &telegram.ReplyParameters{MessageID: m.MessageID, AllowSendingWithoutReply: true},
		LinkPreviewOptions: &telegram.LinkPreviewOptions{IsDisabled: true},
		ReplyMarkup:        kb,
	}
	sent, err := b.tg.SendMessage(ctx, msg)
	if err != nil {
		return telegram.Message{}, fmt.Errorf("reply: %w", err)
	}
	return sent, nil
}
