// Package domain is a photo edit: crop, rotate, straighten, exposure, color,
// a filter, optional text, and the export format.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Filters the editor knows.
var Filters = map[string]struct{}{
	"none": {}, "vivid": {}, "mono": {}, "warm": {}, "cool": {}, "fade": {},
}

// Crop is a normalized rectangle of the original photo.
type Crop struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Text is an optional caption. Empty text draws nothing.
type Text struct {
	Text  string  `json:"text"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Size  float64 `json:"size"`
	Color string  `json:"color"`
}

// Recipe is one export.
type Recipe struct {
	Crop        Crop    `json:"crop"`
	Rotate      int     `json:"rotate"`
	Straighten  float64 `json:"straighten"`
	Exposure    float64 `json:"exposure"`
	Contrast    float64 `json:"contrast"`
	Saturation  float64 `json:"saturation"`
	Temperature float64 `json:"temperature"`
	Filter      string  `json:"filter"`
	Text        Text    `json:"text"`
	Format      string  `json:"format"`
}

// Identity is a recipe that leaves the photo unchanged and exports JPEG.
func Identity() Recipe {
	return Recipe{
		Crop: Crop{X: 0, Y: 0, W: 1, H: 1}, Filter: "none", Format: "jpeg",
		Text: Text{X: 0.5, Y: 0.85, Size: 0.06, Color: "#FFFFFF"},
	}
}

// Validate checks ranges. An empty caption is allowed.
func (r Recipe) Validate() error {
	c := r.Crop
	if c.W < 0.05 || c.H < 0.05 || c.X < 0 || c.Y < 0 || c.X+c.W > 1.001 || c.Y+c.H > 1.001 {
		return errors.New("crop is outside the photo")
	}
	if r.Rotate != 0 && r.Rotate != 90 && r.Rotate != 180 && r.Rotate != 270 {
		return errors.New("rotate must be 0, 90, 180 or 270")
	}
	if r.Straighten < -45 || r.Straighten > 45 {
		return errors.New("straighten must be -45..45 degrees")
	}
	if r.Exposure < -2 || r.Exposure > 2 {
		return errors.New("exposure must be -2..2")
	}
	for _, v := range []float64{r.Contrast, r.Saturation, r.Temperature} {
		if v < -1 || v > 1 {
			return errors.New("color adjustments must be -1..1")
		}
	}
	if _, ok := Filters[r.Filter]; !ok {
		return errors.New("unknown filter")
	}
	if r.Format != "jpeg" && r.Format != "png" {
		return errors.New("format must be jpeg or png")
	}
	text := strings.TrimSpace(r.Text.Text)
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) > 80 {
		return errors.New("text must be at most 80 characters")
	}
	for _, ch := range text {
		if ch < 0x20 {
			return errors.New("text cannot contain control characters")
		}
	}
	if r.Text.X < 0 || r.Text.X > 1 || r.Text.Y < 0 || r.Text.Y > 1 {
		return errors.New("text must sit inside the photo")
	}
	if r.Text.Size < 0.03 || r.Text.Size > 0.2 {
		return errors.New("text size must be 3%..20% of the width")
	}
	if len(r.Text.Color) != 7 || r.Text.Color[0] != '#' {
		return errors.New("text color must be #RRGGBB")
	}
	for _, ch := range r.Text.Color[1:] {
		switch {
		case ch >= '0' && ch <= '9', ch >= 'a' && ch <= 'f', ch >= 'A' && ch <= 'F':
		default:
			return errors.New("text color must be #RRGGBB")
		}
	}
	return nil
}

// Photo is one uploaded picture.
type Photo struct {
	UserID    uuid.UUID
	ID        uuid.UUID
	ObjectKey string
	Title     string
	Width     int
	Height    int
	MimeType  string
	SizeBytes int64
	CreatedAt time.Time
}

// Export is a rendered copy.
type Export struct {
	UserID    uuid.UUID
	ID        uuid.UUID
	PhotoID   uuid.UUID
	ObjectKey string
	MimeType  string
	Width     int
	Height    int
	CreatedAt time.Time
}

// Sentinel errors mapped by HTTP.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrTooLarge = errors.New("file is too large")
)

// WrapInvalid attaches a validation message.
func WrapInvalid(err error) error {
	return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
}
