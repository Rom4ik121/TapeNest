// Package rank builds a My Wave batch: it scores every eligible track from all
// candidate sources (long-term taste profile, item-item CF, ALS "listeners like
// you", audio-content similarity, popularity, freshness, session feedback),
// blends them with tunable weights and per-user Thompson-sampled source
// multipliers, applies penalties and the wave mode, then selects the batch with
// MMR diversity, artist constraints and an ε-exploration slot. Pure function of
// (model, user, session, weights, rng, now) — used by the API and cmd/reco-eval.
package rank

import (
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
)

// Sources (response field "source"; Thompson arms for the learnable ones).
const (
	SrcProfile = "profile"
	SrcCF      = "cf"
	SrcALS     = "als"
	SrcContent = "content"
	SrcSession = "session"
	SrcPopular = "popular"
	SrcFresh   = "fresh"
	SrcExplore = "explore"
)

// LearnableSources are the Thompson-sampling arms.
var LearnableSources = []string{SrcProfile, SrcCF, SrcALS, SrcContent}

// Modes of the wave.
const (
	ModeDefault   = "default"
	ModeCalm      = "calm"
	ModeEnergetic = "energetic"
	ModeDiscover  = "discover"
	ModeFavorites = "favorites"
)

// ValidMode reports whether m is a known mode ("" means default).
func ValidMode(m string) bool {
	switch m {
	case "", ModeDefault, ModeCalm, ModeEnergetic, ModeDiscover, ModeFavorites:
		return true
	}
	return false
}

// Reason kinds (explainability).
const (
	ReasonBecauseLiked  = "because_you_liked"
	ReasonArtist        = "artist_you_like"
	ReasonGenre         = "genre_you_like"
	ReasonTag           = "mood_you_like"
	ReasonListeners     = "similar_listeners"
	ReasonPopular       = "popular"
	ReasonFresh         = "new_in_catalog"
	ReasonDiscovery     = "discovery"
	ReasonFavorite      = "favorite"
	ReasonSessionArtist = "session_artist"
)

// Weights are the blend and selection knobs (env RECO_WEIGHTS JSON overrides).
type Weights struct {
	Profile      float64 `json:"profile"`
	CF           float64 `json:"cf"`
	ALS          float64 `json:"als"`
	Content      float64 `json:"content"`
	Popular      float64 `json:"popular"`
	Fresh        float64 `json:"fresh"`
	Session      float64 `json:"session"`
	Novelty      float64 `json:"novelty"`
	Mode         float64 `json:"mode"`
	Epsilon      float64 `json:"epsilon"`
	MMRLambda    float64 `json:"mmrLambda"`
	MaxPerArtist int     `json:"maxPerArtist"`
	Jitter       float64 `json:"jitter"`
	FreshDays    float64 `json:"freshDays"`
	Thompson     bool    `json:"thompson"`
	PoolSize     int     `json:"poolSize"`
	ColdContent  float64 `json:"coldContent"`
}

// DefaultWeights were picked with cmd/reco-eval (docs/reco/evaluation.md).
func DefaultWeights() Weights {
	return Weights{
		Profile: 1.0, CF: 1.0, ALS: 0.8, Content: 0.6, Popular: 0.4, Fresh: 0.3, Session: 1.3,
		Novelty: 0.15, Mode: 0.8, Epsilon: 0.07, MMRLambda: 0.7, MaxPerArtist: 2, Jitter: 0.03,
		FreshDays: 14, Thompson: true, PoolSize: 80, ColdContent: 0.5,
	}
}

// UserTrack is the decayed per-track state of a user.
type UserTrack struct {
	Affinity   float64
	Plays      int
	Completes  int
	EarlySkips int
	Skips      int
	Liked      bool
	LastPlayed time.Time
}

// Beta is a Thompson arm (successes/failures on top of the prior).
type Beta struct{ Alpha, Beta float64 }

// User is the long-term profile (already decayed to "now").
type User struct {
	Taste   map[string]float64 // "artist:<id>", "album:<id>", "genre:<g>", "tag:<t>"
	Tracks  map[int]UserTrack
	Factors []float32
	Sources map[string]Beta
}

// Feedback is one session signal.
type Feedback struct {
	Track  int
	Action string // like | skip
}

// Session is the short-term context sent by music-service.
type Session struct {
	Mode     string
	Exclude  map[int]bool
	Recent   []int // played/served order, oldest → newest
	Feedback []Feedback
}

// Reason explains a pick; Ref is a track index or -1.
type Reason struct {
	Kind   string
	Ref    int
	Artist string
	Genre  string
	Tag    string
}

// Pick is one ranked track.
type Pick struct {
	Track  int
	Score  float64
	Source string
	Reason Reason
}

type seed struct {
	idx     int
	w       float64
	session bool
}

type cand struct {
	idx     int
	score   float64
	comp    map[string]float64
	source  string
	unheard bool
}

// TasteKey formats a taste map key.
func TasteKey(kind, key string) string { return kind + ":" + key }

// Rank returns up to limit picks.
func Rank(m *model.Model, u *User, s *Session, w Weights, limit int, rnd *rand.Rand, now time.Time) []Pick {
	if m == nil || len(m.Tracks) == 0 || limit <= 0 {
		return nil
	}
	if u == nil {
		u = &User{}
	}
	if s == nil {
		s = &Session{}
	}
	w = applyMode(w, s.Mode)
	seeds := buildSeeds(u, s)
	mult := thompson(u, w, rnd)

	n := len(m.Tracks)
	profileRaw := make([]float64, n)
	cf := make([]float64, n)
	content := make([]float64, n)
	als := make([]float64, n)
	sess := make([]float64, n)

	// long-term + session content centroids
	var cent []float32
	for _, sd := range seeds {
		if e := m.Tracks[sd.idx].Emb; e != nil {
			if cent == nil {
				cent = make([]float32, len(e))
			}
			for d := range e {
				cent[d] += float32(sd.w) * e[d]
			}
		}
	}
	if cent != nil {
		cent = model.Normalize(cent)
	}
	for _, sd := range seeds {
		for _, nb := range m.CF[sd.idx] {
			cf[nb.Idx] += sd.w * float64(nb.Sim)
		}
	}
	for i := range m.Tracks {
		t := &m.Tracks[i]
		if len(u.Taste) > 0 {
			profileRaw[i] = tasteScore(u.Taste, t)
		}
		if cent != nil && t.Emb != nil {
			content[i] = math.Max(0, model.Dot(cent, t.Emb))
		}
		if u.Factors != nil && m.ItemFactors[i] != nil {
			als[i] = model.Dot(u.Factors, m.ItemFactors[i])
		}
	}
	for k, fb := range s.Feedback {
		ft := &m.Tracks[fb.Track]
		recency := math.Pow(0.85, float64(len(s.Feedback)-1-k))
		sign := 1.0
		if fb.Action == "skip" {
			sign = -0.8
		}
		for i := range m.Tracks {
			t := &m.Tracks[i]
			var v float64
			if t.ArtistID == ft.ArtistID {
				v += 1
			}
			if t.Genre != "" && t.Genre == ft.Genre {
				v += 0.4
			}
			if ft.Emb != nil && t.Emb != nil {
				v += 0.8 * math.Max(0, model.Dot(ft.Emb, t.Emb))
			}
			sess[i] += sign * recency * v
		}
	}
	normMax(profileRaw)
	normMax(cf)
	normMax(als)
	normMax(content)
	for i := range sess {
		sess[i] = math.Max(-1.5, math.Min(1.5, sess[i]/2))
	}

	cands := make([]cand, 0, n)
	for i := range m.Tracks {
		if s.Exclude[i] {
			continue
		}
		t := &m.Tracks[i]
		ut, heard := u.Tracks[i]
		unheard := !heard || (ut.Plays == 0 && !ut.Liked)
		comp := map[string]float64{
			SrcProfile: w.Profile * mult[SrcProfile] * profileRaw[i],
			SrcCF:      w.CF * mult[SrcCF] * cf[i],
			SrcALS:     w.ALS * mult[SrcALS] * als[i],
			SrcContent: w.Content * mult[SrcContent] * content[i],
			SrcPopular: w.Popular * t.Popularity,
			SrcSession: w.Session * sess[i],
		}
		if len(m.CF[i]) == 0 && m.ItemFactors[i] == nil {
			// cold item (no collaborative data yet): let audio similarity stand in
			// for the missing CF/ALS evidence, so new tracks can compete (ADR 0010 §5)
			comp[SrcContent] += w.ColdContent * (w.CF + w.ALS) * content[i]
		}
		if w.FreshDays > 0 && !t.CreatedAt.IsZero() {
			if age := now.Sub(t.CreatedAt).Hours() / 24; age >= 0 && age < w.FreshDays {
				comp[SrcFresh] = w.Fresh * (1 - age/w.FreshDays)
			}
		}
		score := 0.0
		for _, v := range comp {
			score += v
		}
		score += penalties(ut, heard, now)
		score += modeBonus(s.Mode, t, ut, heard, unheard, w)
		if unheard {
			score += w.Novelty * (1 - t.Popularity)
		}
		if w.Jitter > 0 && rnd != nil {
			score += w.Jitter * rnd.Float64()
		}
		cands = append(cands, cand{idx: i, score: score, comp: comp, source: dominant(comp, t), unheard: unheard})
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].score > cands[b].score })
	picks := selectBatch(m, s, cands, w, limit, rnd)
	refUses := map[int]int{}
	for k := range picks {
		picks[k].Reason = explain(m, u, seeds, picks[k], refUses)
		if picks[k].Reason.Ref >= 0 {
			refUses[picks[k].Reason.Ref]++
		}
	}
	return picks
}

func applyMode(w Weights, mode string) Weights {
	switch mode {
	case ModeDiscover:
		w.Popular = 0
		w.Epsilon = math.Max(w.Epsilon, 0.3)
		w.Novelty = math.Max(w.Novelty, 0.6)
	case ModeFavorites:
		w.Epsilon = 0
		w.Novelty = 0
	}
	return w
}

func buildSeeds(u *User, s *Session) []seed {
	var seeds []seed
	maxAff := 0.0
	for _, ut := range u.Tracks {
		maxAff = math.Max(maxAff, ut.Affinity)
	}
	if maxAff > 0 {
		for idx, ut := range u.Tracks {
			if ut.Affinity > 0.15*maxAff {
				seeds = append(seeds, seed{idx: idx, w: ut.Affinity / maxAff})
			}
		}
	}
	sort.Slice(seeds, func(a, b int) bool {
		if seeds[a].w != seeds[b].w {
			return seeds[a].w > seeds[b].w
		}
		return seeds[a].idx < seeds[b].idx
	})
	if len(seeds) > 30 {
		seeds = seeds[:30]
	}
	for _, fb := range s.Feedback {
		if fb.Action == "like" {
			seeds = append(seeds, seed{idx: fb.Track, w: 1.5, session: true})
		}
	}
	return seeds
}

func tasteScore(taste map[string]float64, t *model.Track) float64 {
	v := profile.Propagation(profile.TasteArtist) * taste[TasteKey(profile.TasteArtist, t.ArtistID.String())]
	v += profile.Propagation(profile.TasteAlbum) * taste[TasteKey(profile.TasteAlbum, t.AlbumID.String())]
	if t.Genre != "" {
		v += profile.Propagation(profile.TasteGenre) * taste[TasteKey(profile.TasteGenre, t.Genre)]
	}
	for _, tag := range t.Tags {
		v += profile.Propagation(profile.TasteTag) * taste[TasteKey(profile.TasteTag, tag)]
	}
	return v
}

// normMax scales positives by the max positive and clips negatives at −1.
func normMax(v []float64) {
	mx := 0.0
	for _, x := range v {
		mx = math.Max(mx, x)
	}
	if mx <= 0 {
		for i := range v {
			v[i] = math.Max(-1, math.Min(0, v[i]))
		}
		return
	}
	for i := range v {
		v[i] = math.Max(-1, v[i]/mx)
	}
}

func thompson(u *User, w Weights, rnd *rand.Rand) map[string]float64 {
	out := map[string]float64{}
	for _, src := range LearnableSources {
		out[src] = 1
		if !w.Thompson || rnd == nil {
			continue
		}
		b := u.Sources[src]
		theta := sampleBeta(rnd, 5+b.Alpha, 5+b.Beta)
		out[src] = math.Max(0.3, math.Min(1.7, theta/0.5))
	}
	return out
}

func penalties(ut UserTrack, heard bool, now time.Time) float64 {
	if !heard {
		return 0
	}
	p := 0.0
	if !ut.LastPlayed.IsZero() {
		switch ago := now.Sub(ut.LastPlayed); {
		case ago < 2*time.Hour:
			p -= 1.0
		case ago < 24*time.Hour:
			p -= 0.35
		}
	}
	p -= 0.35 * float64(min(ut.EarlySkips, 3))
	if ut.Affinity < -1 {
		p -= 0.5
	}
	return p
}

func modeBonus(mode string, t *model.Track, ut UserTrack, heard, unheard bool, w Weights) float64 {
	switch mode {
	case ModeCalm:
		if t.Emb == nil {
			return -0.2 * w.Mode
		}
		return w.Mode * (1 - 2*t.EnergyPct)
	case ModeEnergetic:
		if t.Emb == nil {
			return -0.2 * w.Mode
		}
		return w.Mode * (2*t.EnergyPct - 1)
	case ModeDiscover:
		if unheard {
			return 0.5 * w.Mode
		}
		return -0.6 * w.Mode
	case ModeFavorites:
		b := 0.0
		if ut.Liked {
			b += 1.2 * w.Mode
		}
		if heard && ut.Affinity > 0 {
			b += 0.5 * w.Mode * math.Min(1, ut.Affinity/4)
		}
		if unheard {
			b -= 0.4 * w.Mode
		}
		return b
	}
	return 0
}

func dominant(comp map[string]float64, t *model.Track) string {
	best, src := 0.05, ""
	for _, k := range []string{SrcSession, SrcCF, SrcContent, SrcALS, SrcProfile, SrcFresh, SrcPopular} {
		if v := comp[k]; v > best {
			best, src = v, k
		}
	}
	if src == "" {
		if t.Popularity > 0 {
			return SrcPopular
		}
		return SrcExplore
	}
	return src
}

// selectBatch applies ε-exploration, MMR and artist constraints.
func selectBatch(m *model.Model, s *Session, cands []cand, w Weights, limit int, rnd *rand.Rand) []Pick {
	poolN := max(w.PoolSize, limit*4)
	pool := cands
	if len(pool) > poolN {
		pool = pool[:poolN]
	}
	var explore []cand
	for k, c := range cands {
		if k >= limit/2 && c.unheard {
			explore = append(explore, c)
		}
		if len(explore) >= 40 {
			break
		}
	}
	if len(pool) == 0 {
		return nil
	}
	lo, hi := pool[len(pool)-1].score, pool[0].score
	span := math.Max(hi-lo, 1e-9)
	used := map[int]bool{}
	perArtist := map[[16]byte]int{}
	prevArtist := [16]byte{}
	hasPrev := false
	var prevEmb [][]float32
	if len(s.Recent) > 0 {
		last := m.Tracks[s.Recent[len(s.Recent)-1]]
		prevArtist, hasPrev = last.ArtistID, true
		if last.Emb != nil {
			prevEmb = append(prevEmb, last.Emb)
		}
	}
	var out []Pick
	for len(out) < limit {
		var chosen *cand
		if w.Epsilon > 0 && rnd != nil && len(explore) > 0 && rnd.Float64() < w.Epsilon {
			for tries := 0; tries < 8 && chosen == nil; tries++ {
				c := explore[rnd.IntN(len(explore))]
				a := m.Tracks[c.idx].ArtistID
				if !used[c.idx] && (!hasPrev || a != prevArtist) && perArtist[a] < w.MaxPerArtist {
					cc := c
					cc.source = SrcExplore
					chosen = &cc
				}
			}
		}
		for relax := 0; chosen == nil && relax < 3; relax++ {
			bestV := math.Inf(-1)
			for k := range pool {
				c := &pool[k]
				if used[c.idx] {
					continue
				}
				t := &m.Tracks[c.idx]
				// relax 0: cap + no adjacency; 1: adjacency only; 2: anything goes
				if relax < 1 && perArtist[t.ArtistID] >= w.MaxPerArtist {
					continue
				}
				if relax < 2 && hasPrev && t.ArtistID == prevArtist {
					continue
				}
				simMax := 0.0
				for _, p := range out {
					pt := &m.Tracks[p.Track]
					sim := model.Dot(t.Emb, pt.Emb)
					if pt.ArtistID == t.ArtistID {
						sim = math.Max(sim, 0.8)
					}
					simMax = math.Max(simMax, sim)
				}
				for _, e := range prevEmb {
					simMax = math.Max(simMax, 0.5*model.Dot(t.Emb, e))
				}
				v := w.MMRLambda*(c.score-lo)/span - (1-w.MMRLambda)*simMax
				if v > bestV {
					bestV, chosen = v, c
				}
			}
		}
		if chosen == nil {
			break
		}
		t := &m.Tracks[chosen.idx]
		used[chosen.idx] = true
		perArtist[t.ArtistID]++
		prevArtist, hasPrev = t.ArtistID, true
		out = append(out, Pick{Track: chosen.idx, Score: chosen.score, Source: chosen.source})
	}
	return out
}

// Explanation limits: a "because you liked X" needs a real link to X, and one
// reference is used at most maxRefUses times per batch so captions stay varied.
const (
	minRefSim  = 0.2
	maxRefUses = 2
)

func explain(m *model.Model, u *User, seeds []seed, p Pick, refUses map[int]int) Reason {
	t := &m.Tracks[p.Track]
	r := Reason{Ref: -1}
	if ut, ok := u.Tracks[p.Track]; ok && ut.Liked {
		r.Kind = ReasonFavorite
		return r
	}
	switch p.Source {
	case SrcCF, SrcContent, SrcSession:
		best, bi := 0.0, -1
		for _, sd := range seeds {
			if sd.idx == p.Track || refUses[sd.idx] >= maxRefUses {
				continue
			}
			var sim float64
			if p.Source == SrcCF {
				for _, nb := range m.CF[sd.idx] {
					if int(nb.Idx) == p.Track {
						sim = float64(nb.Sim)
					}
				}
			} else {
				sim = model.Dot(m.Tracks[sd.idx].Emb, t.Emb)
				if m.Tracks[sd.idx].ArtistID == t.ArtistID {
					sim += 0.3
				}
			}
			if sim < minRefSim {
				continue
			}
			if sd.session {
				sim *= 1.3
			}
			if v := sd.w * sim; v > best {
				best, bi = v, sd.idx
			}
		}
		if bi >= 0 {
			r.Kind, r.Ref = ReasonBecauseLiked, bi
			return r
		}
		if p.Source == SrcSession {
			r.Kind, r.Artist = ReasonSessionArtist, t.Artist
			return r
		}
	case SrcALS:
		r.Kind = ReasonListeners
		return r
	case SrcFresh:
		r.Kind = ReasonFresh
		return r
	case SrcExplore:
		r.Kind = ReasonDiscovery
		return r
	}
	if p.Source == SrcProfile || p.Source == SrcCF || p.Source == SrcContent {
		a := u.Taste[TasteKey(profile.TasteArtist, t.ArtistID.String())]
		g := 0.0
		if t.Genre != "" {
			g = profile.Propagation(profile.TasteGenre) * u.Taste[TasteKey(profile.TasteGenre, t.Genre)]
		}
		bestTag, tv := "", 0.0
		for _, tag := range t.Tags {
			if v := profile.Propagation(profile.TasteTag) * u.Taste[TasteKey(profile.TasteTag, tag)]; v > tv {
				bestTag, tv = tag, v
			}
		}
		switch {
		case a > 0 && a >= g && a >= tv:
			r.Kind, r.Artist = ReasonArtist, t.Artist
		case g > 0 && g >= tv:
			r.Kind, r.Genre = ReasonGenre, t.Genre
		case tv > 0:
			r.Kind, r.Tag = ReasonTag, bestTag
		}
		if r.Kind != "" {
			return r
		}
	}
	if t.Popularity > 0 {
		r.Kind = ReasonPopular
	} else {
		r.Kind = ReasonDiscovery
	}
	return r
}

// sampleBeta draws from Beta(a, b) via two Gamma draws.
func sampleBeta(rnd *rand.Rand, a, b float64) float64 {
	x := sampleGamma(rnd, a)
	y := sampleGamma(rnd, b)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// sampleGamma: Marsaglia–Tsang (shape ≥ 1; boosted for shape < 1).
func sampleGamma(rnd *rand.Rand, k float64) float64 {
	if k < 1 {
		return sampleGamma(rnd, k+1) * math.Pow(rnd.Float64(), 1/k)
	}
	d := k - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := rnd.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rnd.Float64()
		if u < 1-0.0331*x*x*x*x || math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
