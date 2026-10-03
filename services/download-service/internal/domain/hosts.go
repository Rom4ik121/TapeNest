package domain

import (
	"net/url"
	"strings"
)

// More public video hosts yt-dlp already extracts. A pasted link is the only
// way in: no search, no torrent index, no cinema catalog.
const (
	SourceTikTok      Source = "tiktok"
	SourceVimeo       Source = "vimeo"
	SourceDailymotion Source = "dailymotion"
	SourceInstagram   Source = "instagram"
	SourceTwitter     Source = "twitter"
	SourceTwitch      Source = "twitch"
	SourceFacebook    Source = "facebook"
	SourceOK          Source = "ok"
	SourceCoub        Source = "coub"
	SourceReddit      Source = "reddit"
	SourceStreamable  Source = "streamable"
	SourceRumble      Source = "rumble"
	SourceKick        Source = "kick"
	SourceBilibili    Source = "bilibili"
	SourceMailru      Source = "mailru"
	SourceNiconico    Source = "niconico"
	SourceTelegram    Source = "telegram"
)

// publicHosts maps a registrable host (and short-link hosts) to a source.
// Subdomains match by walking labels (vm.tiktok.com → tiktok.com).
var publicHosts = map[string]Source{
	"tiktok.com":       SourceTikTok,
	"vimeo.com":        SourceVimeo,
	"dailymotion.com":  SourceDailymotion,
	"dai.ly":           SourceDailymotion,
	"instagram.com":    SourceInstagram,
	"twitter.com":      SourceTwitter,
	"x.com":            SourceTwitter,
	"twitch.tv":        SourceTwitch,
	"facebook.com":     SourceFacebook,
	"fb.watch":         SourceFacebook,
	"fb.com":           SourceFacebook,
	"ok.ru":            SourceOK,
	"odnoklassniki.ru": SourceOK,
	"coub.com":         SourceCoub,
	"reddit.com":       SourceReddit,
	"redd.it":          SourceReddit,
	"streamable.com":   SourceStreamable,
	"rumble.com":       SourceRumble,
	"kick.com":         SourceKick,
	"bilibili.com":     SourceBilibili,
	"b23.tv":           SourceBilibili,
	"mail.ru":          SourceMailru,
	"nicovideo.jp":     SourceNiconico,
	"nico.ms":          SourceNiconico,
	"t.me":             SourceTelegram,
	"telegram.me":      SourceTelegram,
}

// blockedHosts are torrent indexes and cinema catalogs. They are never a
// download source, even when yt-dlp happens to know the site.
var blockedHosts = map[string]struct{}{
	"rutracker.org": {}, "rutracker.net": {},
	"rutor.info": {}, "rutor.is": {},
	"thepiratebay.org": {},
	"1337x.to":         {}, "1337x.st": {}, "1337x.is": {},
	"nyaa.si":      {},
	"rarbg.to":     {},
	"yts.mx":       {},
	"kinopoisk.ru": {},
	"ivi.ru":       {},
	"okko.tv":      {},
	"netflix.com":  {},
	"megogo.net":   {},
	"rezka.ag":     {}, "hdrezka.ag": {},
}

// HostSummary is the short list shown when a link is rejected.
func HostSummary() string {
	return "YouTube, VK, RuTube, TikTok, Vimeo, Dailymotion, Instagram, X, Twitch, Facebook, OK and other public video hosts"
}

func publicSource(host string) (Source, bool) {
	for {
		if s, ok := publicHosts[host]; ok {
			return s, true
		}
		next, ok := parentHost(host)
		if !ok {
			return "", false
		}
		host = next
	}
}

func blockedHost(host string) bool {
	for {
		if _, ok := blockedHosts[host]; ok {
			return true
		}
		next, ok := parentHost(host)
		if !ok {
			return false
		}
		host = next
	}
}

// parentHost drops the leftmost label when a registrable host remains.
func parentHost(host string) (string, bool) {
	i := strings.IndexByte(host, '.')
	if i < 0 || !strings.Contains(host[i+1:], ".") {
		return "", false
	}
	return host[i+1:], true
}

// dropParam are tracking keys removed from generic video URLs (utm_* too).
var dropParam = map[string]struct{}{
	"si": {}, "feature": {}, "fbclid": {}, "gclid": {}, "yclid": {},
	"igshid": {}, "igsh": {}, "share_id": {}, "ref_src": {},
	"is_from_webapp": {}, "sender_device": {}, "sender_web_id": {},
}

func genericVideo(host string, src Source, path string, q url.Values) (Normalized, error) {
	if path == "" || path == "/" {
		return Normalized{}, ErrInvalidURL
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	switch segs[0] {
	case "playlist", "playlists":
		return Normalized{}, ErrPlaylist
	case "search", "results", "find":
		return Normalized{}, ErrInvalidURL
	}
	id := segs[len(segs)-1]
	if id == "" {
		return Normalized{}, ErrInvalidURL
	}
	if len(id) > 200 {
		id = id[:200]
	}
	out := "https://" + host + path
	if query := canonicalQuery(q); query != "" {
		out += "?" + query
	}
	return Normalized{URL: out, Source: src, ExternalID: id}, nil
}

func canonicalQuery(q url.Values) string {
	next := url.Values{}
	for key, values := range q {
		low := strings.ToLower(key)
		if strings.HasPrefix(low, "utm_") {
			continue
		}
		if _, drop := dropParam[low]; drop {
			continue
		}
		for _, v := range values {
			next.Add(key, v)
		}
	}
	if len(next) == 0 {
		return ""
	}
	return next.Encode()
}
