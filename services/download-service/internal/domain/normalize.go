// Package domain holds download-service business types and pure logic:
// URL normalization, error classification and retry strategy (spec §5.3).
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Source is a supported video site.
type Source string

// Supported sources (spec §5.3; per-domain limits in config).
const (
	SourceYouTube Source = "youtube"
	SourceVK      Source = "vk"
	SourceRuTube  Source = "rutube"
)

// Sources lists all supported sources in display order.
var Sources = []Source{SourceYouTube, SourceVK, SourceRuTube}

// URL validation errors (mapped to 400 codes by the transport layer).
var (
	ErrInvalidURL  = errors.New("invalid url")
	ErrUnsupported = errors.New("unsupported source")
	ErrPlaylist    = errors.New("playlists are not supported")
)

// Normalized is a canonical video URL: one video → one string → one SHA-256 (dedup key).
type Normalized struct {
	URL        string
	Source     Source
	ExternalID string
}

// Hash is the dedup key (hex SHA-256 of the canonical URL).
func (n Normalized) Hash() string {
	sum := sha256.Sum256([]byte(n.URL))
	return hex.EncodeToString(sum[:])
}

var (
	ytID     = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vkVideo  = regexp.MustCompile(`^(?:video|clip)(-?\d+_\d+)$`)
	rutubeID = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// NormalizeURL canonicalizes a user link (spec §5.3): tracking parameters (utm_*, si,
// feature, t, …) are dropped, youtu.be/m.youtube/shorts become youtube.com/watch?v=,
// VK wall/clip links become vk.com/video…, RuTube links keep only the private `p` key.
// Playlists are rejected. The result is stable, so its hash is the dedup key.
func NormalizeURL(raw string) (Normalized, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 2048 {
		return Normalized{}, ErrInvalidURL
	}
	if strings.HasPrefix(strings.ToLower(s), "magnet:") {
		return Normalized{}, ErrUnsupported
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return Normalized{}, ErrInvalidURL
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return Normalized{}, ErrInvalidURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if net.ParseIP(host) != nil {
		return Normalized{}, ErrUnsupported
	}
	host = strings.TrimPrefix(host, "www.")
	q := u.Query()
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")

	switch host {
	case "youtu.be":
		return youtube(segs[0], q)
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		switch {
		case segs[0] == "watch":
			return youtube(q.Get("v"), q)
		case len(segs) == 2 && (segs[0] == "shorts" || segs[0] == "live" || segs[0] == "embed" || segs[0] == "v"):
			return youtube(segs[1], q)
		case segs[0] == "playlist":
			return Normalized{}, ErrPlaylist
		}
		return Normalized{}, ErrInvalidURL
	case "vk.com", "m.vk.com", "vk.ru", "m.vk.ru", "vkvideo.ru", "m.vkvideo.ru":
		return vk(segs, q)
	case "rutube.ru", "m.rutube.ru":
		return rutube(segs, q)
	default:
		if blockedHost(host) {
			return Normalized{}, ErrUnsupported
		}
		if src, ok := publicSource(host); ok {
			return genericVideo(host, src, path, q)
		}
	}
	return Normalized{}, ErrUnsupported
}

func youtube(id string, q url.Values) (Normalized, error) {
	if !ytID.MatchString(id) {
		if id == "" && q.Get("list") != "" {
			return Normalized{}, ErrPlaylist
		}
		return Normalized{}, ErrInvalidURL
	}
	// list=/index= (watch inside a playlist) are dropped: one video only.
	return Normalized{URL: "https://www.youtube.com/watch?v=" + id, Source: SourceYouTube, ExternalID: id}, nil
}

func vk(segs []string, q url.Values) (Normalized, error) {
	cand := segs[len(segs)-1]
	if z := q.Get("z"); z != "" { // vk.com/wall…?z=video-1_2%2Fpl_…
		cand = strings.SplitN(z, "/", 2)[0]
	}
	m := vkVideo.FindStringSubmatch(cand)
	if m == nil {
		if slices.Contains(segs, "playlist") || strings.HasPrefix(cand, "playlist") || strings.HasPrefix(q.Get("section"), "playlist") {
			return Normalized{}, ErrPlaylist
		}
		return Normalized{}, ErrInvalidURL
	}
	return Normalized{URL: "https://vk.com/video" + m[1], Source: SourceVK, ExternalID: m[1]}, nil
}

func rutube(segs []string, q url.Values) (Normalized, error) {
	var id string
	switch {
	case len(segs) >= 2 && (segs[0] == "video" || segs[0] == "shorts") && segs[1] != "private":
		id = segs[1]
	case len(segs) >= 3 && segs[0] == "video" && segs[1] == "private":
		id = segs[2]
	case len(segs) >= 3 && segs[0] == "play" && segs[1] == "embed":
		id = segs[2]
	case len(segs) >= 1 && segs[0] == "plst":
		return Normalized{}, ErrPlaylist
	}
	id = strings.ToLower(id)
	if !rutubeID.MatchString(id) {
		return Normalized{}, ErrInvalidURL
	}
	out := "https://rutube.ru/video/" + id + "/"
	if p := q.Get("p"); p != "" { // access key of a private-by-link video
		out += "?p=" + url.QueryEscape(p)
	}
	return Normalized{URL: out, Source: SourceRuTube, ExternalID: id}, nil
}
