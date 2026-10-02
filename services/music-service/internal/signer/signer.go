// Package signer issues and verifies short-lived signed URLs for audio streams and
// covers. <audio>/<img> cannot send the bearer token, so the gateway routes
// /api/v1/stream/* without JWT and music-service checks an HMAC-SHA256 signature
// instead (spec §9: presigned TTL ≤ 1 h).
package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrBadSignature is returned for missing, forged or expired signatures.
var ErrBadSignature = errors.New("invalid or expired signature")

// StreamPath and CoverPath are the public routes (via api-gateway).
const (
	StreamPath = "/api/v1/stream/tracks/"
	CoverPath  = "/api/v1/stream/covers/"
)

// Signer signs URLs with one key.
type Signer struct {
	key  []byte
	base string // optional absolute origin; "" → same-origin relative URLs
}

// New creates a signer. base is STREAM_PUBLIC_BASE (may be empty).
func New(key, base string) *Signer {
	return &Signer{key: []byte(key), base: strings.TrimRight(base, "/")}
}

func (s *Signer) mac(parts ...string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(strings.Join(parts, "|")))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// StreamURL returns the signed stream URL for a track and its expiry.
func (s *Signer) StreamURL(track, user uuid.UUID, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	q := url.Values{"exp": {e}, "u": {user.String()}, "sig": {s.mac("stream", track.String(), user.String(), e)}}
	return s.base + StreamPath + track.String() + "?" + q.Encode()
}

// VerifyStream checks a stream request; returns the user the URL was issued to.
func (s *Signer) VerifyStream(track uuid.UUID, q url.Values, now time.Time) (uuid.UUID, error) {
	e, u, sig := q.Get("exp"), q.Get("u"), q.Get("sig")
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || sig == "" {
		return uuid.Nil, ErrBadSignature
	}
	user, err := uuid.Parse(u)
	if err != nil {
		return uuid.Nil, ErrBadSignature
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac("stream", track.String(), user.String(), e))) {
		return uuid.Nil, ErrBadSignature
	}
	if now.Unix() > exp {
		return uuid.Nil, ErrBadSignature
	}
	return user, nil
}

// CoverURL returns a stable signed cover URL (no expiry: covers are public
// artwork and the URL must stay cacheable as immutable).
func (s *Signer) CoverURL(coverID string, size int) string {
	if coverID == "" {
		return ""
	}
	sz := strconv.Itoa(size)
	q := url.Values{"size": {sz}, "sig": {s.mac("cover", coverID, sz)}}
	return s.base + CoverPath + url.PathEscape(coverID) + "?" + q.Encode()
}

// VerifyCover checks a cover request and returns the size.
func (s *Signer) VerifyCover(coverID string, q url.Values) (int, error) {
	sz, sig := q.Get("size"), q.Get("sig")
	size, err := strconv.Atoi(sz)
	if err != nil || size < 16 || size > 1200 || sig == "" || coverID == "" {
		return 0, ErrBadSignature
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac("cover", coverID, sz))) {
		return 0, ErrBadSignature
	}
	return size, nil
}
