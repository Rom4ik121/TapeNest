package ytm

import (
	"encoding/base64"
	"net/url"
	"strings"
)

const (
	// CoverVideoPrefix is a cover id that maps to i.ytimg.com for a video.
	CoverVideoPrefix = "ytimg-"
	// CoverURLPrefix is a cover id that stores an allow-listed thumbnail URL.
	CoverURLPrefix = "yturl-"
)

// VideoCover is the cover id for a YouTube video thumbnail.
func VideoCover(videoID string) string {
	if !ValidVideoID(videoID) {
		return ""
	}
	return CoverVideoPrefix + videoID
}

func allowedThumbHost(host string) bool {
	switch strings.ToLower(host) {
	case "i.ytimg.com", "yt3.ggpht.com", "yt3.googleusercontent.com", "lh3.googleusercontent.com":
		return true
	default:
		return false
	}
}

// EncodeThumb stores an https thumbnail URL as a cover id, or "" if the host
// is not a YouTube image host.
func EncodeThumb(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !allowedThumbHost(u.Hostname()) {
		return ""
	}
	return CoverURLPrefix + base64.RawURLEncoding.EncodeToString([]byte(u.String()))
}

// CoverURL resolves a cover id this package created. ok is false for anything else.
func CoverURL(id string) (string, bool) {
	switch {
	case strings.HasPrefix(id, CoverVideoPrefix):
		vid := strings.TrimPrefix(id, CoverVideoPrefix)
		if !ValidVideoID(vid) {
			return "", false
		}
		return "https://i.ytimg.com/vi/" + vid + "/hqdefault.jpg", true
	case strings.HasPrefix(id, CoverURLPrefix):
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, CoverURLPrefix))
		if err != nil {
			return "", false
		}
		if EncodeThumb(string(b)) == "" {
			return "", false
		}
		return string(b), true
	default:
		return "", false
	}
}
