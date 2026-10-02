package ytm

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Item is one row of a YouTube Music shelf (song, album, or artist).
type Item struct {
	VideoID     string
	BrowseID    string
	Title       string
	Artist      string
	Album       string
	Year        int
	DurationSec int
	Thumb       string
}

// Track is a song returned by YouTube Music.
type Track struct {
	VideoID     string
	Title       string
	Artist      string
	Album       string
	DurationSec int
	Thumb       string
}

// Album is a YouTube Music album (browse id MPRE…).
type Album struct {
	BrowseID string
	Title    string
	Artist   string
	Year     int
	Thumb    string
}

// Artist is a YouTube Music artist (browse id UC…).
type Artist struct {
	BrowseID string
	Name     string
	Thumb    string
}

// Catalog is one search: songs, albums and artists.
type Catalog struct {
	Tracks  []Track
	Albums  []Album
	Artists []Artist
}

var (
	reDur  = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	reYear = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

// ParseItems reads musicResponsiveListItemRenderer rows from an innertube body.
func ParseItems(body []byte) ([]Item, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	var items []Item
	walk(root, func(m map[string]any) {
		raw, ok := m["musicResponsiveListItemRenderer"].(map[string]any)
		if !ok {
			return
		}
		if it, ok := parseItem(raw); ok {
			items = append(items, it)
		}
	})
	return items, nil
}

func parseItem(raw map[string]any) (Item, bool) {
	var cols [][]string
	var titleVideo, titleBrowse string
	for i, col := range asList(raw["flexColumns"]) {
		cm := asMap(asMap(col)["musicResponsiveListItemFlexColumnRenderer"])
		runs := asList(asMap(cm["text"])["runs"])
		var line []string
		for _, r := range runs {
			rm := asMap(r)
			if t, ok := rm["text"].(string); ok {
				if c := clean(t); c != "" {
					line = append(line, c)
				}
			}
			if i == 0 {
				if titleVideo == "" {
					titleVideo = findVideo(rm)
				}
				if titleBrowse == "" {
					titleBrowse = findBrowse(rm)
				}
			}
		}
		if len(line) > 0 {
			cols = append(cols, line)
		}
	}
	if titleVideo == "" {
		titleVideo = findVideo(raw)
	}
	if titleBrowse == "" {
		titleBrowse = findBrowse(raw)
	}
	title := ""
	if len(cols) > 0 {
		title = strings.Join(cols[0], " ")
	}
	artist, album := meta(cols)
	if title == "" || (titleVideo == "" && titleBrowse == "") {
		return Item{}, false
	}
	it := Item{
		VideoID: titleVideo, BrowseID: titleBrowse, Title: title, Artist: artist, Album: album,
		DurationSec: duration(raw), Thumb: thumb(raw),
	}
	if y := reYear.FindString(artist + " " + album); y != "" {
		it.Year, _ = strconv.Atoi(y)
	}
	return it, true
}

func meta(cols [][]string) (artist, album string) {
	if len(cols) < 2 {
		return "", ""
	}
	var parts []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s == "" {
			return
		}
		switch strings.ToLower(s) {
		case "song", "album", "artist", "video", "single", "ep":
			return
		}
		parts = append(parts, s)
	}
	for _, col := range cols[1:] {
		for _, t := range col {
			t = strings.TrimSpace(t)
			if t == "" || t == "•" || t == "·" {
				flush()
				continue
			}
			if reDur.MatchString(t) {
				continue
			}
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			cur.WriteString(t)
		}
		flush()
	}
	if len(parts) > 0 {
		artist = parts[0]
	}
	if len(parts) > 1 {
		album = parts[1]
	}
	return artist, album
}

func duration(raw map[string]any) int {
	best := 0
	walk(raw, func(m map[string]any) {
		t, ok := m["text"].(string)
		if !ok {
			return
		}
		g := reDur.FindStringSubmatch(strings.TrimSpace(clean(t)))
		if g == nil {
			return
		}
		a, _ := strconv.Atoi(g[1])
		b, _ := strconv.Atoi(g[2])
		sec := a*60 + b
		if g[3] != "" {
			c, _ := strconv.Atoi(g[3])
			sec = a*3600 + b*60 + c
		}
		if sec > best {
			best = sec
		}
	})
	return best
}

func thumb(raw map[string]any) string {
	bestW := 0
	best := ""
	walk(raw, func(m map[string]any) {
		u, ok := m["url"].(string)
		if !ok || !strings.HasPrefix(u, "https://") {
			return
		}
		w := 0
		switch n := m["width"].(type) {
		case float64:
			w = int(n)
		case json.Number:
			i, _ := n.Int64()
			w = int(i)
		}
		if w >= bestW {
			bestW = w
			best = u
		}
	})
	return best
}

func findVideo(v any) string {
	found := ""
	walk(v, func(m map[string]any) {
		if found != "" {
			return
		}
		if id, ok := m["videoId"].(string); ok && ValidVideoID(id) {
			found = id
		}
	})
	return found
}

func findBrowse(v any) string {
	found := ""
	walk(v, func(m map[string]any) {
		if found != "" {
			return
		}
		if id, ok := m["browseId"].(string); ok && ValidBrowseID(id) {
			found = id
		}
	})
	return found
}

func walk(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		fn(t)
		for _, c := range t {
			walk(c, fn)
		}
	case []any:
		for _, c := range t {
			walk(c, fn)
		}
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' {
			return -1
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}
