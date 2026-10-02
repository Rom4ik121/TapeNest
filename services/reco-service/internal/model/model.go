// Package model is the in-memory snapshot the ranker works on: catalog, audio
// embeddings, neighbour lists and latent factors, all indexed by dense ints.
// It is rebuilt by the API whenever reco.state.model_version changes.
package model

import (
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
)

// Track is one catalog entry with derived content data.
type Track struct {
	ID          uuid.UUID
	Title       string
	ArtistID    uuid.UUID
	Artist      string
	AlbumID     uuid.UUID
	Album       string
	Genre       string
	Year        int
	DurationSec int
	CreatedAt   time.Time
	Popularity  float64 // 0..1 (normalized score from music.track_popularity)

	Raw       []float32 // audio descriptor (nil: not analysed yet)
	Emb       []float32 // unit-length z-scored descriptor (nil: not analysed)
	Tempo     float64
	EnergyPct float64 // percentile of perceived energy within the catalog (0..1), see energyScore
	Tags      []string
}

// Neighbor is a similar item.
type Neighbor struct {
	Idx int32
	Sim float32
}

// Model is an immutable snapshot.
type Model struct {
	Version     int64
	Tracks      []Track
	Index       map[uuid.UUID]int
	CF          [][]Neighbor // item-item co-occurrence neighbours
	Content     [][]Neighbor // audio-feature cosine neighbours
	ItemFactors [][]float32  // ALS item factors (nil rows: unknown)
	UserFactors map[uuid.UUID][]float32
	BuiltAt     time.Time
}

// New indexes tracks and derives embeddings/tags from Raw descriptors.
func New(tracks []Track) *Model {
	m := &Model{Tracks: tracks, Index: make(map[uuid.UUID]int, len(tracks)), UserFactors: map[uuid.UUID][]float32{}, BuiltAt: time.Now()}
	for i := range tracks {
		m.Index[tracks[i].ID] = i
	}
	m.CF = make([][]Neighbor, len(tracks))
	m.Content = make([][]Neighbor, len(tracks))
	m.ItemFactors = make([][]float32, len(tracks))
	DeriveContent(m.Tracks)
	return m
}

// Group weights of the raw descriptor when building the embedding: the 20 mel
// bands together weigh about as much as the scalar descriptors.
func dimWeight(d int) float64 {
	switch {
	case d >= 11:
		return 0.55
	case d == 0: // tempo is noisy on old recordings
		return 0.7
	}
	return 1
}

// melShape returns the raw descriptor with the mel bands replaced by the
// spectral shape (log-mel minus its mean), so the embedding compares timbre,
// not recording level.
func melShape(raw []float32) []float64 {
	out := make([]float64, len(raw))
	var m float64
	for d, v := range raw {
		out[d] = float64(v)
		if d >= melStart {
			m += float64(v)
		}
	}
	m /= float64(len(raw) - melStart)
	for d := melStart; d < len(raw); d++ {
		out[d] -= m
	}
	return out
}

// melStart is the index of the first mel band in audio.Features.Raw.
const melStart = 11

// Raw descriptor indices used below (audio.Features.Raw order).
const (
	rawTempo    = 0
	rawLoudness = 1
	rawCentroid = 5
	rawFlux     = 8
	rawValence  = 9
)

// DeriveContent z-scores raw descriptors over the catalog into unit embeddings,
// computes the energy percentile and cheap mood tags.
func DeriveContent(tracks []Track) {
	dims := audio.RawDims
	var n float64
	mean := make([]float64, dims)
	shapes := make([][]float64, len(tracks))
	for i, t := range tracks {
		if len(t.Raw) != dims {
			continue
		}
		n++
		shapes[i] = melShape(t.Raw)
		for d, v := range shapes[i] {
			mean[d] += v
		}
	}
	if n == 0 {
		return
	}
	for d := range mean {
		mean[d] /= n
	}
	sd := make([]float64, dims)
	for _, sh := range shapes {
		for d, v := range sh {
			sd[d] += (v - mean[d]) * (v - mean[d])
		}
	}
	for d := range sd {
		sd[d] = math.Sqrt(sd[d]/n) + 1e-9
	}
	z := func(sh []float64, d int) float64 { return (sh[d] - mean[d]) / sd[d] }
	var energies, centroids []float64
	for _, sh := range shapes {
		if sh != nil {
			energies = append(energies, energyScore(sh, z))
			centroids = append(centroids, sh[rawCentroid])
		}
	}
	sort.Float64s(energies)
	sort.Float64s(centroids)
	for i := range tracks {
		t := &tracks[i]
		t.Emb, t.Tags = nil, nil
		sh := shapes[i]
		if sh == nil {
			continue
		}
		e := make([]float32, dims)
		for d := range sh {
			e[d] = float32(z(sh, d) * dimWeight(d))
		}
		t.Emb = Normalize(e)
		t.Tempo = sh[rawTempo]
		t.EnergyPct = rank(energies, energyScore(sh, z))
		t.Tags = moodTags(t.Tempo, t.EnergyPct, rank(centroids, sh[rawCentroid]), sh[rawValence])
	}
}

// energyScore is a perceived-energy proxy: onset/flux density on normalised
// audio dominates, loudness, tempo and brightness refine it. RMS alone mostly
// measured mastering level (a hot-mastered nocturne came out "energetic").
func energyScore(sh []float64, z func([]float64, int) float64) float64 {
	return 0.45*z(sh, rawFlux) + 0.25*z(sh, rawLoudness) + 0.15*z(sh, rawTempo) + 0.15*z(sh, rawCentroid)
}

func rank(sorted []float64, v float64) float64 {
	if len(sorted) < 2 {
		return 0.5
	}
	i := sort.SearchFloat64s(sorted, v)
	return float64(i) / float64(len(sorted)-1)
}

// moodTags derives human-readable tags; they also feed the taste profile.
func moodTags(tempo, energyPct, brightPct, valence float64) []string {
	var tags []string
	switch {
	case energyPct >= 0.67:
		tags = append(tags, "energetic")
	case energyPct <= 0.33:
		tags = append(tags, "calm")
	}
	switch {
	case tempo >= 125:
		tags = append(tags, "fast")
	case tempo > 0 && tempo <= 85:
		tags = append(tags, "slow")
	}
	switch {
	case brightPct >= 0.7:
		tags = append(tags, "bright")
	case brightPct <= 0.3:
		tags = append(tags, "warm")
	}
	if valence >= 0.15 {
		tags = append(tags, "major")
	} else if valence <= -0.15 {
		tags = append(tags, "minor")
	}
	return tags
}

// Normalize returns v scaled to unit length (zero vector stays zero).
func Normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(s))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// Dot is the inner product (cosine for unit vectors); 0 when either is nil.
func Dot(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
