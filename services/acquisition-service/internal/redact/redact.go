// Package redact scrubs credentials from strings and errors before they reach
// logs or API responses: *arr API keys travel in query strings (Prowlarr download
// links carry ?apikey=…), qBittorrent cookies in headers. Stage-3 lesson (two
// Navidrome leaks): never log a raw URL or a transport error of an authenticated call.
package redact

import (
	"errors"
	"net/url"
	"regexp"
)

var secretParam = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|token|passkey|password|pass|auth|sig|signature|key|t|s|u|p|jackett_apikey)=([^&\s"'<>]+)`)

// String replaces credential-looking query parameter values with "REDACTED".
func String(s string) string {
	return secretParam.ReplaceAllString(s, "$1=REDACTED")
}

// URL returns scheme://host/path of a raw URL (no query, no userinfo).
func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid-url"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// Error wraps err so that its message is scrubbed; errors.Is/As still work.
func Error(err error) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return &scrubbed{msg: ue.Op + " " + URL(ue.URL) + ": " + String(innerMsg(ue.Err)), err: err}
	}
	return &scrubbed{msg: String(err.Error()), err: err}
}

func innerMsg(err error) string {
	if err == nil {
		return "failed"
	}
	return err.Error()
}

type scrubbed struct {
	msg string
	err error
}

func (s *scrubbed) Error() string { return s.msg }
func (s *scrubbed) Unwrap() error { return s.err }
