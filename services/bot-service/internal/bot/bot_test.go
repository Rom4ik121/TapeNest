package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tapenest/tapenest/services/bot-service/internal/gateway"
	"github.com/tapenest/tapenest/services/bot-service/internal/i18n"
	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

type fakeSender struct {
	sent    []telegram.SendMessage
	edits   []telegram.EditMessageText
	err     error
	editErr error
}

func (f *fakeSender) SendMessage(_ context.Context, m telegram.SendMessage) (telegram.Message, error) {
	f.sent = append(f.sent, m)
	return telegram.Message{MessageID: int64(100 + len(f.sent)), Chat: telegram.Chat{ID: m.ChatID}}, f.err
}

func (f *fakeSender) EditMessageText(_ context.Context, m telegram.EditMessageText) error {
	f.edits = append(f.edits, m)
	return f.editErr
}

type fakeGW struct {
	outcome gateway.Outcome
	err     error
	reqs    []gateway.DownloadRequest
}

func (f *fakeGW) RequestDownload(_ context.Context, r gateway.DownloadRequest, _ string) (gateway.Outcome, error) {
	f.reqs = append(f.reqs, r)
	return f.outcome, f.err
}

const waveURL = "https://wave.example/"

func newBot(t *testing.T, apps MiniApps) (*Bot, *fakeSender, *fakeGW) {
	t.Helper()
	texts, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, g := &fakeSender{}, &fakeGW{}
	return New(s, g, texts, apps, slog.New(slog.NewTextHandler(io.Discard, nil))), s, g
}

func msg(text, lang string) telegram.Update {
	return telegram.Update{UpdateID: 1, Message: &telegram.Message{
		MessageID: 10, Chat: telegram.Chat{ID: 99, Type: "private"}, Text: text,
		From: &telegram.User{ID: 99, FirstName: "Roma", LanguageCode: lang},
	}}
}

func TestStartAndHelp(t *testing.T) {
	b, s, _ := newBot(t, MiniApps{WavePlayer: waveURL})
	ctx := context.Background()
	for _, tc := range []struct{ text, lang, prefix string }{
		{"/start", "ru", "Привет, Roma!"},
		{"/start deeplink-payload", "en-GB", "Hi, Roma!"},
		{"/START@tapenest_bot", "en", "Hi, Roma!"},
		{"/help", "ru", "Что я умею"},
		{"/help", "en", "Here's what I can do"},
	} {
		s.sent = nil
		if err := b.Handle(ctx, msg(tc.text, tc.lang), "r"); err != nil {
			t.Fatal(err)
		}
		if len(s.sent) != 1 || !strings.HasPrefix(s.sent[0].Text, tc.prefix) {
			t.Fatalf("%s: %+v", tc.text, s.sent)
		}
		m := s.sent[0]
		if m.ChatID != 99 || m.ReplyParameters.MessageID != 10 || m.ReplyMarkup == nil {
			t.Fatalf("%s: reply shape %+v", tc.text, m)
		}
		kb := m.ReplyMarkup.InlineKeyboard
		if len(kb) != 1 || kb[0][0].WebApp == nil || kb[0][0].WebApp.URL != waveURL {
			t.Fatalf("%s: keyboard %+v", tc.text, kb)
		}
	}
	if strings.Contains(s.sent[0].Text, "{sources}") {
		t.Fatal("placeholders must be substituted")
	}
	// Nameless user and unknown command.
	u := msg("/start", "ru")
	u.Message.From.FirstName = " "
	s.sent = nil
	_ = b.Handle(ctx, u, "r")
	if !strings.HasPrefix(s.sent[0].Text, "Привет! ") {
		t.Fatal(s.sent[0].Text)
	}
	s.sent = nil
	_ = b.Handle(ctx, msg("/nope", "en"), "r")
	if !strings.Contains(s.sent[0].Text, "/help") || s.sent[0].ReplyMarkup != nil {
		t.Fatalf("unknown: %+v", s.sent[0])
	}
}

func TestLinks(t *testing.T) {
	b, s, g := newBot(t, MiniApps{WavePlayer: waveURL})
	ctx := context.Background()
	cases := []struct {
		outcome gateway.Outcome
		err     error
		lang    string
		want    string // text of the edited status message ("" = no edit)
		apps    bool
	}{
		{gateway.Accepted, nil, "en", "", false},
		{gateway.NotImplemented, nil, "ru", "Скачивание сейчас не подключено", true},
		{gateway.NotImplemented, nil, "en", "Downloading isn't enabled", true},
		{gateway.Invalid, nil, "en", "couldn't parse", false},
		{gateway.Unsupported, nil, "en", "YouTube, VK, RuTube", false},
		{gateway.Playlist, nil, "ru", "Плейлисты не скачиваю", false},
		{gateway.QuotaActive, nil, "en", "3 downloads in progress", false},
		{gateway.QuotaDaily, nil, "ru", "Дневной лимит", false},
		{gateway.RateLimited, nil, "en", "Too many requests", false},
		{gateway.Unavailable, errors.New("down"), "ru", "временно недоступно", false},
		{gateway.Failed, errors.New("500"), "en", "Something went wrong", false},
	}
	for _, tc := range cases {
		s.sent, s.edits, g.reqs = nil, nil, nil
		g.outcome, g.err = tc.outcome, tc.err
		if err := b.Handle(ctx, msg("look https://example.com and https://youtu.be/dQw4w9WgXcQ", tc.lang), "r"); err != nil {
			t.Fatal(err)
		}
		// 1) ack first, 2) gateway gets the ack id to edit later
		if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "YouTube") || s.sent[0].ReplyParameters.MessageID != 10 {
			t.Fatalf("ack: %+v", s.sent)
		}
		r := g.reqs
		if len(r) != 1 || r[0].URL != "https://youtu.be/dQw4w9WgXcQ" || r[0].TelegramID != 99 || r[0].ChatID != 99 ||
			r[0].StatusMessageID != 101 || r[0].ReplyToMessageID != 10 || r[0].LanguageCode != tc.lang {
			t.Fatalf("gateway request: %+v", r)
		}
		if tc.want == "" {
			if len(s.edits) != 0 {
				t.Fatalf("accepted: no edit expected, got %+v", s.edits)
			}
			continue
		}
		if len(s.edits) != 1 || s.edits[0].MessageID != 101 || !strings.Contains(s.edits[0].Text, tc.want) || (s.edits[0].ReplyMarkup != nil) != tc.apps {
			t.Fatalf("outcome %v: %+v", tc.outcome, s.edits)
		}
	}
	s.sent, g.reqs = nil, nil
	_ = b.Handle(ctx, msg("https://example.com/video", "en"), "r")
	if len(g.reqs) != 0 || !strings.Contains(s.sent[0].Text, "YouTube, VK, RuTube") {
		t.Fatalf("unsupported: %+v %+v", g.reqs, s.sent)
	}
	// Link in a media caption.
	s.sent, g.reqs = nil, nil
	u := msg("", "en")
	u.Message.Caption = "https://rutube.ru/video/1/"
	_ = b.Handle(ctx, u, "r")
	if len(g.reqs) != 1 {
		t.Fatal("caption links must be handled")
	}
	// ack failure stops before the gateway; edit failure surfaces
	s.err, g.reqs = errors.New("tg down"), nil
	if err := b.Handle(ctx, msg("https://youtu.be/dQw4w9WgXcQ", "en"), "r"); err == nil || len(g.reqs) != 0 {
		t.Fatal("ack error must stop processing")
	}
	s.err, s.editErr, g.outcome = nil, errors.New("edit failed"), gateway.Invalid
	if err := b.Handle(ctx, msg("https://youtu.be/dQw4w9WgXcQ", "en"), "r"); err == nil {
		t.Fatal("edit error must surface")
	}
}

func TestIgnoredAndHint(t *testing.T) {
	b, s, _ := newBot(t, MiniApps{WavePlayer: waveURL})
	ctx := context.Background()
	group := msg("/start", "ru")
	group.Message.Chat.Type = "group"
	botUser := msg("/start", "ru")
	botUser.Message.From.IsBot = true
	noFrom := msg("/start", "ru")
	noFrom.Message.From = nil
	for _, u := range []telegram.Update{{UpdateID: 5}, group, botUser, noFrom} {
		if err := b.Handle(ctx, u, "r"); err != nil || len(s.sent) != 0 {
			t.Fatalf("must be ignored: %+v", u)
		}
	}
	_ = b.Handle(ctx, msg("hello", "en"), "r")
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "Send me a video link") || s.sent[0].ReplyMarkup == nil {
		t.Fatalf("hint: %+v", s.sent)
	}
	s.err = errors.New("tg down")
	if err := b.Handle(ctx, msg("hello", "en"), "r"); err == nil {
		t.Fatal("send errors must surface")
	}
}

type fakeSetup struct {
	calls   []string
	failOn  string
	webhook [2]string
	menu    telegram.MenuButton
}

func (f *fakeSetup) rec(name string) error {
	f.calls = append(f.calls, name)
	if f.failOn == name {
		return errors.New("fail " + name)
	}
	return nil
}

func (f *fakeSetup) SetWebhook(_ context.Context, url, secret string, _ []string) error {
	f.webhook = [2]string{url, secret}
	return f.rec("webhook")
}

func (f *fakeSetup) SetChatMenuButton(_ context.Context, b telegram.MenuButton) error {
	f.menu = b
	return f.rec("menu")
}

func (f *fakeSetup) SetMyCommands(_ context.Context, _ []telegram.BotCommand, lang string) error {
	return f.rec("commands:" + lang)
}

func TestSetup(t *testing.T) {
	b, _, _ := newBot(t, MiniApps{WavePlayer: waveURL})
	f := &fakeSetup{}
	cfg := SetupConfig{WebhookURL: "https://x.example/tg/webhook", WebhookSecret: "s3cr3t", MenuText: "WavePlayer"}
	if err := b.Setup(context.Background(), f, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, ",") != "webhook,menu,commands:,commands:ru,commands:en" {
		t.Fatal(f.calls)
	}
	if f.webhook[0] != cfg.WebhookURL || f.webhook[1] != "s3cr3t" || f.menu.Type != "web_app" || f.menu.WebApp.URL != waveURL || f.menu.Text != "WavePlayer" {
		t.Fatalf("%+v %+v", f.webhook, f.menu)
	}
	for _, step := range []string{"webhook", "menu", "commands:", "commands:en"} {
		if err := b.Setup(context.Background(), &fakeSetup{failOn: step}, cfg); err == nil {
			t.Errorf("failure in %s must surface", step)
		}
	}
	if cmds := b.Commands(i18n.EN); len(cmds) != 2 || cmds[0].Command != "start" || cmds[1].Description == "" {
		t.Fatal(cmds)
	}
}

func TestCinemaCommandRemoved(t *testing.T) {
	b, s, _ := newBot(t, MiniApps{WavePlayer: waveURL})
	if cmds := b.Commands(i18n.RU); len(cmds) != 2 {
		t.Fatal(cmds)
	}
	if err := b.Handle(context.Background(), msg("/cinema", "en"), "r"); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "don't know") {
		t.Fatalf("%+v", s.sent)
	}
}
