// Package domain is the video editor's recipe: what ffmpeg is allowed to do
// to a file the user already downloaded.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits keep an export practical on one CPU.
const (
	MaxClips     = 12
	MaxTexts     = 8
	MinClipSec   = 0.2
	MaxSpeed     = 4
	MinSpeed     = 0.25
	MaxTextRunes = 80
)

// Transitions ffmpeg's xfade filter actually implements.
var Transitions = map[string]struct{}{
	"none": {}, "fade": {}, "wipeleft": {}, "wiperight": {},
	"slideleft": {}, "slideright": {}, "circleopen": {}, "fadeblack": {},
}

// Crop is a normalized rectangle (0..1) of the source frame.
type Crop struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Clip is one piece of the source, in timeline order.
type Clip struct {
	StartSec      float64 `json:"startSec"`
	EndSec        float64 `json:"endSec"`
	Speed         float64 `json:"speed"`
	Volume        float64 `json:"volume"`
	Crop          Crop    `json:"crop"`
	Rotate        int     `json:"rotate"`
	Transition    string  `json:"transition"`
	TransitionSec float64 `json:"transitionSec"`
}

// Text is a burned-in caption.
type Text struct {
	Text     string  `json:"text"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Size     int     `json:"size"`
	Color    string  `json:"color"`
}

// Recipe is the whole edit. Clip order is the timeline (split + reorder).
type Recipe struct {
	Clips       []Clip  `json:"clips"`
	Texts       []Text  `json:"texts"`
	MusicVolume float64 `json:"musicVolume"`
	MuteSource  bool    `json:"muteSource"`
}

// FullCrop is the uncropped frame.
func FullCrop() Crop { return Crop{X: 0, Y: 0, W: 1, H: 1} }

// DefaultRecipe is one clip covering the whole download.
func DefaultRecipe(duration float64) Recipe {
	if duration <= 0 {
		duration = 1
	}
	return Recipe{
		Clips: []Clip{{
			StartSec: 0, EndSec: duration, Speed: 1, Volume: 1,
			Crop: FullCrop(), Rotate: 0, Transition: "none", TransitionSec: 0.4,
		}},
		MusicVolume: 0.35,
	}
}

// Validate checks a recipe against the source duration (0 = unknown).
func (r Recipe) Validate(duration float64) error {
	if n := len(r.Clips); n < 1 || n > MaxClips {
		return fmt.Errorf("clips must be 1..%d", MaxClips)
	}
	if len(r.Texts) > MaxTexts {
		return fmt.Errorf("texts must be at most %d", MaxTexts)
	}
	if r.MusicVolume < 0 || r.MusicVolume > 1 {
		return errors.New("music volume must be 0..1")
	}
	for i, c := range r.Clips {
		if err := c.validate(i, duration); err != nil {
			return err
		}
	}
	for i, t := range r.Texts {
		if err := t.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (c Clip) validate(i int, duration float64) error {
	if c.StartSec < 0 || c.EndSec <= c.StartSec {
		return fmt.Errorf("clip %d: start must be before end", i+1)
	}
	if c.EndSec-c.StartSec < MinClipSec {
		return fmt.Errorf("clip %d: too short", i+1)
	}
	if duration > 0 && c.EndSec > duration+0.05 {
		return fmt.Errorf("clip %d: end is past the video", i+1)
	}
	if c.Speed < MinSpeed || c.Speed > MaxSpeed {
		return fmt.Errorf("clip %d: speed must be %.2f..%d", i+1, MinSpeed, MaxSpeed)
	}
	if c.Volume < 0 || c.Volume > 2 {
		return fmt.Errorf("clip %d: volume must be 0..2", i+1)
	}
	if c.Rotate != 0 && c.Rotate != 90 && c.Rotate != 180 && c.Rotate != 270 {
		return fmt.Errorf("clip %d: rotate must be 0, 90, 180 or 270", i+1)
	}
	if _, ok := Transitions[c.Transition]; !ok {
		return fmt.Errorf("clip %d: unknown transition", i+1)
	}
	if i > 0 && (c.TransitionSec < 0.1 || c.TransitionSec > 2) {
		return fmt.Errorf("clip %d: transition length must be 0.1..2s", i+1)
	}
	cr := c.Crop
	if cr.W < 0.05 || cr.H < 0.05 || cr.X < 0 || cr.Y < 0 || cr.X+cr.W > 1.001 || cr.Y+cr.H > 1.001 {
		return fmt.Errorf("clip %d: crop is outside the frame", i+1)
	}
	return nil
}

func (t Text) validate(i int) error {
	s := strings.TrimSpace(t.Text)
	if s == "" || utf8.RuneCountInString(s) > MaxTextRunes {
		return fmt.Errorf("text %d: must be 1..%d characters", i+1, MaxTextRunes)
	}
	for _, r := range s {
		if r < 0x20 {
			return fmt.Errorf("text %d: control characters are not allowed", i+1)
		}
	}
	if t.EndSec <= t.StartSec || t.StartSec < 0 {
		return fmt.Errorf("text %d: start must be before end", i+1)
	}
	if t.X < 0 || t.X > 1 || t.Y < 0 || t.Y > 1 {
		return fmt.Errorf("text %d: position must be inside the frame", i+1)
	}
	if t.Size < 12 || t.Size > 96 {
		return fmt.Errorf("text %d: size must be 12..96", i+1)
	}
	if !hexColor(t.Color) {
		return fmt.Errorf("text %d: color must be #RRGGBB", i+1)
	}
	return nil
}

func hexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// OutputDuration is the timeline length after speed and transitions.
func (r Recipe) OutputDuration() float64 {
	if len(r.Clips) == 0 {
		return 0
	}
	total := 0.0
	for i, c := range r.Clips {
		total += (c.EndSec - c.StartSec) / c.Speed
		if i > 0 && c.Transition != "none" {
			total -= c.TransitionSec
		}
	}
	if total < 0.1 {
		return 0.1
	}
	return total
}
