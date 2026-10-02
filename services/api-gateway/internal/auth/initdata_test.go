package auth

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInitDataValidate(t *testing.T) {
	v := NewInitDataValidator(testBotToken, 24*time.Hour, fixedNow)
	good := signInitData(t, v, testNow.Add(-time.Hour), defaultUser(), map[string]string{"signature": "abc", "start_param": "wave"})

	got, err := v.Validate(good)
	if err != nil {
		t.Fatalf("valid initData rejected: %v", err)
	}
	if got.User.ID != 42 || got.User.FirstName != "Рома" || got.User.Username != "roma" || !got.User.IsPremium || got.User.LanguageCode != "ru" {
		t.Fatalf("unexpected user: %+v", got.User)
	}
	if got.StartParam != "wave" || got.QueryID == "" || !got.AuthDate.Equal(testNow.Add(-time.Hour)) {
		t.Fatalf("unexpected payload: %+v", got)
	}

	tamper := func(key, val string) string {
		vals, _ := url.ParseQuery(good)
		vals.Set(key, val)
		return vals.Encode()
	}
	other := NewInitDataValidator("999:other-bot", 24*time.Hour, fixedNow)

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", ErrInitDataMalformed},
		{"too long", strings.Repeat("a", maxInitDataLen+1), ErrInitDataMalformed},
		{"bad query", "%zz", ErrInitDataMalformed},
		{"no hash", tamper("hash", ""), ErrInitDataMalformed},
		{"non-hex hash", tamper("hash", strings.Repeat("z", 64)), ErrInitDataMalformed},
		{"short hash", tamper("hash", "abcd"), ErrInitDataMalformed},
		{"tampered user", tamper("user", `{"id":1,"first_name":"Eve"}`), ErrInitDataSignature},
		{"tampered auth_date", tamper("auth_date", "1"), ErrInitDataSignature},
		{"tampered signature field", tamper("signature", "zzz"), ErrInitDataSignature},
		{"other bot", signInitData(t, other, testNow, defaultUser(), nil), ErrInitDataSignature},
		{"duplicate field", good + "&user=x", ErrInitDataMalformed},
		{"expired", signInitData(t, v, testNow.Add(-25*time.Hour), defaultUser(), nil), ErrInitDataExpired},
		{"future", signInitData(t, v, testNow.Add(5*time.Minute), defaultUser(), nil), ErrInitDataFuture},
		{"no user id", signInitData(t, v, testNow, map[string]any{"first_name": "x"}, nil), ErrInitDataMalformed},
		{"user not json", func() string {
			vals := url.Values{"user": {"nope"}, "auth_date": {strconv.FormatInt(testNow.Unix(), 10)}}
			vals.Set("hash", v.Sign(vals))
			return vals.Encode()
		}(), ErrInitDataMalformed},
		{"bad auth_date", func() string {
			vals := url.Values{"user": {`{"id":1}`}, "auth_date": {"x"}}
			vals.Set("hash", v.Sign(vals))
			return vals.Encode()
		}(), ErrInitDataMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Validate(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestInitDataSkewAndNoTTL(t *testing.T) {
	v := NewInitDataValidator(testBotToken, 24*time.Hour, fixedNow)
	if _, err := v.Validate(signInitData(t, v, testNow.Add(30*time.Second), defaultUser(), nil)); err != nil {
		t.Fatalf("small clock skew must be accepted: %v", err)
	}
	noTTL := NewInitDataValidator(testBotToken, 0, fixedNow)
	if _, err := noTTL.Validate(signInitData(t, noTTL, testNow.Add(-1000*time.Hour), defaultUser(), nil)); err != nil {
		t.Fatalf("ttl=0 disables expiry: %v", err)
	}
	if NewInitDataValidator(testBotToken, time.Hour, nil).now == nil {
		t.Fatal("default clock must be set")
	}
}

// Known-answer test: data_check_string must follow Telegram's canonical form.
func TestDataCheckString(t *testing.T) {
	vals := url.Values{"b": {"2"}, "a": {"1"}, "hash": {"x"}, "c": {"x=y"}}
	if got := dataCheckString(vals); got != "a=1\nb=2\nc=x=y" {
		t.Fatalf("got %q", got)
	}
}
