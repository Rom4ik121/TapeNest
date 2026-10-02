package profile

import "time"

// TrackState is the in-memory mirror of a reco.user_tracks row.
type TrackState struct {
	Affinity                                    float64
	At                                          time.Time
	Plays, Completes, EarlySkips, Skips, PlAdds int
	Liked                                       bool
	LastPlayed                                  time.Time
}

// Apply folds one event into the track state and returns the affinity delta
// (for taste propagation). Mirrors the SQL upsert used by the ingest worker.
func (s *TrackState) Apply(e Event) float64 {
	d, c := Delta(e, s.Completes)
	s.Affinity, s.At = Accumulate(s.Affinity, s.At, d, e.At)
	s.Plays += c.Plays
	s.Completes += c.Completes
	s.EarlySkips += c.EarlySkips
	s.Skips += c.Skips
	s.PlAdds += c.PlaylistAdds
	if c.Liked != nil {
		s.Liked = *c.Liked
	}
	if (e.Kind == KindPlay || e.Kind == KindSkip) && e.At.After(s.LastPlayed) {
		s.LastPlayed = e.At
	}
	return d
}

// Taste is an in-memory decayed weight map keyed by "kind:key".
type Taste struct {
	W  map[string]float64
	At map[string]time.Time
}

// NewTaste creates an empty taste map.
func NewTaste() *Taste { return &Taste{W: map[string]float64{}, At: map[string]time.Time{}} }

// Add propagates a track delta to its taste keys.
func (t *Taste) Add(keys []TasteKey, delta float64, at time.Time) {
	for _, k := range keys {
		id := k.Kind + ":" + k.Key
		t.W[id], t.At[id] = Accumulate(t.W[id], t.At[id], delta*Propagation(k.Kind), at)
	}
}

// Snapshot returns weights decayed to now.
func (t *Taste) Snapshot(now time.Time) map[string]float64 {
	out := make(map[string]float64, len(t.W))
	for k, v := range t.W {
		out[k] = Decay(v, t.At[k], now)
	}
	return out
}
