package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "123456:SECRET-TOKEN-VALUE"

func TestCallSuccessAndMethods(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, strings.TrimPrefix(r.URL.Path, "/bot"+token))
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TapeNest","username":"tapenest_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77,"chat":{"id":1,"type":"private"},"text":"hi"}}`))
		case strings.HasSuffix(r.URL.Path, "/editMessageText") && b["text"] == "same":
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`))
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"https://x/tg/webhook","pending_update_count":0}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/", token, nil)
	ctx := context.Background()
	me, err := c.GetMe(ctx)
	if err != nil || me.Username != "tapenest_bot" {
		t.Fatalf("getMe: %v %+v", err, me)
	}
	info, err := c.GetWebhookInfo(ctx)
	if err != nil || info.URL != "https://x/tg/webhook" {
		t.Fatalf("info: %v", err)
	}
	if err := c.SetWebhook(ctx, "https://x/tg/webhook", "sec", []string{"message"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetChatMenuButton(ctx, MenuButton{Type: "web_app", Text: "W", WebApp: &WebAppInfo{URL: "https://w"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMyCommands(ctx, []BotCommand{{Command: "start", Description: "d"}}, "en"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMyCommands(ctx, nil, ""); err != nil {
		t.Fatal(err)
	}
	if m, err := c.SendMessage(ctx, SendMessage{ChatID: 1, Text: "hi"}); err != nil || m.MessageID != 77 {
		t.Fatal(m, err)
	}
	if err := c.EditMessageText(ctx, EditMessageText{ChatID: 1, MessageID: 77, Text: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := c.EditMessageText(ctx, EditMessageText{ChatID: 1, MessageID: 77, Text: "same"}); err != nil {
		t.Fatalf("not modified must be ignored: %v", err)
	}
	want := "/getMe,/getWebhookInfo,/setWebhook,/setChatMenuButton,/setMyCommands,/setMyCommands,/sendMessage,/editMessageText,/editMessageText"
	if strings.Join(paths, ",") != want {
		t.Fatal(paths)
	}
	if bodies[2]["secret_token"] != "sec" || bodies[4]["language_code"] != "en" || bodies[5]["language_code"] != nil {
		t.Fatalf("bodies: %v", bodies)
	}
	mb, _ := bodies[3]["menu_button"].(map[string]any)
	if mb["type"] != "web_app" {
		t.Fatalf("menu: %v", bodies[3])
	}
}

func TestErrorsAndRetry(t *testing.T) {
	calls := 0
	mode := "429"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		switch mode {
		case "429":
			if calls == 1 {
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`))
		case "400":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
		case "html":
			_, _ = w.Write([]byte(`<html>`))
		case "badresult":
			_, _ = w.Write([]byte(`{"ok":true,"result":"str"}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, token, &http.Client{Timeout: 5 * time.Second})
	ctx := context.Background()
	start := time.Now()
	if _, err := c.SendMessage(ctx, SendMessage{ChatID: 1, Text: "x"}); err != nil || calls != 2 || time.Since(start) < time.Second {
		t.Fatalf("429 must be retried once after retry_after: %v calls=%d", err, calls)
	}
	mode = "400"
	_, err := c.SendMessage(ctx, SendMessage{ChatID: 1})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("api error: %v", err)
	}
	mode = "html"
	if _, err := c.SendMessage(ctx, SendMessage{}); err == nil {
		t.Fatal("non-JSON must fail")
	}
	mode = "badresult"
	if _, err := c.GetMe(ctx); err == nil {
		t.Fatal("bad result must fail")
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	c := NewClient(srv.URL, token, nil)
	_, err := c.SendMessage(context.Background(), SendMessage{})
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("token leaked or no error: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("expected redaction marker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "getMe", nil, nil); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("cancelled: %v", err)
	}
}
