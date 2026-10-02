// Package telegram is a minimal Bot API client (only the methods bot-service uses).
package telegram

import "io"

// Update is an incoming update (only the fields we subscribe to: message).
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message is a Telegram message.
type Message struct {
	MessageID       int64           `json:"message_id"`
	From            *User           `json:"from,omitempty"`
	Chat            Chat            `json:"chat"`
	Text            string          `json:"text,omitempty"`
	Entities        []MessageEntity `json:"entities,omitempty"`
	Caption         string          `json:"caption,omitempty"`
	CaptionEntities []MessageEntity `json:"caption_entities,omitempty"`
}

// User is a Telegram user.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// Chat is a Telegram chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// MessageEntity marks a span of text; Offset/Length are in UTF-16 code units.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty"`
}

// WebAppInfo points at a mini app.
type WebAppInfo struct {
	URL string `json:"url"`
}

// InlineKeyboardButton is one inline button (web_app or url).
type InlineKeyboardButton struct {
	Text   string      `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
	URL    string      `json:"url,omitempty"`
}

// InlineKeyboardMarkup is an inline keyboard.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// ReplyParameters makes a message a reply.
type ReplyParameters struct {
	MessageID                int64 `json:"message_id"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply"`
}

// SendMessage are sendMessage params.
type SendMessage struct {
	ChatID             int64                 `json:"chat_id"`
	Text               string                `json:"text"`
	ParseMode          string                `json:"parse_mode,omitempty"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	ReplyParameters    *ReplyParameters      `json:"reply_parameters,omitempty"`
	LinkPreviewOptions *LinkPreviewOptions   `json:"link_preview_options,omitempty"`
}

// LinkPreviewOptions controls link previews.
type LinkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

// MenuButton is a chat menu button (type web_app).
type MenuButton struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

// BotCommand is one command for setMyCommands.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// WebhookInfo is the getWebhookInfo result.
type WebhookInfo struct {
	URL                  string `json:"url"`
	PendingUpdateCount   int    `json:"pending_update_count"`
	LastErrorDate        int64  `json:"last_error_date,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	HasCustomCertificate bool   `json:"has_custom_certificate"`
}

// EditMessageText are editMessageText params.
type EditMessageText struct {
	ChatID             int64                 `json:"chat_id"`
	MessageID          int64                 `json:"message_id"`
	Text               string                `json:"text"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	LinkPreviewOptions *LinkPreviewOptions   `json:"link_preview_options,omitempty"`
}

// Upload is a file sent with sendVideo (mp4) or sendDocument (other types) as
// multipart/form-data. Body is streamed; Size must be exact (Content-Length).
type Upload struct {
	ChatID           int64
	ReplyToMessageID int64
	Caption          string
	FileName         string
	MimeType         string
	Size             int64
	Body             io.Reader
	DurationSec      int
	Width, Height    int
	AsDocument       bool
}
