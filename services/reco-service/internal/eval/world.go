// Package eval is the offline evaluation harness: a synthetic-users simulator
// (latent genre / artist / audio preferences + popularity bias), a time-split
// holdout, ranking metrics and a closed-loop wave-session simulation. It compares
// the old music-service heuristic (ADR 0009 §6) with the reco blend and ablations.
package eval

import (
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
)

// Config sizes the synthetic world.
type Config struct {
	Seed          uint64
	Genres        int
	Artists       int
	Tracks        int
	Users         int
	EventsPerUser int
	Days          int
	NewTrackShare float64 // share of tracks added "recently" (no training interactions)
	HoldoutShare  float64 // last share of each user's timeline used as test
	K             int
}

// DefaultConfig is what docs/reco/evaluation.md reports.
func DefaultConfig() Config {
	return Config{
		Seed: 7, Genres: 8, Artists: 90, Tracks: 720, Users: 300, EventsPerUser: 70, Days: 60,
		NewTrackShare: 0.08, HoldoutShare: 0.25, K: 10,
	}
}

const latentDims = 8

// SynthTrack carries the ground truth of a generated track.
type SynthTrack struct {
	Genre, Artist int
	Latent        []float64
	Pop           float64 // true popularity weight (Zipf)
	New           bool
}

// SynthUser carries a user's hidden preferences.
type SynthUser struct {
	GenrePref  []float64
	FavArtists map[int]bool
	Audio      []float64
	PopBias    float64
	Threshold  float64
}

// Event is one generated interaction.
type Event struct {
	User, Track int
	Kind        string
	Completed   bool
	Position    float64
	At          time.Time
}

// World is a generated dataset.
type World struct {
	Cfg    Config
	Tracks []SynthTrack
	Users  []SynthUser
	Events []Event // sorted by time
	Start  time.Time
	util   [][]float64
}

// NewWorld generates the dataset deterministically from cfg.Seed.
func NewWorld(cfg Config) *World {
	rnd := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed*31+1)) //nolint:gosec // simulation
	w := &World{Cfg: cfg, Start: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	centers := make([][]float64, cfg.Genres)
	for g := range centers {
		centers[g] = randVec(rnd, latentDims, 1.0)
	}
	artistGenre := make([]int, cfg.Artists)
	artistOff := make([][]float64, cfg.Artists)
	for a := range artistGenre {
		artistGenre[a] = a % cfg.Genres
		artistOff[a] = randVec(rnd, latentDims, 0.45)
	}
	for i := 0; i < cfg.Tracks; i++ {
		a := rnd.IntN(cfg.Artists)
		g := artistGenre[a]
		lat := make([]float64, latentDims)
		for d := range lat {
			lat[d] = centers[g][d] + artistOff[a][d] + rnd.NormFloat64()*0.3
		}
		w.Tracks = append(w.Tracks, SynthTrack{
			Genre: g, Artist: a, Latent: lat,
			Pop: 1 / math.Pow(float64(rnd.IntN(cfg.Tracks)+1), 0.8), New: rnd.Float64() < cfg.NewTrackShare,
		})
	}
	for u := 0; u < cfg.Users; u++ {
		gp := make([]float64, cfg.Genres)
		fav := rnd.IntN(cfg.Genres)
		gp[fav] = 1
		if rnd.Float64() < 0.6 {
			gp[rnd.IntN(cfg.Genres)] += 0.6
		}
		fa := map[int]bool{}
		for len(fa) < 3 {
			a := rnd.IntN(cfg.Artists)
			if gp[artistGenre[a]] > 0 || rnd.Float64() < 0.2 {
				fa[a] = true
			}
		}
		au := make([]float64, latentDims)
		for d := range au {
			au[d] = centers[fav][d] + rnd.NormFloat64()*0.5
		}
		w.Users = append(w.Users, SynthUser{GenrePref: gp, FavArtists: fa, Audio: au, PopBias: 0.1 + 0.3*rnd.Float64(), Threshold: 0.9})
	}
	w.util = make([][]float64, cfg.Users)
	for u := range w.Users {
		w.util[u] = make([]float64, cfg.Tracks)
		for i := range w.Tracks {
			w.util[u][i] = w.trueUtility(u, i) + rnd.NormFloat64()*0.15
		}
	}
	w.genEvents(rnd)
	return w
}

func (w *World) trueUtility(u, i int) float64 {
	us, t := &w.Users[u], &w.Tracks[i]
	v := 1.2 * us.GenrePref[t.Genre]
	if us.FavArtists[t.Artist] {
		v += 0.8
	}
	v += 0.6 * cosine(us.Audio, t.Latent)
	v += us.PopBias * math.Log10(1+100*t.Pop)
	return v
}

// Utility is the (noisy, fixed) utility of track i for user u.
func (w *World) Utility(u, i int) float64 { return w.util[u][i] }

// genEvents simulates organic listening: exposure is a mix of popular, genre
// browsing and random discovery; the outcome depends on the true utility.
func (w *World) genEvents(rnd *rand.Rand) {
	cfg := w.Cfg
	popCDF := make([]float64, len(w.Tracks))
	acc := 0.0
	for i, t := range w.Tracks {
		acc += t.Pop
		popCDF[i] = acc
	}
	byGenre := make([][]int, cfg.Genres)
	for i, t := range w.Tracks {
		byGenre[t.Genre] = append(byGenre[t.Genre], i)
	}
	span := time.Duration(cfg.Days) * 24 * time.Hour
	for u := range w.Users {
		us := &w.Users[u]
		for e := 0; e < cfg.EventsPerUser; e++ {
			var i int
			switch r := rnd.Float64(); {
			case r < 0.4:
				i = sort.SearchFloat64s(popCDF, rnd.Float64()*acc)
			case r < 0.8:
				g := argmaxSample(rnd, us.GenrePref)
				i = byGenre[g][rnd.IntN(len(byGenre[g]))]
			default:
				i = rnd.IntN(len(w.Tracks))
			}
			i = min(i, len(w.Tracks)-1)
			if w.Tracks[i].New && float64(e) < float64(cfg.EventsPerUser)*(1-cfg.HoldoutShare) {
				continue // new tracks only appear in the holdout window
			}
			at := w.Start.Add(time.Duration(float64(span) * (float64(e) + rnd.Float64()) / float64(cfg.EventsPerUser)))
			ut := w.util[u][i]
			pc := sigmoid(3 * (ut - us.Threshold))
			if rnd.Float64() < pc {
				w.Events = append(w.Events, Event{User: u, Track: i, Kind: profile.KindPlay, Completed: true, Position: 180, At: at})
				if rnd.Float64() < sigmoid(4*(ut-us.Threshold-0.7)) {
					w.Events = append(w.Events, Event{User: u, Track: i, Kind: profile.KindLike, At: at.Add(time.Minute)})
				}
				if rnd.Float64() < sigmoid(4*(ut-us.Threshold-0.9))*0.5 {
					w.Events = append(w.Events, Event{User: u, Track: i, Kind: profile.KindPlaylistAdd, At: at.Add(2 * time.Minute)})
				}
			} else {
				w.Events = append(w.Events, Event{User: u, Track: i, Kind: profile.KindSkip, Position: 5 + 40*rnd.Float64(), At: at})
			}
		}
	}
	sort.SliceStable(w.Events, func(a, b int) bool { return w.Events[a].At.Before(w.Events[b].At) })
}

// Split is a time-based holdout.
type Split struct {
	Train  []Event
	Test   map[int]map[int]bool // user → positive tracks first seen in the test window
	Cutoff time.Time
}

// Holdout splits each user's timeline: the last HoldoutShare of the global time
// range is test; positives are completions/likes of tracks unseen in training.
func (w *World) Holdout() Split {
	span := time.Duration(w.Cfg.Days) * 24 * time.Hour
	cut := w.Start.Add(time.Duration(float64(span) * (1 - w.Cfg.HoldoutShare)))
	s := Split{Test: map[int]map[int]bool{}, Cutoff: cut}
	seen := map[[2]int]bool{}
	for _, e := range w.Events {
		if e.At.Before(cut) {
			s.Train = append(s.Train, e)
			seen[[2]int{e.User, e.Track}] = true
		}
	}
	for _, e := range w.Events {
		if e.At.Before(cut) || seen[[2]int{e.User, e.Track}] {
			continue
		}
		if (e.Kind == profile.KindPlay && e.Completed) || e.Kind == profile.KindLike {
			if s.Test[e.User] == nil {
				s.Test[e.User] = map[int]bool{}
			}
			s.Test[e.User][e.Track] = true
		}
	}
	return s
}

// recordingGainSD is the spread of the per-track recording-level offset.
const recordingGainSD = 1.5

// Catalog builds model tracks: synthetic ids, genre, artist, popularity from
// training plays and a raw audio descriptor derived from the latent vector.
func (w *World) Catalog(train []Event, cutoff time.Time) []model.Track {
	rnd := rand.New(rand.NewPCG(w.Cfg.Seed+99, 5)) //nolint:gosec // simulation
	proj := make([][]float64, audio.RawDims)
	for d := range proj {
		proj[d] = randVec(rnd, latentDims, 1)
	}
	plays := make([]float64, len(w.Tracks))
	for _, e := range train {
		if e.Kind == profile.KindPlay {
			plays[e.Track]++
		}
	}
	maxP := 0.0
	for _, p := range plays {
		maxP = math.Max(maxP, math.Log1p(p))
	}
	artistIDs := make([]uuid.UUID, w.Cfg.Artists)
	for a := range artistIDs {
		artistIDs[a] = detUUID(rnd)
	}
	out := make([]model.Track, len(w.Tracks))
	for i, t := range w.Tracks {
		raw := make([]float32, audio.RawDims)
		// recording level is a nuisance factor unrelated to taste (mastering,
		// 78 rpm transfers…): it shifts loudness, RMS and every log-mel band alike
		gain := rnd.NormFloat64() * recordingGainSD
		for d := range raw {
			var v float64
			for k := range t.Latent {
				v += proj[d][k] * t.Latent[k]
			}
			if d == 1 || d == 2 || d >= 11 {
				v += gain
			}
			raw[d] = float32(v + rnd.NormFloat64()*0.6)
		}
		created := w.Start.Add(-90 * 24 * time.Hour)
		if t.New {
			created = cutoff.Add(-3 * 24 * time.Hour)
		}
		pop := 0.0
		if maxP > 0 {
			pop = math.Log1p(plays[i]) / maxP
		}
		out[i] = model.Track{
			ID: detUUID(rnd), Title: "t", ArtistID: artistIDs[t.Artist], Artist: "a",
			AlbumID: artistIDs[t.Artist], Genre: genreName(t.Genre), Popularity: pop, CreatedAt: created, Raw: raw,
		}
	}
	return out
}

func detUUID(rnd *rand.Rand) uuid.UUID {
	var u uuid.UUID
	for i := range u {
		u[i] = byte(rnd.Uint32()) //nolint:gosec // random bytes, truncation intended
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

func randVec(rnd *rand.Rand, n int, scale float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = rnd.NormFloat64() * scale
	}
	return v
}

func cosine(a, b []float64) float64 {
	var ab, aa, bb float64
	for i := range a {
		ab += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func argmaxSample(rnd *rand.Rand, w []float64) int {
	sum := 0.0
	for _, v := range w {
		sum += v
	}
	r := rnd.Float64() * sum
	for i, v := range w {
		r -= v
		if r <= 0 {
			return i
		}
	}
	return len(w) - 1
}

// genreName maps a synthetic genre index to "A", "B", ...
func genreName(g int) string { return string("ABCDEFGHIJKLMNOPQRSTUVWXYZ"[g%26]) }
