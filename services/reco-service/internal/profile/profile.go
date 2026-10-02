// Package profile turns raw user signals into the long-term taste profile:
// a decayed affinity per (user, track) plus decayed weights per artist, album,
// genre and audio tag. The same pure functions are used by the ingest worker
// (persisted in reco.user_tracks / reco.user_taste) and by cmd/reco-eval.
package profile

import (
	"math"
	"time"
)

// Event kinds understood by the profile.
const (
	KindPlay           = "play"            // listen event (≥50% or completed)
	KindSkip           = "skip"            // track left before the listen event (position given)
	KindLike           = "like"            // library like
	KindUnlike         = "unlike"          // library unlike
	KindPlaylistAdd    = "playlist_add"    // added to a playlist
	KindPlaylistRemove = "playlist_remove" // removed from a playlist
	KindWaveLike       = "wave_like"       // like inside My Wave (session feedback)
	KindWaveSkip       = "wave_skip"       // skip inside My Wave (session feedback)
)

// Taste kinds (reco.user_taste.kind).
const (
	TasteArtist = "artist"
	TasteAlbum  = "album"
	TasteGenre  = "genre"
	TasteTag    = "tag"
)

// Tunables (documented in ADR 0010 §3).
const (
	HalfLife        = 30 * 24 * time.Hour
	EarlySkipSec    = 30
	WeightComplete  = 1.0
	WeightHalf      = 0.5 // listen event at ≥50% without completion
	WeightReplay    = 0.5 // extra per repeated completion
	WeightEarlySkip = -1.0
	WeightLateSkip  = -0.3
	WeightLike      = 3.0
	WeightPlaylist  = 2.0
	WeightPlRemove  = -1.0
	WeightWaveLike  = 1.0
	WeightWaveSkip  = -0.5
)

// Propagation of a track delta to taste keys.
var propagation = map[string]float64{TasteArtist: 1.0, TasteAlbum: 0.6, TasteGenre: 0.5, TasteTag: 0.25}

// Propagation returns the factor for a taste kind.
func Propagation(kind string) float64 { return propagation[kind] }

// Event is one normalized user signal.
type Event struct {
	Kind        string
	PositionSec float64
	Completed   bool
	At          time.Time
}

// Counters are the per (user, track) counters kept next to the affinity.
type Counters struct {
	Plays, Completes, EarlySkips, Skips, PlaylistAdds int
	Liked                                             *bool // nil: unchanged
}

// Delta returns the affinity change and counter increments for an event, given
// how many completions the user already had for the track (replays).
func Delta(e Event, priorCompletes int) (float64, Counters) {
	var c Counters
	t, f := true, false
	switch e.Kind {
	case KindPlay:
		c.Plays = 1
		if e.Completed {
			c.Completes = 1
			d := WeightComplete
			if priorCompletes > 0 {
				d += WeightReplay
			}
			return d, c
		}
		return WeightHalf, c
	case KindSkip:
		if e.PositionSec < EarlySkipSec {
			c.EarlySkips = 1
			return WeightEarlySkip, c
		}
		c.Skips = 1
		return WeightLateSkip, c
	case KindLike:
		c.Liked = &t
		return WeightLike, c
	case KindUnlike:
		c.Liked = &f
		return -WeightLike, c
	case KindPlaylistAdd:
		c.PlaylistAdds = 1
		return WeightPlaylist, c
	case KindPlaylistRemove:
		return WeightPlRemove, c
	case KindWaveLike:
		return WeightWaveLike, c
	case KindWaveSkip:
		c.Skips = 1
		return WeightWaveSkip, c
	}
	return 0, c
}

// Decay returns v measured at `from` decayed to `to` (never grows for to < from).
func Decay(v float64, from, to time.Time) float64 {
	dt := to.Sub(from)
	if dt <= 0 {
		return v
	}
	return v * math.Pow(0.5, float64(dt)/float64(HalfLife))
}

// Accumulate adds delta at time at to a value last updated at `last`.
// Out-of-order (older) events are decayed to the current reference time instead.
func Accumulate(v float64, last time.Time, delta float64, at time.Time) (float64, time.Time) {
	if last.IsZero() {
		return delta, at
	}
	if at.Before(last) {
		return v + Decay(delta, at, last), last
	}
	return Decay(v, last, at) + delta, at
}

// TasteKey is one taste dimension (kind + key) a track contributes to.
type TasteKey struct{ Kind, Key string }

// KeysFor returns taste keys of a track (artist, album, genre, tags).
func KeysFor(artistID, albumID, genre string, tags []string) []TasteKey {
	var ks []TasteKey
	if artistID != "" {
		ks = append(ks, TasteKey{TasteArtist, artistID})
	}
	if albumID != "" {
		ks = append(ks, TasteKey{TasteAlbum, albumID})
	}
	if genre != "" {
		ks = append(ks, TasteKey{TasteGenre, genre})
	}
	for _, t := range tags {
		ks = append(ks, TasteKey{TasteTag, t})
	}
	return ks
}
