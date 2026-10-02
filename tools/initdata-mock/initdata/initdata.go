// Package initdata signs and validates Telegram Mini App initData
// (https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app).
//
// secret_key = HMAC_SHA256(key="WebAppData", msg=<bot token>)
// hash       = hex(HMAC_SHA256(key=secret_key, msg=data_check_string))
// data_check_string = "k=v" pairs (all fields except hash), sorted by key, joined by "\n".
package initdata

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// User mirrors the Telegram WebAppUser object (snake_case JSON as Telegram sends it).
type User struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
	IsPremium    bool   `json:"is_premium,omitempty"`
	PhotoURL     string `json:"photo_url,omitempty"`
	AllowsWrite  bool   `json:"allows_write_to_pm,omitempty"`
}

// Params are the fields put into initData.
type Params struct {
	User         User
	AuthDate     time.Time
	QueryID      string
	StartParam   string
	ChatInstance string
	ChatType     string
	// Signature is Telegram's Ed25519 third-party signature. It cannot be
	// forged locally; a placeholder is used because client SDKs
	// (@telegram-apps/sdk 2.11) require the field to be present. It is part of
	// data_check_string, so the HMAC stays valid.
	Signature string
}

// Validation errors.
var (
	ErrNoHash      = errors.New("initdata: hash is missing")
	ErrBadHash     = errors.New("initdata: hash mismatch")
	ErrExpired     = errors.New("initdata: auth_date is too old")
	ErrFuture      = errors.New("initdata: auth_date is in the future")
	ErrNoAuthDate  = errors.New("initdata: auth_date is missing or invalid")
	ErrEmptyToken  = errors.New("initdata: bot token is empty")
	allowedSkewSec = int64(60)
)

func secretKey(botToken string) []byte {
	m := hmac.New(sha256.New, []byte("WebAppData"))
	m.Write([]byte(botToken))
	return m.Sum(nil)
}

// DataCheckString builds the canonical string from parsed values (hash/signature excluded).
func DataCheckString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "hash" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values.Get(k))
	}
	return strings.Join(parts, "\n")
}

func computeHash(botToken string, values url.Values) string {
	m := hmac.New(sha256.New, secretKey(botToken))
	m.Write([]byte(DataCheckString(values)))
	return hex.EncodeToString(m.Sum(nil))
}

// Sign returns a raw initData query string signed with botToken.
func Sign(botToken string, p Params) (string, error) {
	if botToken == "" {
		return "", ErrEmptyToken
	}
	userJSON, err := json.Marshal(p.User)
	if err != nil {
		return "", fmt.Errorf("initdata: marshal user: %w", err)
	}
	authDate := p.AuthDate
	if authDate.IsZero() {
		authDate = time.Now()
	}
	v := url.Values{}
	v.Set("user", string(userJSON))
	v.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	if p.QueryID != "" {
		v.Set("query_id", p.QueryID)
	}
	if p.StartParam != "" {
		v.Set("start_param", p.StartParam)
	}
	if p.ChatInstance != "" {
		v.Set("chat_instance", p.ChatInstance)
	}
	if p.ChatType != "" {
		v.Set("chat_type", p.ChatType)
	}
	if p.Signature != "" {
		v.Set("signature", p.Signature)
	}
	v.Set("hash", computeHash(botToken, v))
	return v.Encode(), nil
}

// Validate checks the signature and auth_date TTL (spec §5.2: 24 h). now is injectable for tests.
func Validate(botToken, raw string, ttl time.Duration, now time.Time) (url.Values, error) {
	if botToken == "" {
		return nil, ErrEmptyToken
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("initdata: parse: %w", err)
	}
	got := values.Get("hash")
	if got == "" {
		return nil, ErrNoHash
	}
	want := computeHash(botToken, values)
	if !hmac.Equal([]byte(got), []byte(want)) {
		return nil, ErrBadHash
	}
	ts, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || ts <= 0 {
		return nil, ErrNoAuthDate
	}
	if ts > now.Unix()+allowedSkewSec {
		return nil, ErrFuture
	}
	if ttl > 0 && now.Sub(time.Unix(ts, 0)) > ttl {
		return nil, ErrExpired
	}
	return values, nil
}
