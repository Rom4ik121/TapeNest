package signer

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStreamURL(t *testing.T) {
	s := New("k-0123456789abcdef0123456789abcdef", "")
	track, user := uuid.New(), uuid.New()
	now := time.Unix(1_800_000_000, 0)
	raw := s.StreamURL(track, user, now.Add(time.Hour))
	if !strings.HasPrefix(raw, StreamPath+track.String()+"?") {
		t.Fatal(raw)
	}
	u, _ := url.Parse(raw)
	got, err := s.VerifyStream(track, u.Query(), now)
	if err != nil || got != user {
		t.Fatalf("verify: %v %v", got, err)
	}
	cases := map[string]func(url.Values){
		"expired":   func(q url.Values) {},
		"forged":    func(q url.Values) { q.Set("sig", "AAAA") },
		"other exp": func(q url.Values) { q.Set("exp", "1900000000") },
		"bad user":  func(q url.Values) { q.Set("u", "x") },
		"bad exp":   func(q url.Values) { q.Set("exp", "x") },
		"no sig":    func(q url.Values) { q.Del("sig") },
	}
	for name, mut := range cases {
		q := u.Query()
		mut(q)
		at := now
		if name == "expired" {
			at = now.Add(2 * time.Hour)
		}
		if _, err := s.VerifyStream(track, q, at); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.VerifyStream(uuid.New(), u.Query(), now); err == nil {
		t.Error("other track must fail")
	}
	if _, err := New("other-key-0123456789abcdef0123456", "").VerifyStream(track, u.Query(), now); err == nil {
		t.Error("other key must fail")
	}
}

func TestCoverURL(t *testing.T) {
	s := New("k-0123456789abcdef0123456789abcdef", "https://app.example/")
	if s.CoverURL("", 300) != "" {
		t.Fatal("no cover → empty")
	}
	raw := s.CoverURL("al-1_0", 300)
	if !strings.HasPrefix(raw, "https://app.example"+CoverPath+"al-1_0?") {
		t.Fatal(raw)
	}
	u, _ := url.Parse(raw)
	if size, err := s.VerifyCover("al-1_0", u.Query()); err != nil || size != 300 {
		t.Fatal(size, err)
	}
	q := u.Query()
	q.Set("size", "600")
	if _, err := s.VerifyCover("al-1_0", q); err == nil {
		t.Error("size is signed")
	}
	for _, sz := range []string{"x", "5", "5000"} {
		q := u.Query()
		q.Set("size", sz)
		if _, err := s.VerifyCover("al-1_0", q); err == nil {
			t.Error(sz)
		}
	}
	if _, err := s.VerifyCover("al-2_0", u.Query()); err == nil {
		t.Error("other cover must fail")
	}
}
