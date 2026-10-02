package ytdlp

import (
	"strconv"
	"strings"
)

// Progress of pass 2, aggregated over all files of a merge (video + audio).
type Progress struct {
	DownloadedBytes int64
	TotalBytes      int64 // 0 when unknown
	SpeedBps        float64
	EtaSec          int
}

// tracker parses TNPROG lines. yt-dlp restarts the counters for each file of a
// merge, so completed files are accumulated into base.
type tracker struct {
	cb        func(Progress)
	base      int64
	lastDown  int64
	lastTotal int64
}

func newTracker(cb func(Progress)) *tracker { return &tracker{cb: cb} }

// line handles one output line; false when it is not a progress line.
func (t *tracker) line(s string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), "TNPROG ")
	if !ok {
		return false
	}
	f := strings.Fields(rest)
	if len(f) < 5 {
		return true
	}
	down := num(f[0])
	total := num(f[1])
	if total <= 0 {
		total = num(f[2])
	}
	if down < t.lastDown { // next file of a merge
		t.base += max(t.lastTotal, t.lastDown)
	}
	t.lastDown, t.lastTotal = down, total
	speed, _ := strconv.ParseFloat(f[3], 64)
	eta, _ := strconv.ParseFloat(f[4], 64)
	p := Progress{DownloadedBytes: t.base + down, SpeedBps: speed, EtaSec: int(eta)}
	if total > 0 {
		p.TotalBytes = t.base + total
	}
	if t.cb != nil {
		t.cb(p)
	}
	return true
}

func num(s string) int64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(v)
}
