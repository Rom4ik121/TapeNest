// Package match holds the pure decision logic of acquisition: which release to
// grab (quality × seeders × size sanity × title match) and which torrent file is
// which album track (track number + title similarity).
package match

import (
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Quality labels.
const (
	QFLAC   = "FLAC"
	QMP3320 = "MP3-320"
	QMP3V0  = "MP3-V0"
	QMP3    = "MP3"
	QLossy  = "Lossy"
	QUnk    = "Unknown"
)

var (
	reFLAC  = regexp.MustCompile(`(?i)\b(flac|lossless|alac)\b`)
	re320   = regexp.MustCompile(`(?i)\b320\s*(k|kbps|kbit)?\b|\bcbr\s*320\b`)
	reV0    = regexp.MustCompile(`(?i)\b(v0|vbr[\s-]?v0|vbr)\b`)
	reMP3   = regexp.MustCompile(`(?i)\bmp3\b`)
	reLossy = regexp.MustCompile(`(?i)\b(aac|m4a|ogg|opus|vorbis|wma)\b`)
	rePack  = regexp.MustCompile(`(?i)\b(discography|дискография|anthology|complete\s+works|collection|mega\s*pack|box\s*set)\b`)
)

// Quality guesses the release quality from its title.
func Quality(title string) string {
	switch {
	case reFLAC.MatchString(title):
		return QFLAC
	case re320.MatchString(title):
		return QMP3320
	case reV0.MatchString(title):
		return QMP3V0
	case reMP3.MatchString(title):
		return QMP3
	case reLossy.MatchString(title):
		return QLossy
	}
	return QUnk
}

// qualityWeight: MP3 320 first (streams start fastest, fine on phones), FLAC close.
var qualityWeight = map[string]float64{QMP3320: 3.0, QFLAC: 2.8, QMP3V0: 2.5, QMP3: 1.6, QUnk: 1.2, QLossy: 1.0}

// Release is a candidate from the indexers.
type Release struct {
	Title   string
	Size    int64
	Seeders int
}

// Want is what we are looking for.
type Want struct {
	Artist     string
	Album      string
	TrackCount int
	MaxBytes   int64
}

// Scored is a release with its score (higher is better).
type Scored struct {
	Index   int
	Score   float64
	Quality string
}

// Rank filters and orders candidates; rejected ones are dropped.
func Rank(want Want, rs []Release) []Scored {
	var out []Scored
	albumTok := withoutCatalog(significant(Tokens(want.Album)))
	credits := artistCredits(want.Artist)
	for i, r := range rs {
		if r.Seeders < 1 || r.Size <= 0 || (want.MaxBytes > 0 && r.Size > want.MaxBytes) {
			continue
		}
		n := max(want.TrackCount, 1)
		perTrack := r.Size / int64(n)
		if perTrack < 700_000 { // < ~0.7 MB per track: truncated or 64 kbit/s junk
			continue
		}
		tt := Tokens(r.Title)
		if coverage(albumTok, tt) < 0.6 {
			continue
		}
		if len(credits) > 0 && !isVarious(want.Artist) && bestCoverage(credits, tt) < 0.5 {
			continue
		}
		if rePack.MatchString(r.Title) && !rePack.MatchString(want.Album) {
			continue
		}
		q := Quality(r.Title)
		gb := float64(r.Size) / (1 << 30)
		s := qualityWeight[q]*10 + 4*math.Log2(1+float64(r.Seeders)) - 2*gb
		out = append(out, Scored{Index: i, Score: s, Quality: q})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out
}

// catalogPrefixes precede catalogue numbers that release titles often omit
// ("Goldberg Variations, BWV 988", "Symphony No. 5, Op. 67").
var catalogPrefixes = map[string]bool{"bwv": true, "op": true, "opus": true, "kv": true, "k": true, "hob": true, "rv": true, "woo": true, "d": true, "no": true, "nr": true}

func withoutCatalog(ts []string) []string {
	out := ts[:0:0]
	for i := 0; i < len(ts); i++ {
		if catalogPrefixes[ts[i]] && i+1 < len(ts) && isNumber(ts[i+1]) {
			i++ // skip "bwv 988"
			continue
		}
		out = append(out, ts[i])
	}
	if len(out) == 0 {
		return ts
	}
	return out
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

var creditSplit = regexp.MustCompile(`(?i)\s*(?:;|,|&|/|\bfeat\.?|\bft\.?|\bwith\b|\band\b|\bx\b)\s*`)

// artistCredits splits a joined credit ("J. S. Bach; Kimiko Ishizaka") into
// per-artist token lists; the whole credit is kept as the first entry.
func artistCredits(a string) [][]string {
	var out [][]string
	if whole := significant(Tokens(a)); len(whole) > 0 {
		out = append(out, whole)
	}
	parts := creditSplit.Split(a, -1)
	if len(parts) < 2 {
		return out
	}
	for _, p := range parts {
		if t := significant(Tokens(p)); len(t) > 0 {
			out = append(out, t)
		}
	}
	return out
}

func bestCoverage(credits [][]string, have []string) float64 {
	best := 0.0
	for _, c := range credits {
		best = max(best, coverage(c, have))
	}
	return best
}

func isVarious(a string) bool {
	a = strings.ToLower(a)
	return a == "various artists" || a == "various"
}

var stop = map[string]bool{"the": true, "a": true, "an": true, "and": true, "of": true, "feat": true, "ft": true, "&": true}

func significant(ts []string) []string {
	out := ts[:0:0]
	for _, t := range ts {
		if !stop[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return ts
	}
	return out
}

func coverage(want, have []string) float64 {
	if len(want) == 0 {
		return 1
	}
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	hit := 0
	for _, w := range want {
		if set[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

var fold = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "ú", "u", "ù", "u",
	"û", "u", "ü", "u", "ñ", "n", "ç", "c", "ß", "ss", "ё", "е", "’", "", "'", "")

// Tokens lowercases, folds common accents and splits on non letters/digits.
func Tokens(s string) []string {
	s = fold.Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Similarity is the Dice coefficient of token sets (0..1).
func Similarity(a, b string) float64 {
	ta, tb := Tokens(a), Tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	sa := map[string]bool{}
	for _, t := range ta {
		sa[t] = true
	}
	sb := map[string]bool{}
	for _, t := range tb {
		sb[t] = true
	}
	hit := 0
	for t := range sa {
		if sb[t] {
			hit++
		}
	}
	return 2 * float64(hit) / float64(len(sa)+len(sb))
}

// ── files ────────────────────────────────────────────────────────────────────

var audioExts = map[string]int{".mp3": 5, ".flac": 4, ".m4a": 3, ".ogg": 2, ".opus": 2, ".aac": 1, ".wav": 0}

// AudioExt returns the lowercase extension of an audio file ("" if not audio).
func AudioExt(name string) string {
	e := strings.ToLower(path.Ext(name))
	if _, ok := audioExts[e]; ok {
		return e
	}
	return ""
}

// ContentType of an audio extension.
func ContentType(name string) string {
	switch AudioExt(name) {
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	}
	return "application/octet-stream"
}

// Track is an album track to locate.
type Track struct {
	Key      string
	Disc     int
	Position int
	Title    string
}

// File is a torrent file.
type File struct {
	Index int
	Name  string
	Size  int64
}

var (
	reNum  = regexp.MustCompile(`(?:^|[\s._\-\[(])(\d{1,3})(?:[\s._\-\])]|$)`)
	reDisc = regexp.MustCompile(`(?i)(?:cd|disc|disk|диск)[\s._-]*(\d{1,2})`)
	reDxT  = regexp.MustCompile(`^(\d)[-.](\d{2})\b`)
)

// fileNumbers extracts (disc, track) from a torrent path.
func fileNumbers(name string) (disc, track int) {
	disc = 1
	dir, base := path.Split(name)
	if m := reDisc.FindStringSubmatch(dir); m != nil {
		disc, _ = strconv.Atoi(m[1])
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	if m := reDxT.FindStringSubmatch(stem); m != nil {
		disc, _ = strconv.Atoi(m[1])
		track, _ = strconv.Atoi(m[2])
		return disc, track
	}
	if m := reNum.FindStringSubmatch(stem); m != nil {
		track, _ = strconv.Atoi(m[1])
	}
	return disc, track
}

// Mapping is the result of MapFiles.
type Mapping struct {
	Ext     string         // chosen audio format
	ByTrack map[string]int // track key → file index
	Skip    []int          // file indexes not worth downloading (other formats, junk)
	Extras  []int          // small artwork / cue / log files kept at normal priority
}

// MapFiles assigns torrent files to album tracks. Among several audio formats
// (FLAC + MP3 in one torrent) only preferExt (or the most complete one) is kept.
func MapFiles(tracks []Track, files []File, preferExt string) Mapping {
	groups := map[string][]File{}
	for _, f := range files {
		if e := AudioExt(f.Name); e != "" {
			groups[e] = append(groups[e], f)
		}
	}
	largest := ""
	for e, fs := range groups {
		if largest == "" || len(fs) > len(groups[largest]) || (len(fs) == len(groups[largest]) && audioExts[e] > audioExts[largest]) {
			largest = e
		}
	}
	best := largest
	if fs, ok := groups[preferExt]; ok && len(fs)*10 >= len(groups[largest])*8 {
		best = preferExt
	}
	m := Mapping{Ext: best, ByTrack: map[string]int{}}
	type pair struct {
		t, f  int
		score float64
	}
	cand := groups[best]
	var pairs []pair
	for ti, t := range tracks {
		for fi, f := range cand {
			disc, num := fileNumbers(f.Name)
			s := 0.6 * Similarity(t.Title, path.Base(f.Name))
			if num == t.Position && (disc == t.Disc || len(tracks) == len(cand)) {
				s += 0.5
			}
			if s >= 0.35 {
				pairs = append(pairs, pair{ti, fi, s})
			}
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].score > pairs[b].score })
	usedT, usedF := map[int]bool{}, map[int]bool{}
	for _, p := range pairs {
		if usedT[p.t] || usedF[p.f] {
			continue
		}
		usedT[p.t], usedF[p.f] = true, true
		m.ByTrack[tracks[p.t].Key] = cand[p.f].Index
	}
	mapped := map[int]bool{}
	for _, idx := range m.ByTrack {
		mapped[idx] = true
	}
	for _, f := range files {
		if mapped[f.Index] {
			continue
		}
		ext := strings.ToLower(path.Ext(f.Name))
		switch {
		case AudioExt(f.Name) == best:
			m.Extras = append(m.Extras, f.Index) // bonus/hidden track of the chosen format
		case (ext == ".jpg" || ext == ".jpeg" || ext == ".png") && f.Size < 5<<20 && isArtwork(f.Name):
			m.Extras = append(m.Extras, f.Index)
		case ext == ".cue" || ext == ".log":
			m.Extras = append(m.Extras, f.Index)
		default:
			m.Skip = append(m.Skip, f.Index)
		}
	}
	return m
}

func isArtwork(name string) bool {
	b := strings.ToLower(path.Base(name))
	for _, k := range []string{"cover", "folder", "front", "album"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}
