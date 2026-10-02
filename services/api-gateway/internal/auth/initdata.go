// Package auth implements Telegram initData validation, JWT issuing and
// refresh-token rotation (spec §5.2, §9).
package auth

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

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

// initData validation errors. All of them map to 401 at the transport layer.
var (
	ErrInitDataMalformed = errors.New("initdata: malformed")
	ErrInitDataSignature = errors.New("initdata: bad signature")
	ErrInitDataExpired   = errors.New("initdata: expired")
	ErrInitDataFuture    = errors.New("initdata: auth_date in the future")
)

const (
	maxInitDataLen = 8 << 10 // Telegram initData is ~1 KB; reject anything absurd early.
	clockSkew      = 60 * time.Second
)

// InitData is the validated content of Telegram initData.
type InitData struct {
	User       domain.TelegramProfile
	AuthDate   time.Time
	QueryID    string
	StartParam string
	ChatType   string
}

type tgUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	IsPremium    bool   `json:"is_premium"`
	PhotoURL     string `json:"photo_url"`
}

// InitDataValidator checks initData per
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app:
//
//	secret_key = HMAC_SHA256(key="WebAppData", msg=bot_token)
//	hash       = hex(HMAC_SHA256(key=secret_key, msg=data_check_string))
//
// data_check_string = every field except "hash", "k=v", sorted by key, joined by "\n".
// Replay protection = auth_date TTL (24 h by default) + future-skew check; the same
// initData is legitimately re-sent on every mini app reload within a session.
type InitDataValidator struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewInitDataValidator derives the HMAC secret once; the bot token is not kept.
func NewInitDataValidator(botToken string, ttl time.Duration, now func() time.Time) *InitDataValidator {
	m := hmac.New(sha256.New, []byte("WebAppData"))
	m.Write([]byte(botToken))
	if now == nil {
		now = time.Now
	}
	return &InitDataValidator{secret: m.Sum(nil), ttl: ttl, now: now}
}

// Sign computes the hash for values (used by tests and dev tooling).
func (v *InitDataValidator) Sign(values url.Values) string {
	m := hmac.New(sha256.New, v.secret)
	m.Write([]byte(dataCheckString(values)))
	return hex.EncodeToString(m.Sum(nil))
}

func dataCheckString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values.Get(k))
	}
	return b.String()
}

// Validate verifies signature and freshness and returns the parsed payload.
func (v *InitDataValidator) Validate(raw string) (InitData, error) {
	if raw == "" || len(raw) > maxInitDataLen {
		return InitData{}, ErrInitDataMalformed
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, fmt.Errorf("%w: %w", ErrInitDataMalformed, err)
	}
	for k, vs := range values {
		if len(vs) != 1 {
			return InitData{}, fmt.Errorf("%w: duplicate field %q", ErrInitDataMalformed, k)
		}
	}
	got, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(got) != sha256.Size {
		return InitData{}, ErrInitDataMalformed
	}
	want, _ := hex.DecodeString(v.Sign(values))
	if !hmac.Equal(got, want) {
		return InitData{}, ErrInitDataSignature
	}
	ts, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || ts <= 0 {
		return InitData{}, ErrInitDataMalformed
	}
	authDate := time.Unix(ts, 0)
	now := v.now()
	if authDate.After(now.Add(clockSkew)) {
		return InitData{}, ErrInitDataFuture
	}
	if v.ttl > 0 && now.Sub(authDate) > v.ttl {
		return InitData{}, ErrInitDataExpired
	}
	var u tgUser
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil || u.ID <= 0 {
		return InitData{}, fmt.Errorf("%w: user", ErrInitDataMalformed)
	}
	return InitData{
		User: domain.TelegramProfile{
			ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username,
			LanguageCode: u.LanguageCode, PhotoURL: u.PhotoURL, IsPremium: u.IsPremium,
		},
		AuthDate:   authDate,
		QueryID:    values.Get("query_id"),
		StartParam: values.Get("start_param"),
		ChatType:   values.Get("chat_type"),
	}, nil
}
