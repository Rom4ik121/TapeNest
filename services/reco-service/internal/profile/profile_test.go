package profile

import (
	"math"
	"testing"
	"time"
)

func TestDelta(t *testing.T) {
	cases := []struct {
		e     Event
		prior int
		want  float64
	}{
		{Event{Kind: KindPlay, Completed: true}, 0, WeightComplete},
		{Event{Kind: KindPlay, Completed: true}, 2, WeightComplete + WeightReplay},
		{Event{Kind: KindPlay}, 0, WeightHalf},
		{Event{Kind: KindSkip, PositionSec: 10}, 0, WeightEarlySkip},
		{Event{Kind: KindSkip, PositionSec: 60}, 0, WeightLateSkip},
		{Event{Kind: KindLike}, 0, WeightLike},
		{Event{Kind: KindUnlike}, 0, -WeightLike},
		{Event{Kind: KindPlaylistAdd}, 0, WeightPlaylist},
		{Event{Kind: KindPlaylistRemove}, 0, WeightPlRemove},
		{Event{Kind: KindWaveLike}, 0, WeightWaveLike},
		{Event{Kind: KindWaveSkip}, 0, WeightWaveSkip},
		{Event{Kind: "bogus"}, 0, 0},
	}
	for _, c := range cases {
		if got, _ := Delta(c.e, c.prior); got != c.want {
			t.Errorf("%+v: got %v want %v", c.e, got, c.want)
		}
	}
}

func TestDecayAndAccumulate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if v := Decay(8, t0, t0.Add(HalfLife)); math.Abs(v-4) > 1e-9 {
		t.Fatalf("half-life decay: %v", v)
	}
	if Decay(8, t0, t0.Add(-time.Hour)) != 8 {
		t.Fatal("no growth backwards")
	}
	v, at := Accumulate(0, time.Time{}, 3, t0)
	if v != 3 || !at.Equal(t0) {
		t.Fatal("first accumulate")
	}
	v, at = Accumulate(v, at, 1, t0.Add(HalfLife))
	if math.Abs(v-2.5) > 1e-9 || !at.Equal(t0.Add(HalfLife)) {
		t.Fatalf("accumulate forward: %v", v)
	}
	v2, at2 := Accumulate(v, at, 2, t0) // older event decays to the reference time
	if math.Abs(v2-3.5) > 1e-9 || !at2.Equal(at) {
		t.Fatalf("accumulate backwards: %v", v2)
	}
}

func TestTrackStateAndTaste(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var s TrackState
	s.Apply(Event{Kind: KindPlay, Completed: true, At: t0})
	s.Apply(Event{Kind: KindPlay, Completed: true, At: t0})
	s.Apply(Event{Kind: KindLike, At: t0})
	s.Apply(Event{Kind: KindSkip, PositionSec: 5, At: t0.Add(time.Hour)})
	s.Apply(Event{Kind: KindPlaylistAdd, At: t0})
	if s.Plays != 2 || s.Completes != 2 || !s.Liked || s.EarlySkips != 1 || s.PlAdds != 1 || !s.LastPlayed.Equal(t0.Add(time.Hour)) {
		t.Fatalf("state %+v", s)
	}
	s.Apply(Event{Kind: KindUnlike, At: t0.Add(2 * time.Hour)})
	if s.Liked {
		t.Fatal("unlike")
	}
	tt := NewTaste()
	keys := KeysFor("a1", "al1", "Jazz", []string{"calm"})
	if len(keys) != 4 || len(KeysFor("", "", "", nil)) != 0 {
		t.Fatal("keys")
	}
	tt.Add(keys, 2, t0)
	snap := tt.Snapshot(t0)
	if snap["artist:a1"] != 2 || snap["album:al1"] != 1.2 || snap["genre:Jazz"] != 1 || snap["tag:calm"] != 0.5 {
		t.Fatalf("taste %v", snap)
	}
	if Propagation(TasteArtist) != 1 {
		t.Fatal("propagation")
	}
}
