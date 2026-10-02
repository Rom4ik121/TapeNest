// Package sign builds short-lived HMAC tokens for /hls URLs (native HLS cannot send a bearer).
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Signer signs session ids.
type Signer struct {
	key []byte
	ttl time.Duration
}

// New returns a signer. key must be at least 32 bytes.
func New(key string, ttl time.Duration) *Signer {
	if ttl <= 0 || ttl > time.Hour {
		ttl = time.Hour
	}
	return &Signer{key: []byte(key), ttl: ttl}
}

// Token is exp (unix seconds) plus hex HMAC.
type Token struct {
	Exp int64
	Sig string
}

// Sign returns a token valid for ttl.
func (s *Signer) Sign(sessionID string, now time.Time) Token {
	exp := now.Add(s.ttl).Unix()
	return Token{Exp: exp, Sig: s.mac(sessionID, exp)}
}

// Valid reports whether sig matches sessionID and exp is still in the future.
func (s *Signer) Valid(sessionID string, exp int64, sig string, now time.Time) error {
	if exp < now.Unix() {
		return errors.New("expired")
	}
	want := s.mac(sessionID, exp)
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return errors.New("bad signature")
	}
	return nil
}

func (s *Signer) mac(sessionID string, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	_, _ = m.Write([]byte(sessionID))
	_, _ = m.Write([]byte("|"))
	_, _ = m.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}
