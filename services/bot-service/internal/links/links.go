// Package links finds video links in messages and classifies supported sources.
// Normalisation/dedup is download-service's job (stage 2); the bot only detects.
package links

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

// Source is a supported video platform.
type Source string

// Supported sources (spec §5.3 lists per-domain limits for these).
const (
	YouTube Source = "YouTube"
	VK      Source = "VK"
	RuTube  Source = "RuTube"
)

// Sources is the ordered list shown to users.
var Sources = []Source{YouTube, VK, RuTube}

var hostSources = map[string]Source{
	"youtube.com": YouTube, "youtu.be": YouTube, "youtube-nocookie.com": YouTube,
	"vk.com": VK, "vk.ru": VK, "vkvideo.ru": VK,
	"rutube.ru": RuTube,
}

// Link is a detected URL.
type Link struct {
	URL    string
	Source Source // empty when unsupported
}

// Classify returns the source of rawURL, or "" if it is not a supported http(s) link.
func Classify(rawURL string) Source {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for {
		if s, ok := hostSources[host]; ok {
			return s
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			return ""
		}
		host = host[i+1:] // m.youtube.com → youtube.com, music.youtube.com → youtube.com
	}
}

var bareURL = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)

// Find extracts URLs from text using Telegram entities (url/text_link; offsets
// in UTF-16 units) and falls back to a regex when there are no entities.
func Find(text string, entities []telegram.MessageEntity) []Link {
	var raw []string
	if len(entities) > 0 {
		u16 := utf16.Encode([]rune(text))
		for _, e := range entities {
			switch e.Type {
			case "url":
				if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > len(u16) {
					continue
				}
				raw = append(raw, string(utf16.Decode(u16[e.Offset:e.Offset+e.Length])))
			case "text_link":
				raw = append(raw, e.URL)
			}
		}
	} else {
		raw = bareURL.FindAllString(text, -1)
	}
	seen := map[string]bool{}
	out := make([]Link, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimRight(r, ".,;:!?)»")
		if !strings.Contains(r, "://") {
			r = "https://" + r // Telegram marks "youtu.be/x" as url without a scheme
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, Link{URL: r, Source: Classify(r)})
	}
	return out
}

// SourceNames joins supported sources for messages.
func SourceNames() string {
	names := make([]string, len(Sources))
	for i, s := range Sources {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}
