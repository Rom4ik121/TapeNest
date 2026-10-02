package domain

import (
	"sort"
	"strings"
)

// Format is one entry of yt-dlp's `formats` list (subset of fields we use).
type Format struct {
	ID             string  `json:"format_id"`
	Ext            string  `json:"ext"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Height         int     `json:"height"`
	Width          int     `json:"width"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesize_approx"`
	TBR            float64 `json:"tbr"`
	Protocol       string  `json:"protocol"`
	HasDRM         bool    `json:"has_drm"`
}

// Info is yt-dlp metadata (`--dump-single-json`, first pass).
type Info struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Duration   float64  `json:"duration"`
	Extractor  string   `json:"extractor_key"`
	IsLive     bool     `json:"is_live"`
	LiveStatus string   `json:"live_status"`
	Thumbnail  string   `json:"thumbnail"`
	HasDRM     bool     `json:"_has_drm"`
	Formats    []Format `json:"formats"`
}

// Live reports live streams and not-yet-started premieres.
func (i Info) Live() bool {
	return i.IsLive || i.LiveStatus == "is_live" || i.LiveStatus == "is_upcoming"
}

// DRM reports DRM-protected media (never downloaded, spec §14).
func (i Info) DRM() bool {
	if i.HasDRM {
		return true
	}
	if len(i.Formats) == 0 {
		return false
	}
	for _, f := range i.Formats {
		if !f.HasDRM {
			return false
		}
	}
	return true
}

// Limits steer format choice.
type Limits struct {
	MaxHeight   int   // e.g. 720
	MinTGHeight int   // don't go below this just to fit Telegram, e.g. 360
	TGLimit     int64 // Telegram bot upload limit (50 MB)
}

// Choice is the selected format for the second pass.
type Choice struct {
	Spec    string // yt-dlp -f value: "137+140" or "18"
	Single  bool   // one progressive file → can be piped stdout → S3
	Height  int
	EstSize int64 // 0 = unknown
	Ext     string
}

func hasVideo(f Format) bool { return f.VCodec != "" && f.VCodec != "none" }
func hasAudio(f Format) bool { return f.ACodec != "" && f.ACodec != "none" }

func (f Format) size(dur float64) int64 {
	switch {
	case f.Filesize > 0:
		return f.Filesize
	case f.FilesizeApprox > 0:
		return f.FilesizeApprox
	case f.TBR > 0 && dur > 0:
		return int64(f.TBR * 1000 / 8 * dur)
	}
	return 0
}

// codecScore prefers H.264/AAC in MP4 (plays inline in every Telegram client),
// then other MP4 codecs, then the rest.
func codecScore(f Format) int {
	v := strings.ToLower(f.VCodec)
	switch {
	case strings.HasPrefix(v, "avc1") || strings.HasPrefix(v, "h264"):
		return 3
	case f.Ext == "mp4" && (strings.HasPrefix(v, "hvc1") || strings.HasPrefix(v, "hev1")):
		return 2
	case f.Ext == "mp4":
		return 1
	}
	return 0
}

// SelectFormat picks what to download (single profile for bot and mini app, so
// the dedup key is just the canonical URL):
//  1. the best height ≤ MaxHeight whose estimated size fits the Telegram limit,
//     if that height is ≥ MinTGHeight — the bot can send the file directly;
//  2. otherwise the best height ≤ MaxHeight (delivered as a link).
//
// H.264/AAC is preferred; a progressive (audio+video) format beats a merge at the
// same height/codec because it can be streamed straight into S3.
func SelectFormat(info Info, lim Limits) (Choice, bool) {
	dur := info.Duration
	var audios []Format
	for _, f := range info.Formats {
		if f.HasDRM {
			continue
		}
		if hasAudio(f) && !hasVideo(f) {
			audios = append(audios, f)
		}
	}
	// container-matching audio first (m4a for mp4), non-DRC variants, then bitrate
	pref := func(a Format, forExt string) int {
		p := 0
		if (forExt == "mp4" && a.Ext == "m4a") || (forExt == "webm" && a.Ext == "webm") {
			p += 2
		}
		if !strings.Contains(a.ID, "drc") {
			p++
		}
		return p
	}
	bestAudio := func(forExt string) (Format, bool) {
		var best Format
		found := false
		for _, a := range audios {
			pa, pb := pref(a, forExt), pref(best, forExt)
			if !found || pa > pb || (pa == pb && a.TBR > best.TBR) {
				best, found = a, true
			}
		}
		return best, found
	}
	var cands []Choice
	scores := map[string]int{}
	for _, f := range info.Formats {
		if f.HasDRM || !hasVideo(f) || f.Height <= 0 || f.Height > lim.MaxHeight || strings.HasPrefix(f.Protocol, "mhtml") {
			continue
		}
		if hasAudio(f) {
			c := Choice{Spec: f.ID, Single: !strings.Contains(f.Protocol, "m3u8") && !strings.Contains(f.Protocol, "dash"), Height: f.Height, EstSize: f.size(dur), Ext: f.Ext}
			cands = append(cands, c)
			scores[c.Spec] = codecScore(f)*2 + 1
			continue
		}
		a, ok := bestAudio(f.Ext)
		if !ok {
			continue
		}
		est := f.size(dur)
		if as := a.size(dur); est > 0 && as > 0 {
			est += as
		} else {
			est = 0
		}
		c := Choice{Spec: f.ID + "+" + a.ID, Height: f.Height, EstSize: est, Ext: "mp4"}
		cands = append(cands, c)
		scores[c.Spec] = codecScore(f) * 2
	}
	if len(cands) == 0 {
		return Choice{}, false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Height != b.Height {
			return a.Height > b.Height
		}
		if scores[a.Spec] != scores[b.Spec] {
			return scores[a.Spec] > scores[b.Spec]
		}
		return a.EstSize != 0 && (b.EstSize == 0 || a.EstSize < b.EstSize)
	})
	if lim.TGLimit > 0 {
		for _, c := range cands {
			if c.Height < lim.MinTGHeight {
				break
			}
			if c.EstSize > 0 && c.EstSize <= lim.TGLimit {
				return c, true
			}
		}
	}
	return cands[0], true
}
