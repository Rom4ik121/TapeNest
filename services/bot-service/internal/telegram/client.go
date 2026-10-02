package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// APIError is a Bot API error response.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Client calls the Bot API. The token is never included in returned errors or logs.
type Client struct {
	base   string
	token  string
	http   *http.Client
	upload *http.Client // long timeout for file uploads
}

// NewClient creates a client; base is e.g. https://api.telegram.org.
func NewClient(base, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: hc, upload: &http.Client{Timeout: 10 * time.Minute}}
}

// IsNotModified reports Telegram's "message is not modified" (harmless on edits).
func IsNotModified(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == http.StatusBadRequest && strings.Contains(e.Description, "message is not modified")
}

// IsTooLarge reports a rejected upload (413 Request Entity Too Large).
func IsTooLarge(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Code == http.StatusRequestEntityTooLarge || strings.Contains(strings.ToLower(e.Description), "too large") || strings.Contains(strings.ToLower(e.Description), "too big"))
}

// IsUnreachable reports a permanent delivery failure for the chat: the user blocked
// the bot (403) or the chat does not exist (400 "chat not found").
func IsUnreachable(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == http.StatusForbidden || (e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Description), "chat not found"))
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// redact removes the bot token from error texts (net/http errors embed the URL).
func (c *Client) redact(err error) error {
	if err == nil || c.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<redacted>"))
}

// Call invokes method with params and decodes result into out (may be nil).
// A 429 with retry_after ≤ 5 s is retried once.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.call(ctx, method, params, out)
		var apiErr *APIError
		if attempt == 0 && errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests &&
			apiErr.RetryAfter > 0 && apiErr.RetryAfter <= 5 {
			select {
			case <-time.After(time.Duration(apiErr.RetryAfter) * time.Second):
				continue
			case <-ctx.Done():
				return fmt.Errorf("telegram %s: %w", method, ctx.Err())
			}
		}
		return err
	}
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: marshal: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, c.redact(err))
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(c.http, req, method, out)
}

func (c *Client) do(hc *http.Client, req *http.Request, method string, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, c.redact(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		return &APIError{Method: method, Code: resp.StatusCode, Description: "Request Entity Too Large"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: read: %w", method, c.redact(err))
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("telegram %s: http %d: bad response", method, resp.StatusCode)
	}
	if !r.OK {
		e := &APIError{Method: method, Code: r.ErrorCode, Description: r.Description}
		if r.Parameters != nil {
			e.RetryAfter = r.Parameters.RetryAfter
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// SendMessage sends a text message and returns it (message_id is needed for edits).
func (c *Client) SendMessage(ctx context.Context, m SendMessage) (Message, error) {
	var out Message
	err := c.Call(ctx, "sendMessage", m, &out)
	return out, err
}

// EditMessageText edits a bot message; "message is not modified" is not an error.
func (c *Client) EditMessageText(ctx context.Context, m EditMessageText) error {
	if err := c.Call(ctx, "editMessageText", m, nil); err != nil && !IsNotModified(err) {
		return err
	}
	return nil
}

// SendFile uploads u with sendVideo (supports_streaming) or sendDocument. The body
// is streamed with an exact Content-Length, so nothing is buffered in memory.
func (c *Client) SendFile(ctx context.Context, u Upload) error {
	method, field := "sendVideo", "video"
	if u.AsDocument {
		method, field = "sendDocument", "document"
	}
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	fields := [][2]string{{"chat_id", strconv.FormatInt(u.ChatID, 10)}}
	if u.Caption != "" {
		fields = append(fields, [2]string{"caption", u.Caption})
	}
	if u.ReplyToMessageID != 0 {
		rp, _ := json.Marshal(ReplyParameters{MessageID: u.ReplyToMessageID, AllowSendingWithoutReply: true})
		fields = append(fields, [2]string{"reply_parameters", string(rp)})
	}
	if !u.AsDocument {
		fields = append(fields, [2]string{"supports_streaming", "true"})
		for k, v := range map[string]int{"duration": u.DurationSec, "width": u.Width, "height": u.Height} {
			if v > 0 {
				fields = append(fields, [2]string{k, strconv.Itoa(v)})
			}
		}
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return fmt.Errorf("telegram %s: form: %w", method, err)
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, quoteFileName(u.FileName)))
	ct := u.MimeType
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	if _, err := mw.CreatePart(h); err != nil {
		return fmt.Errorf("telegram %s: form: %w", method, err)
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n"
	body := io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(u.Body, u.Size), strings.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, body)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, c.redact(err))
	}
	req.ContentLength = int64(head.Len()) + u.Size + int64(len(tail))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(c.upload, req, method, nil)
}

// quoteFileName keeps a multipart filename safe (no quotes/newlines).
func quoteFileName(s string) string {
	r := strings.NewReplacer(`"`, "'", "\r", "", "\n", "", `\`, "_")
	if s = r.Replace(s); s == "" {
		return "video.mp4"
	}
	return s
}

// SetWebhook registers the webhook with a secret token.
func (c *Client) SetWebhook(ctx context.Context, url, secret string, allowed []string) error {
	return c.Call(ctx, "setWebhook", map[string]any{
		"url": url, "secret_token": secret, "allowed_updates": allowed, "max_connections": 40,
	}, nil)
}

// GetWebhookInfo returns the current webhook status.
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	err := c.Call(ctx, "getWebhookInfo", map[string]any{}, &info)
	return info, err
}

// SetChatMenuButton sets the default menu button for all private chats.
func (c *Client) SetChatMenuButton(ctx context.Context, b MenuButton) error {
	return c.Call(ctx, "setChatMenuButton", map[string]any{"menu_button": b}, nil)
}

// SetMyCommands sets the command list for a language ("" = default).
func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand, lang string) error {
	p := map[string]any{"commands": cmds}
	if lang != "" {
		p["language_code"] = lang
	}
	return c.Call(ctx, "setMyCommands", p, nil)
}

// GetMe returns the bot user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.Call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}
