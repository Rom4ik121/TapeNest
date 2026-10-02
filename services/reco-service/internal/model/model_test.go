package model

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
)

func raw(tempo, rms, centroid, valence float32) []float32 {
	r := make([]float32, audio.RawDims)
	r[0], r[2], r[5], r[9] = tempo, rms, centroid, valence
	for d := 11; d < audio.RawDims; d++ {
		r[d] = rms * float32(d)
	}
	return r
}

func TestNewDerivesEmbeddingsAndTags(t *testing.T) {
	ts := []Track{
		{ID: uuid.New(), Raw: raw(140, 0.30, 3000, 0.5)},
		{ID: uuid.New(), Raw: raw(70, 0.02, 800, -0.5)},
		{ID: uuid.New(), Raw: raw(100, 0.10, 1500, 0)},
		{ID: uuid.New()}, // not analysed
	}
	m := New(ts)
	if len(m.Index) != 4 || m.Index[ts[2].ID] != 2 {
		t.Fatal("index")
	}
	for i := 0; i < 3; i++ {
		var n float64
		for _, v := range m.Tracks[i].Emb {
			n += float64(v * v)
		}
		if math.Abs(n-1) > 1e-4 {
			t.Fatalf("embedding %d not unit: %f", i, n)
		}
	}
	if m.Tracks[3].Emb != nil || m.Tracks[3].Tags != nil {
		t.Fatal("unanalysed track must have no embedding")
	}
	has := func(tags []string, want string) bool {
		for _, x := range tags {
			if x == want {
				return true
			}
		}
		return false
	}
	if !has(m.Tracks[0].Tags, "energetic") || !has(m.Tracks[0].Tags, "fast") || !has(m.Tracks[0].Tags, "major") || !has(m.Tracks[0].Tags, "bright") {
		t.Fatalf("tags of loud fast track: %v", m.Tracks[0].Tags)
	}
	if !has(m.Tracks[1].Tags, "calm") || !has(m.Tracks[1].Tags, "slow") || !has(m.Tracks[1].Tags, "minor") || !has(m.Tracks[1].Tags, "warm") {
		t.Fatalf("tags of quiet slow track: %v", m.Tracks[1].Tags)
	}
	if m.Tracks[0].EnergyPct != 1 || m.Tracks[1].EnergyPct != 0 {
		t.Fatalf("energy pct %v %v", m.Tracks[0].EnergyPct, m.Tracks[1].EnergyPct)
	}
	if Dot(m.Tracks[0].Emb, m.Tracks[0].Emb) < 0.999 || Dot(nil, m.Tracks[0].Emb) != 0 {
		t.Fatal("dot")
	}
	if len(m.CF) != 4 || len(m.Content) != 4 || len(m.ItemFactors) != 4 || m.BuiltAt.After(time.Now()) {
		t.Fatal("slices")
	}
}

func TestEmptyAndNormalize(t *testing.T) {
	DeriveContent(nil)
	z := Normalize([]float32{0, 0})
	if z[0] != 0 {
		t.Fatal("zero vector")
	}
	if rank([]float64{1}, 1) != 0.5 {
		t.Fatal("rank of single")
	}
	if tags := moodTags(100, 0.5, 0.5, 0); len(tags) != 0 {
		t.Fatalf("neutral tags %v", tags)
	}
}
