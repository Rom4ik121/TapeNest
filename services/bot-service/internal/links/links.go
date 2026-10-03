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

// Supported sources. YouTube, VK and RuTube are the original set; the rest are
// public video hosts yt-dlp already extracts. Torrent indexes and cinema
// catalogs are not sources — a download starts only from a pasted link.
const (
	YouTube     Source = "YouTube"
	VK          Source = "VK"
	RuTube      Source = "RuTube"
	TikTok      Source = "TikTok"
	Vimeo       Source = "Vimeo"
	Dailymotion Source = "Dailymotion"
	Instagram   Source = "Instagram"
	X           Source = "X"
	Twitch      Source = "Twitch"
	Facebook    Source = "Facebook"
	OK          Source = "OK"
	Coub        Source = "Coub"
	Reddit      Source = "Reddit"
	Streamable  Source = "Streamable"
	Rumble      Source = "Rumble"
	Kick        Source = "Kick"
	Bilibili    Source = "Bilibili"
	Mailru      Source = "Mail.ru"
	Niconico    Source = "Niconico"
	Telegram    Source = "Telegram"
)

// Sources is the ordered list shown to users.
var Sources = []Source{
	YouTube, VK, RuTube, TikTok, Vimeo, Dailymotion, Instagram, X, Twitch, Facebook,
	OK, Coub, Reddit, Streamable, Rumble, Kick, Bilibili, Mailru, Niconico, Telegram,
}

var hostSources = map[string]Source{
	"youtube.com": YouTube, "youtu.be": YouTube, "youtube-nocookie.com": YouTube,
	"vk.com": VK, "vk.ru": VK, "vkvideo.ru": VK,
	"rutube.ru":       RuTube,
	"tiktok.com":      TikTok,
	"vimeo.com":       Vimeo,
	"dailymotion.com": Dailymotion, "dai.ly": Dailymotion,
	"instagram.com": Instagram,
	"twitter.com":   X, "x.com": X,
	"twitch.tv":    Twitch,
	"facebook.com": Facebook, "fb.watch": Facebook, "fb.com": Facebook,
	"ok.ru": OK, "odnoklassniki.ru": OK,
	"coub.com":   Coub,
	"reddit.com": Reddit, "redd.it": Reddit,
	"streamable.com": Streamable,
	"rumble.com":     Rumble,
	"kick.com":       Kick,
	"bilibili.com":   Bilibili, "b23.tv": Bilibili,
	"mail.ru":      Mailru,
	"nicovideo.jp": Niconico, "nico.ms": Niconico,
	"t.me": Telegram, "telegram.me": Telegram,
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
