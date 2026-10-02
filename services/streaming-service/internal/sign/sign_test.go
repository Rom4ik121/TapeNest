package sign

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	s := New("0123456789abcdef0123456789abcdef", time.Hour)
	now := time.Unix(1_700_000_000, 0)
	tok := s.Sign("sess", now)
	if err := s.Valid("sess", tok.Exp, tok.Sig, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Valid("sess", tok.Exp, tok.Sig, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expected expiry")
	}
	if err := s.Valid("other", tok.Exp, tok.Sig, now); err == nil {
		t.Fatal("expected mismatch")
	}
}
