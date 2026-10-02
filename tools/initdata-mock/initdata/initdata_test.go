package initdata

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:TEST-token-not-real_abcdefghijklmnopqrstu"

func TestSignValidate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw, err := Sign(testToken, Params{
		User:       User{ID: 42, FirstName: "Рома", Username: "roma", LanguageCode: "ru"},
		AuthDate:   now.Add(-time.Hour),
		QueryID:    "AAH",
		StartParam: "wave",
		Signature:  "sig",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		token   string
		raw     string
		now     time.Time
		wantErr error
	}{
		{"valid", testToken, raw, now, nil},
		{"wrong token", testToken + "x", raw, now, ErrBadHash},
		{"expired (24h TTL)", testToken, raw, now.Add(24 * time.Hour), ErrExpired},
		{"future auth_date", testToken, raw, now.Add(-2 * time.Hour), ErrFuture},
		{"tampered user", testToken, strings.Replace(raw, "roma", "evil", 1), now, ErrBadHash},
		{"tampered signature", testToken, strings.Replace(raw, "signature=sig", "signature=sih", 1), now, ErrBadHash},
		{"no hash", testToken, "auth_date=1&user=%7B%7D", now, ErrNoHash},
		{"empty token", "", raw, now, ErrEmptyToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.token, tt.raw, 24*time.Hour, tt.now)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Known-answer test: data_check_string is sorted, hash excluded.
func TestDataCheckString(t *testing.T) {
	v := url.Values{"user": {`{"id":1}`}, "auth_date": {"10"}, "hash": {"x"}, "query_id": {"q"}}
	got := DataCheckString(v)
	want := "auth_date=10\nquery_id=q\nuser={\"id\":1}"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSignEmptyToken(t *testing.T) {
	if _, err := Sign("", Params{}); !errors.Is(err, ErrEmptyToken) {
		t.Fatal(err)
	}
}
